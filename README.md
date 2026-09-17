# Vertex EVM Indexer

A production-oriented EVM blockchain indexer built step by step. The finished
service will turn canonical on-chain blocks, transactions, and logs into
reliable downstream events for deposit, withdrawal, ledger, reconciliation,
and reporting workflows.

The implementation intentionally grows in small, verifiable slices. Complete
and test one step before moving to the next; the unchecked items are design
direction, not code that already exists.

## Implementation roadmap

- [x] **Step 1 — Bootstrap and verify the chain connection**
  - Load configuration from environment variables.
  - Call `eth_chainId` and reject an unexpected network.
  - Call `eth_blockNumber` to prove the node is usable.
  - Give every RPC request a timeout and return useful errors.
- [x] **Step 2 — Persist blocks and checkpoints**
  - Add PostgreSQL migrations for blocks and per-chain checkpoints.
  - Fetch a bounded block range and advance its checkpoint atomically.
  - Use uniqueness constraints so replaying a range is safe.
- [x] **Step 3 — Index transactions and EVM logs**
  - Store transactions, receipts, and selected contract events.
  - Decode ERC-20 `Transfer` logs and normalize addresses and quantities.
  - Make writes idempotent with natural chain identifiers.
- [x] **Step 4 — Track confirmations and publish an outbox**
  - Separate observed blocks from finalized business events.
  - Promote events only after a configurable confirmation depth.
  - Write downstream messages to a transactional outbox.
- [x] **Step 5 — Detect and recover from reorganizations**
  - Compare stored parent hashes with the node's canonical chain.
  - Find the common ancestor and invalidate orphaned chain data.
  - Replay forward without duplicating downstream effects.
  - Emit compensating events for published transfers orphaned by a reorganization.
- [x] **Step 6 — Operate continuously and recover safely**
  - Add polling, bounded concurrency, retries with backoff, and graceful shutdown.
  - Add explicit replay commands and poison-event/dead-letter handling.
  - Protect concurrent workers with leases or advisory locks.
- [x] **Step 7 — Add production observability**
  - Expose health, readiness, Prometheus metrics, structured logs, and traces.
  - Alert on index lag, RPC failures, reorg depth, and outbox backlog.
- [x] **Step 8 — Containerize and deploy on AWS**
  - Add Docker, local Compose, and Terraform for ECS Fargate, RDS, MSK,
    ECR, secrets, autoscaling, backups, and alarms.

## Run it

Requirements: Go 1.25+, PostgreSQL, and access to an EVM-compatible JSON-RPC endpoint.

```bash
export RPC_URL="https://your-ethereum-rpc.example"
export EXPECTED_CHAIN_ID="1"
export DATABASE_URL="postgres://postgres:postgres@localhost:5432/vertex?sslmode=disable"
export START_BLOCK="0"
export BLOCK_BATCH_SIZE="100"
go run ./cmd/indexer
```

The default `run` command catches up and then polls continuously. It retries
transient failures with exponential backoff and shuts down cleanly on SIGINT or
SIGTERM. Operational commands are:

```bash
go run ./cmd/indexer run                 # continuous service (default)
go run ./cmd/indexer once                # index at most one bounded range
go run ./cmd/indexer replay-and-run 19000000 # rewind and replay continuously
```

Configuration:

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `RPC_URL` | yes | — | HTTP(S) EVM JSON-RPC endpoint |
| `DATABASE_URL` | yes | — | PostgreSQL connection URL |
| `EXPECTED_CHAIN_ID` | no | `1` | Decimal chain ID used to prevent indexing the wrong network |
| `RPC_TIMEOUT` | no | `10s` | Timeout for each JSON-RPC request |
| `START_BLOCK` | no | `0` | First block used when a chain has no checkpoint |
| `BLOCK_BATCH_SIZE` | no | `100` | Blocks per run, from 1 through 1000 |
| `CONFIRMATION_DEPTH` | no | `12` | Number of blocks required before observed events are promoted |
| `POLL_INTERVAL` | no | `2s` | Delay between checks while caught up or after exhausted retries |
| `RPC_CONCURRENCY` | no | `8` | Maximum concurrent receipt requests, from 1 through 128 |
| `RPC_RATE_LIMIT` | no | `5` | Maximum RPC requests per second for this process |
| `MAX_RETRIES` | no | `4` | Retries per failed indexing range, from 0 through 20 |
| `RETRY_INITIAL_DELAY` | no | `500ms` | Initial exponential retry delay |
| `OBSERVABILITY_ADDR` | no | `:9090` | Listen address for health, readiness, and Prometheus metrics |
| `OUTBOX_BATCH_SIZE` | no | `100` | Maximum rows leased in one relay pass |
| `OUTBOX_POLL_INTERVAL` | no | `1s` | Delay when the relay has no full batch to drain |
| `OUTBOX_LEASE` | no | `30s` | Time before an interrupted delivery can be reclaimed |
| `OUTBOX_RETRY_INITIAL_DELAY` | no | `1s` | Initial failed-publish retry delay |

