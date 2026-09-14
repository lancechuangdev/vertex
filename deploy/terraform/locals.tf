data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_caller_identity" "current" {}

locals {
  prefix = "${var.name}-${var.environment}"
  azs    = slice(data.aws_availability_zones.available.names, 0, 2)

  public_subnets = {
    for index, az in local.azs : az => cidrsubnet(var.vpc_cidr, 8, index)
  }
  private_subnets = {
    for index, az in local.azs : az => cidrsubnet(var.vpc_cidr, 8, index + 10)
  }
  alarm_actions = var.alarm_sns_topic_arn == null ? [] : [var.alarm_sns_topic_arn]
}
