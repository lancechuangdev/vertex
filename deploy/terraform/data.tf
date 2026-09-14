resource "aws_db_subnet_group" "this" {
  name       = local.prefix
  subnet_ids = values(aws_subnet.private)[*].id
}

resource "aws_db_instance" "this" {
  identifier                   = local.prefix
  engine                       = "postgres"
  engine_version               = var.database_engine_version
  instance_class               = var.database_instance_class
  db_name                      = var.database_name
  username                     = var.database_username
  manage_master_user_password  = true
  allocated_storage            = var.database_allocated_storage
  max_allocated_storage        = var.database_max_allocated_storage
  storage_type                 = "gp3"
  storage_encrypted            = true
  multi_az                     = var.database_multi_az
  db_subnet_group_name         = aws_db_subnet_group.this.name
  vpc_security_group_ids       = [aws_security_group.database.id]
  publicly_accessible          = false
  backup_retention_period      = var.database_backup_retention_days
  backup_window                = "03:00-04:00"
  maintenance_window           = "sun:04:00-sun:05:00"
  auto_minor_version_upgrade   = true
  deletion_protection          = var.deletion_protection
  skip_final_snapshot          = !var.deletion_protection
  final_snapshot_identifier    = var.deletion_protection ? "${local.prefix}-final" : null
  performance_insights_enabled = true
  copy_tags_to_snapshot        = true
  apply_immediately            = false
}

resource "aws_msk_serverless_cluster" "this" {
  count        = var.enable_msk ? 1 : 0
  cluster_name = local.prefix

  vpc_config {
    subnet_ids         = values(aws_subnet.private)[*].id
    security_group_ids = [aws_security_group.msk[0].id]
  }

  client_authentication {
    sasl {
      iam {
        enabled = true
      }
    }
  }
}
