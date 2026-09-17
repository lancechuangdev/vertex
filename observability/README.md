# Prometheus queries and troubleshooting runbook

The local Prometheus UI is at <http://localhost:9090>. Use `/query` for PromQL,
`/targets` for scrape health, `/alerts` for alert state, and `/rules` for loaded
rules.

Every Vertex metric has a `chain_id` label. Ethereum mainnet is `1` and Base
mainnet is `8453`. Filter a metric by placing the selector directly after its
name:

```promql
vertex_index_lag_blocks{chain_id="1"}
```

Omit the selector to return all chains. Counters reset when a process restarts,
so use `rate()` or `increase()` rather than subtracting raw counter values.

## Indexing progress

```promql
# Node-reported head
vertex_chain_head
```

```promql
# Next block to process
vertex_checkpoint_next_block
```

```promql
# Blocks remaining
vertex_index_lag_blocks
```

```promql
# Blocks indexed per minute
sum by (chain_id) (rate(vertex_blocks_indexed_total[5m])) * 60
```

```promql
# Number of checkpoint changes in 10 minutes; zero means no progress
changes(vertex_checkpoint_next_block[10m])
```

```promql
# Estimated catch-up time in hours
vertex_index_lag_blocks
/
clamp_min(
  sum by (chain_id) (rate(vertex_blocks_indexed_total[15m])),
  0.000001
)
/
3600
```

```promql
# Completion percentage
100 * vertex_checkpoint_next_block / vertex_chain_head
```

Catch-up time is only an estimate: blocks with many transactions take longer.

## Indexing cycles

```promql
# Successful and failed cycles per minute
sum by (chain_id, result) (rate(vertex_index_runs_total[5m])) * 60
```

```promql
# Cycle failure percentage
100 *
sum by (chain_id) (rate(vertex_index_runs_total{result="error"}[10m]))
/
clamp_min(
  sum by (chain_id) (rate(vertex_index_runs_total[10m])),
  0.000001
)
```

```promql
# Seconds since the last successful cycle
time() - vertex_last_success_timestamp_seconds
```

```promql
# Chains without a successful cycle for 10 minutes
time() - vertex_last_success_timestamp_seconds > 600
```

## RPC traffic, errors, and latency

```promql
# Requests per second by method and result
sum by (chain_id, method, result) (
  rate(vertex_rpc_requests_total[5m])
)
```

```promql
# Total requests per second per chain
sum by (chain_id) (rate(vertex_rpc_requests_total[5m]))
```

```promql
# Errors per minute
sum by (chain_id, method) (
  rate(vertex_rpc_requests_total{result="error"}[5m])
) * 60
```

```promql
# Error percentage by method
100 *
sum by (chain_id, method) (
  rate(vertex_rpc_requests_total{result="error"}[10m])
)
/
clamp_min(
  sum by (chain_id, method) (rate(vertex_rpc_requests_total[10m])),
  0.000001
)
```

```promql
# Average RPC duration
sum by (chain_id, method) (
  rate(vertex_rpc_request_duration_seconds_sum[5m])
)
/
sum by (chain_id, method) (
  rate(vertex_rpc_request_duration_seconds_count[5m])
)
```

```promql
# p95 RPC duration
histogram_quantile(
  0.95,
  sum by (chain_id, method, le) (
    rate(vertex_rpc_request_duration_seconds_bucket[5m])
  )
)
```

RPC duration includes time waiting for the application limiter. At two requests
per second, approximately 0.5 seconds can therefore be normal.

## Reorganizations and backlogs

```promql
# Reorganizations in the last hour
sum by (chain_id) (increase(vertex_reorganizations_total[1h]))
```

```promql
# Reorganizations deeper than six blocks in the last hour
increase(vertex_reorg_depth_blocks_count[1h])
- ignoring(le)
increase(vertex_reorg_depth_blocks_bucket{le="6"}[1h])
```

```promql
vertex_outbox_backlog
```

```promql
delta(vertex_outbox_backlog[15m])
```

```promql
vertex_dead_letter_backlog
```

```promql
increase(vertex_dead_letters_total[1h])
```

The relay publishes valid outbox messages and marks them complete. A growing
backlog therefore indicates delivery failures, an unhealthy relay, or
insufficient publishing capacity.

