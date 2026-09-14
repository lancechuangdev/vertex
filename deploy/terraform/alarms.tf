resource "aws_cloudwatch_metric_alarm" "indexer_missing" {
  for_each          = var.chains
  alarm_name        = "${local.prefix}-${each.key}-task-missing"
  alarm_description = "The singleton indexer task for chain ${each.value.chain_id} is not running."
  namespace         = "ECS/ContainerInsights"
  metric_name       = "RunningTaskCount"
  dimensions = {
    ClusterName = aws_ecs_cluster.this.name
    ServiceName = aws_ecs_service.indexer[each.key].name
  }
  statistic           = "Minimum"
  period              = 60
  evaluation_periods  = 2
  datapoints_to_alarm = 2
  comparison_operator = "LessThanThreshold"
  threshold           = 1
  treat_missing_data  = "breaching"
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
}

resource "aws_cloudwatch_metric_alarm" "indexer_cpu" {
  for_each          = var.chains
  alarm_name        = "${local.prefix}-${each.key}-high-cpu"
  alarm_description = "Sustained high CPU on chain ${each.value.chain_id}; tune task size or RPC concurrency."
  namespace         = "AWS/ECS"
  metric_name       = "CPUUtilization"
  dimensions = {
    ClusterName = aws_ecs_cluster.this.name
    ServiceName = aws_ecs_service.indexer[each.key].name
  }
  statistic           = "Average"
  period              = 300
  evaluation_periods  = 3
  comparison_operator = "GreaterThanThreshold"
  threshold           = 85
  treat_missing_data  = "missing"
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
}

resource "aws_cloudwatch_metric_alarm" "database_storage_warning" {
  alarm_name          = "${local.prefix}-database-storage-warning"
  alarm_description   = "RDS free storage is below 20 percent (${var.database_allocated_storage * 0.20} GiB) of configured capacity; review growth and plan expansion."
  namespace           = "AWS/RDS"
  metric_name         = "FreeStorageSpace"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "LessThanThreshold"
  threshold           = var.database_allocated_storage * 0.20 * 1024 * 1024 * 1024
  treat_missing_data  = "missing"
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
  tags                = { Severity = "warning" }
}

resource "aws_cloudwatch_metric_alarm" "database_storage_critical" {
  alarm_name          = "${local.prefix}-database-storage-critical"
  alarm_description   = "RDS free storage is below 10 percent (${var.database_allocated_storage * 0.10} GiB) of configured capacity; expand storage or pause writers immediately."
  namespace           = "AWS/RDS"
  metric_name         = "FreeStorageSpace"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Minimum"
  period              = 300
  evaluation_periods  = 2
  comparison_operator = "LessThanThreshold"
  threshold           = var.database_allocated_storage * 0.10 * 1024 * 1024 * 1024
  treat_missing_data  = "missing"
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
  tags                = { Severity = "critical" }
}

resource "aws_cloudwatch_metric_alarm" "database_cpu" {
  alarm_name          = "${local.prefix}-database-high-cpu"
  alarm_description   = "RDS CPU has exceeded 85 percent for 15 minutes."
  namespace           = "AWS/RDS"
  metric_name         = "CPUUtilization"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  statistic           = "Average"
  period              = 300
  evaluation_periods  = 3
  comparison_operator = "GreaterThanThreshold"
  threshold           = 85
  treat_missing_data  = "missing"
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
}
