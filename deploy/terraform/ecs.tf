resource "aws_ecs_cluster" "this" {
  name = local.prefix

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_cloudwatch_log_group" "indexer" {
  for_each          = var.chains
  name              = "/ecs/${local.prefix}/${each.key}"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "adot" {
  for_each          = var.chains
  name              = "/ecs/${local.prefix}/${each.key}/adot"
  retention_in_days = 30
}

resource "aws_ecs_task_definition" "indexer" {
  for_each                 = var.chains
  family                   = "${local.prefix}-${each.key}"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = each.value.cpu
  memory                   = each.value.memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([
    {
      name                   = "indexer"
      image                  = "${aws_ecr_repository.indexer.repository_url}:${var.image_tag}"
      essential              = true
      command                = ["run"]
      readonlyRootFilesystem = true
      dependsOn = [{
        containerName = "adot-collector"
        condition     = "HEALTHY"
      }]
      linuxParameters = {
        initProcessEnabled = true
      }
      portMappings = [{
        name          = "observability"
        containerPort = 9090
        protocol      = "tcp"
      }]
      environment = [
        { name = "EXPECTED_CHAIN_ID", value = tostring(each.value.chain_id) },
        { name = "DATABASE_HOST", value = aws_db_instance.this.address },
        { name = "DATABASE_PORT", value = tostring(aws_db_instance.this.port) },
        { name = "DATABASE_NAME", value = var.database_name },
        { name = "DATABASE_USER", value = var.database_username },
        { name = "DATABASE_SSLMODE", value = "require" },
        { name = "START_BLOCK", value = tostring(each.value.start_block) },
        { name = "CONFIRMATION_DEPTH", value = tostring(each.value.confirmation_depth) },
        { name = "BLOCK_BATCH_SIZE", value = tostring(each.value.block_batch_size) },
        { name = "POLL_INTERVAL", value = each.value.poll_interval },
        { name = "RPC_CONCURRENCY", value = tostring(each.value.rpc_concurrency) },
        { name = "RPC_RATE_LIMIT", value = tostring(each.value.rpc_rate_limit) },
        { name = "OBSERVABILITY_ADDR", value = ":9090" },
        { name = "OTEL_EXPORTER_OTLP_ENDPOINT", value = "http://localhost:4318" }
      ]
      secrets = [
        { name = "RPC_URL", valueFrom = each.value.rpc_secret_arn },
        { name = "DATABASE_PASSWORD", valueFrom = "${aws_db_instance.this.master_user_secret[0].secret_arn}:password::" }
      ]
      healthCheck = {
        command     = ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:9090/readyz || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.indexer[each.key].name
          awslogs-region        = var.aws_region
          awslogs-stream-prefix = "indexer"
        }
      }
    },
    {
      name              = "adot-collector"
      image             = var.adot_collector_image
      essential         = true
      command           = ["--config=env:ADOT_CONFIG"]
      cpu               = 64
      memoryReservation = 128
      stopTimeout       = 120
      environment = [
        { name = "AWS_REGION", value = var.aws_region },
        { name = "ADOT_CONFIG", value = local.adot_configs[each.key] }
      ]
      healthCheck = {
        command     = ["CMD-SHELL", "/healthcheck"]
        interval    = 10
        timeout     = 5
        retries     = 3
        startPeriod = 10
      }
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          awslogs-group         = aws_cloudwatch_log_group.adot[each.key].name
          awslogs-region        = var.aws_region
          awslogs-stream-prefix = "collector"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "indexer" {
  for_each                           = var.chains
  name                               = "indexer-${each.key}"
  cluster                            = aws_ecs_cluster.this.id
  task_definition                    = aws_ecs_task_definition.indexer[each.key].arn
  desired_count                      = 1
  launch_type                        = "FARGATE"
  platform_version                   = "LATEST"
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100
  enable_execute_command             = true
  propagate_tags                     = "SERVICE"

  network_configuration {
    subnets          = values(aws_subnet.private)[*].id
    security_groups  = [aws_security_group.indexer.id]
    assign_public_ip = false
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  depends_on = [
    aws_iam_role_policy_attachment.execution,
    aws_iam_role_policy.task_amp,
    aws_iam_role_policy.task_xray
  ]
}
