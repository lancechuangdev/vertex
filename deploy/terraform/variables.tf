variable "aws_region" {
  description = "AWS region for all resources."
  type        = string
  default     = "us-west-2"
}

variable "name" {
  description = "Resource name prefix."
  type        = string
  default     = "vertex"
}

variable "environment" {
  description = "Deployment environment name."
  type        = string
  default     = "production"
}

variable "vpc_cidr" {
  description = "CIDR allocated to the Vertex VPC."
  type        = string
  default     = "10.42.0.0/16"
}

variable "image_tag" {
  description = "Immutable image tag already pushed to the ECR repository."
  type        = string
}

variable "chains" {
  description = "One singleton ECS service per chain. RPC secrets must contain the URL as plaintext."
  type = map(object({
    chain_id           = number
    rpc_secret_arn     = string
    start_block        = optional(number, 0)
    confirmation_depth = optional(number, 12)
    block_batch_size   = optional(number, 25)
    poll_interval      = optional(string, "2s")
    rpc_concurrency    = optional(number, 2)
    rpc_rate_limit     = optional(number, 2)
    cpu                = optional(number, 512)
    memory             = optional(number, 1024)
  }))

  validation {
    condition     = length(var.chains) > 0 && length(distinct([for chain in values(var.chains) : chain.chain_id])) == length(var.chains)
    error_message = "At least one chain is required and every chain_id must be unique."
  }
}

variable "database_name" {
  type    = string
  default = "vertex"
}

variable "database_engine_version" {
  description = "PostgreSQL major family. RDS selects and maintains a supported minor release within this family."
  type        = string
  default     = "17"

  validation {
    condition     = var.database_engine_version == "17"
    error_message = "Vertex is currently pinned to the PostgreSQL 17 major family."
  }
}

variable "database_username" {
  type    = string
  default = "vertex"
}

variable "database_instance_class" {
  type    = string
  default = "db.t4g.micro"
}

variable "database_allocated_storage" {
  description = "Initial RDS storage in GiB."
  type        = number
  default     = 20
}

variable "database_max_allocated_storage" {
  description = "RDS storage autoscaling ceiling in GiB."
  type        = number
  default     = 200
}

variable "database_backup_retention_days" {
  type    = number
  default = 14
}

variable "database_multi_az" {
  type    = bool
  default = true
}

variable "deletion_protection" {
  type    = bool
  default = true
}

variable "enable_msk" {
  description = "Provision an IAM-authenticated MSK Serverless cluster for the future outbox publisher."
  type        = bool
  default     = true
}

variable "alarm_sns_topic_arn" {
  description = "Optional SNS topic receiving CloudWatch alarm notifications."
  type        = string
  default     = null
}

variable "tags" {
  type    = map(string)
  default = {}
}
