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

  amp_remote_write_endpoint = "${aws_prometheus_workspace.vertex.prometheus_endpoint}api/v1/remote_write"
  adot_configs = {
    for chain_name, chain in var.chains : chain_name => yamlencode({
      extensions = {
        health_check = {}
        sigv4auth = {
          region  = var.aws_region
          service = "aps"
        }
      }
      receivers = {
        otlp = {
          protocols = {
            http = {
              endpoint = "0.0.0.0:4318"
            }
          }
        }
        prometheus = {
          config = {
            scrape_configs = [{
              job_name        = "vertex-indexer"
              scheme          = "http"
              metrics_path    = "/metrics"
              scrape_interval = "15s"
              scrape_timeout  = "10s"
              static_configs = [{
                targets = ["127.0.0.1:9090"]
                labels = {
                  network = chain_name
                }
              }]
            }]
          }
        }
      }
      processors = {
        batch = {}
      }
      exporters = {
        awsxray = {}
        prometheusremotewrite = {
          endpoint = local.amp_remote_write_endpoint
          auth = {
            authenticator = "sigv4auth"
          }
        }
      }
      service = {
        extensions = ["health_check", "sigv4auth"]
        pipelines = {
          traces = {
            receivers  = ["otlp"]
            processors = ["batch"]
            exporters  = ["awsxray"]
          }
          metrics = {
            receivers  = ["prometheus"]
            processors = ["batch"]
            exporters  = ["prometheusremotewrite"]
          }
        }
      }
    })
  }
}
