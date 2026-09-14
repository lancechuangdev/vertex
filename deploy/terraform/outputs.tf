output "ecr_repository_url" {
  value = aws_ecr_repository.indexer.repository_url
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.this.name
}

output "ecs_services" {
  value = { for name, service in aws_ecs_service.indexer : name => service.name }
}

output "database_endpoint" {
  value = aws_db_instance.this.endpoint
}

output "database_master_secret_arn" {
  value     = aws_db_instance.this.master_user_secret[0].secret_arn
  sensitive = true
}

output "msk_cluster_arn" {
  value = var.enable_msk ? aws_msk_serverless_cluster.this[0].arn : null
}