Operational HTTP endpoints:

- `/healthz` reports process liveness.
- `/readyz` verifies database connectivity after startup and lock acquisition.
- `/metrics` exposes Prometheus metrics for indexing, RPC calls, reorgs, dead letters, and outbox backlog.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` to
export OpenTelemetry traces over OTLP/HTTP. Without an endpoint, tracing uses a
no-op provider. Prometheus alert rules are provided in
`observability/alerts.yml`; common PromQL queries and incident playbooks are in
`observability/README.md`.

The built-in relay claims unpublished rows with `FOR UPDATE SKIP LOCKED`, logs
each message as its delivery transport, and then marks it published.
Delivery is at least once: consumers must deduplicate with
`deduplication_key`. Failed deliveries use exponential backoff (capped at one
hour), and expired leases are automatically reclaimed.

Run the tests:

```bash
go test ./...
```

## Run multiple chains locally

The service uses one process per chain. Copy `.env.example` to `.env`, set the
Ethereum and Base RPC URLs, then start PostgreSQL, both indexers, and
Prometheus:

```bash
cp .env.example .env
docker compose up --build
```

Ethereum metrics are exposed at `localhost:9091`, Base metrics at
`localhost:9092`, the Prometheus UI at `localhost:9090`, and the Jaeger UI at
`http://localhost:16686`. Both indexers share
PostgreSQL safely because persisted records, checkpoints, and worker locks are
scoped by chain ID. Every application metric also carries a `chain_id` label.

Polling is configured per chain because block production differs. The local
defaults are `ETHEREUM_POLL_INTERVAL=5s` and `BASE_POLL_INTERVAL=1s`. Polling
only occurs while caught up or after exhausted retries; catch-up cycles continue
without an added poll delay. `RPC_RATE_LIMIT` separately caps all JSON-RPC calls
per second. Polls consume that budget, but most usage comes from block and
transaction-receipt requests.

For the OpenTelemetry trace-link, open Jaeger, select `vertex-indexer`,
and find an `outbox.publish` span. Its **Links** section points to the earlier
`indexer.run_once` trace that transactionally created the message. A link is
used instead of a parent/child edge because indexing and delivery are separate
asynchronous operations.

## Deploy to AWS

Terraform in `deploy/terraform` creates one singleton ECS Fargate service per
configured chain, plus ECR, encrypted RDS, optional MSK Serverless, private
networking, Secrets Manager integration, backups, storage autoscaling, logs,
and CloudWatch alarms. See `deploy/terraform/README.md` for prerequisites and
deployment commands.

RDS credentials are managed by RDS and injected into ECS without placing the
password in Terraform configuration. RPC URLs must be created as Secrets
Manager secrets before deployment. The built-in publisher writes outbox
deliveries to structured logs; the optional MSK cluster is reserved for
replacing that transport with a Kafka publisher.

## Current layout

```text
cmd/indexer/       service entry point, commands, and graceful shutdown
internal/config/   environment configuration and validation
internal/ethrpc/   small typed EVM JSON-RPC client
internal/indexer/  bounded range orchestration and continuous runner
internal/postgres/ migrations, dead letters, locking, and atomic persistence
internal/observability/ health endpoints, Prometheus metrics, and OTLP traces
internal/outbox/        leased at-least-once relay and publisher
observability/       Prometheus alert rules
deploy/terraform/    multi-chain AWS ECS, RDS, MSK, ECR, and monitoring stack
```

## Design invariants

- Chain identity is verified before any data can be written.
- The database will be the source of truth for progress, not process memory.
- A block range can be processed repeatedly with the same result.
- Downstream messages will never be published inside a database transaction;
  an outbox will bridge that boundary.
- Confirmed does not mean irreversible: reorg recovery remains possible.
