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
- [ ] **Step 2 — Persist blocks and checkpoints**
  - Add PostgreSQL migrations for blocks and per-chain checkpoints.
  - Fetch a bounded block range and advance its checkpoint atomically.
  - Use uniqueness constraints so replaying a range is safe.
- [ ] **Step 3 — Index transactions and EVM logs**
  - Store transactions, receipts, and selected contract events.
  - Decode ERC-20 `Transfer` logs and normalize addresses and quantities.
  - Make writes idempotent with natural chain identifiers.
- [ ] **Step 4 — Track confirmations and publish an outbox**
  - Separate observed blocks from finalized business events.
  - Promote events only after a configurable confirmation depth.
  - Write downstream messages to a transactional outbox.
- [ ] **Step 5 — Detect and recover from reorganizations**
  - Compare stored parent hashes with the node's canonical chain.
  - Find the common ancestor and invalidate orphaned chain data.
  - Replay forward without duplicating downstream effects.
- [ ] **Step 6 — Operate continuously and recover safely**
  - Add polling, bounded concurrency, retries with backoff, and graceful shutdown.
  - Add explicit replay commands and poison-event/dead-letter handling.
  - Protect concurrent workers with leases or advisory locks.
- [ ] **Step 7 — Add production observability**
  - Expose health, readiness, Prometheus metrics, structured logs, and traces.
  - Alert on index lag, RPC failures, reorg depth, and outbox backlog.
- [ ] **Step 8 — Containerize and deploy on AWS**
  - Add Docker, local Compose, and Terraform for ECS Fargate, RDS, MSK,
    ECR, secrets, autoscaling, backups, and alarms.

## Step 1: run it

Requirements: Go 1.25+ and access to an EVM-compatible JSON-RPC endpoint.

```bash
export RPC_URL="https://your-ethereum-rpc.example"
export EXPECTED_CHAIN_ID="1"
go run ./cmd/indexer
```

Successful startup prints the verified chain ID and the node's latest block,
then exits. This is a startup probe, not yet a continuous indexer.

Configuration:

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `RPC_URL` | yes | — | HTTP(S) EVM JSON-RPC endpoint |
| `EXPECTED_CHAIN_ID` | no | `1` | Decimal chain ID used to prevent indexing the wrong network |
| `RPC_TIMEOUT` | no | `10s` | Timeout for each JSON-RPC request |

Run the tests:

```bash
go test ./...
```

## Current layout

```text
cmd/indexer/       service entry point and startup probe
internal/config/   environment configuration and validation
internal/ethrpc/   small typed EVM JSON-RPC client
```

## Design invariants

- Chain identity is verified before any data can be written.
- The database will be the source of truth for progress, not process memory.
- A block range can be processed repeatedly with the same result.
- Downstream messages will never be published inside a database transaction;
  an outbox will bridge that boundary.
- Confirmed does not mean irreversible: reorg recovery remains possible.
