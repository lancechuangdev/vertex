resource "aws_iam_role" "execution" {
  name = "${local.prefix}-ecs-execution"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "execution" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "execution_secrets" {
  name = "secrets"
  role = aws_iam_role.execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["secretsmanager:GetSecretValue"]
      Resource = concat(
        [for chain in values(var.chains) : chain.rpc_secret_arn],
        [aws_db_instance.this.master_user_secret[0].secret_arn]
      )
    }]
  })
}

resource "aws_iam_role" "task" {
  name = "${local.prefix}-indexer-task"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "task_xray" {
  name = "xray"
  role = aws_iam_role.task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "xray:PutTelemetryRecords",
        "xray:PutTraceSegments"
      ]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role_policy" "task_msk" {
  count = var.enable_msk ? 1 : 0
  name  = "msk"
  role  = aws_iam_role.task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "kafka-cluster:Connect",
        "kafka-cluster:DescribeCluster"
      ]
      Resource = aws_msk_serverless_cluster.this[0].arn
    }]
  })
}
