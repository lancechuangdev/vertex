# Vertex AWS deployment

This stack runs one singleton ECS Fargate service per entry in `chains`. All
services share an encrypted RDS PostgreSQL database; rows, checkpoints, and
worker locks remain isolated by chain ID. A second replica of the same chain is
intentionally not used because the indexer's PostgreSQL advisory lock permits
one active writer per chain.

RDS is pinned to the PostgreSQL 17 major family and automatic minor upgrades
are enabled. This preserves Vertex's tested major-version boundary while RDS
applies supported PostgreSQL 17 maintenance and security releases.

Set `poll_interval` independently for every chain. A useful starting point is
roughly half the block-production interval without polling extremely fast:
Ethereum `5s`, Base `1s`, Arbitrum `500ms`, and opBNB `500ms`. The independent
`rpc_rate_limit` caps total calls per second, including head polls, block
requests, and receipt requests; size it to the RPC provider quota and expected
transaction volume.

It creates a two-AZ VPC, private Fargate tasks, NAT egress for JSON-RPC calls,
ECR, encrypted RDS with automated backups and storage autoscaling, CloudWatch
logs/alarms, and optionally an IAM-authenticated MSK Serverless cluster. MSK is
reserved for the outbox publisher: the current indexer writes the transactional
outbox but does not yet publish it.

## Prerequisites

- Terraform 1.6+
- AWS credentials
- Docker and the AWS CLI
- one Secrets Manager secret per chain containing its raw RPC URL
- an S3/DynamoDB Terraform backend configured by your platform team

Do not commit a real `terraform.tfvars` or RPC URL. This example intentionally
does not create RPC secret values, so credentials never enter Terraform state.

## Deploy

```bash
cp terraform.tfvars.example terraform.tfvars
terraform init
# Bootstrap ECR first so an image exists before ECS starts.
terraform apply -target=aws_ecr_repository.indexer

repository=$(terraform output -raw ecr_repository_url)
aws ecr get-login-password --region us-west-2 |
  docker login --username AWS --password-stdin "${repository%%/*}"
docker build -t "$repository:$(git rev-parse HEAD)" ../..
docker push "$repository:$(git rev-parse HEAD)"

# Set image_tag to that commit SHA, then create the full stack.
terraform plan
terraform apply
```

Use the Git commit SHA as `image_tag`, apply again, and ECS will roll each
service by stopping its old singleton before starting the new task. This avoids
lock contention at the cost of a short deployment pause; the database
checkpoint makes the restart resumable.

RDS master credentials are generated and rotated by RDS-managed Secrets
Manager. ECS injects only the password field, while the host and username are
ordinary task environment values.

CloudWatch alarms cover missing tasks, sustained task CPU, RDS CPU, and two RDS
storage levels: warning below 20 percent and critical below 10 percent of the
configured allocation. Supply `alarm_sns_topic_arn` to route alarm transitions. Application
Prometheus rules remain in `observability/alerts.yml`; connect the private
`:9090/metrics` task endpoints to your chosen managed scraper during platform
integration.