## Process and scrape health

```promql
# 1 means reachable; 0 means the scrape failed
up{job="vertex-indexer"}
```

```promql
# Resident memory in MiB
process_resident_memory_bytes / 1024 / 1024
```

```promql
# CPU cores used
rate(process_cpu_seconds_total[5m])
```

```promql
go_goroutines
```

```promql
# File-descriptor utilization percentage
100 * process_open_fds / process_max_fds
```

```promql
# Process uptime in hours
(time() - process_start_time_seconds) / 3600
```

```promql
# Process restarts during the last hour
changes(process_start_time_seconds[1h])
```

## Graphing multiple queries

In `/query`, enter the first expression, click **Add query**, enter the second,
select **Graph**, choose a time range, and execute the queries. For example:

```promql
vertex_index_lag_blocks
```

```promql
sum by (chain_id) (rate(vertex_blocks_indexed_total[15m])) * 60
```

Lag and throughput have very different units and scales, so throughput may look
flat beside a lag of millions. Graph completion percentage or catch-up hours
separately in that case. Grafana is better for reusable dashboards and separate
axes.

## Troubleshooting playbooks

### Prometheus cannot reach an indexer

1. Run `up{job="vertex-indexer"}` and inspect `/targets`.
2. Check containers with `docker compose ps`.
3. Open `http://localhost:9091/healthz` for Ethereum or port `9092` for Base.
4. Inspect recent logs:

   ```bash
   docker compose logs --since=10m indexer-ethereum indexer-base
   ```

### The checkpoint is not advancing

Check these together:

```promql
changes(vertex_checkpoint_next_block[10m])
```

```promql
time() - vertex_last_success_timestamp_seconds
```

```promql
sum by (chain_id, method, result) (
  rate(vertex_rpc_requests_total[10m])
)
```

- RPC errors indicate a provider, quota, or network problem.
- Sustained receipt traffic can mean a transaction-heavy block is processing.
- No traffic with `up == 1` can mean the runner is in backoff.
- `up == 0` indicates a container or scrape-network problem.

### The provider returns HTTP 429

Compare error traffic with total traffic using the RPC queries above. Reduce
the per-chain limit in `.env`:

```dotenv
ETHEREUM_RPC_RATE_LIMIT=1
BASE_RPC_RATE_LIMIT=1
RPC_CONCURRENCY=1
```

Then recreate the workers:

```bash
docker compose up -d --build --force-recreate \
  indexer-ethereum indexer-base
```

The client honors `Retry-After`, and runner retries use exponential backoff
with jitter. Persistent 429s at a low rate usually mean the provider's
account-level credit quota is exhausted.

### Indexing lag is growing

Graph lag, throughput, RPC errors, and p95 duration. If RPC calls succeed but
throughput remains below chain production, increase rate and concurrency only
within the provider quota, upgrade the RPC plan, or start at a newer block when
history is unnecessary. `START_BLOCK` only applies when no checkpoint exists.

### RPC calls are slow

Compare p95 duration by method and chain. If every method degrades, investigate
the provider and network. If receipts alone degrade, the current block may be
transaction-heavy. Account for limiter wait time before treating roughly
`1 / RPC_RATE_LIMIT` seconds as network latency.

### Memory continuously grows

Graph resident memory and the Go heap together over several hours:

```promql
process_resident_memory_bytes / 1024 / 1024
```

```promql
go_memstats_heap_alloc_bytes / 1024 / 1024
```

If both rise across repeated garbage collections, investigate retained Go
objects. If heap usage stabilizes while resident memory stays higher, the Go
runtime may merely be retaining reusable memory.

### Outbox or dead-letter backlog grows

Use `delta(vertex_outbox_backlog[15m])` to distinguish a stable backlog from a
growing one. A growing outbox indicates inadequate publishing capacity—or,
currently, no publisher. A nonzero dead-letter backlog means malformed events
need inspection or resolution.

## Alert rules

`alerts.yml` defines lag, RPC failure, reorganization, backlog, stalled-cycle,
and missing-target alerts. After editing it, use `/rules` to verify loading and
`/alerts` to inspect inactive, pending, or firing state.
