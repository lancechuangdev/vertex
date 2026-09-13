package observability

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	ChainHead       prometheus.Gauge
	CheckpointNext  prometheus.Gauge
	IndexLag        prometheus.Gauge
	OutboxBacklog   prometheus.Gauge
	DeadLetterQueue prometheus.Gauge
	RPCRequests     *prometheus.CounterVec
	RPCDuration     *prometheus.HistogramVec
	Runs            *prometheus.CounterVec
	Blocks          prometheus.Counter
	Reorganizations prometheus.Counter
	ReorgDepth      prometheus.Histogram
	DeadLetters     prometheus.Counter
	LastSuccess     prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ChainHead:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_chain_head", Help: "Latest block reported by the RPC node."}),
		CheckpointNext:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_checkpoint_next_block", Help: "Next block the indexer will process."}),
		IndexLag:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_index_lag_blocks", Help: "Blocks between the checkpoint and chain head."}),
		OutboxBacklog:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_outbox_backlog", Help: "Unpublished, valid outbox messages."}),
		DeadLetterQueue: prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_dead_letter_backlog", Help: "Unresolved dead-letter events."}),
		RPCRequests:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vertex_rpc_requests_total", Help: "EVM RPC calls by method and result."}, []string{"method", "result"}),
		RPCDuration:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vertex_rpc_request_duration_seconds", Help: "EVM RPC request latency.", Buckets: prometheus.DefBuckets}, []string{"method"}),
		Runs:            prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vertex_index_runs_total", Help: "Completed indexing attempts by result."}, []string{"result"}),
		Blocks:          prometheus.NewCounter(prometheus.CounterOpts{Name: "vertex_blocks_indexed_total", Help: "Blocks committed by this process."}),
		Reorganizations: prometheus.NewCounter(prometheus.CounterOpts{Name: "vertex_reorganizations_total", Help: "Chain reorganizations detected."}),
		ReorgDepth:      prometheus.NewHistogram(prometheus.HistogramOpts{Name: "vertex_reorg_depth_blocks", Help: "Depth of detected chain reorganizations.", Buckets: []float64{1, 2, 3, 6, 12, 24, 48, 96}}),
		DeadLetters:     prometheus.NewCounter(prometheus.CounterOpts{Name: "vertex_dead_letters_total", Help: "Poison events written to the dead-letter queue."}),
		LastSuccess:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "vertex_last_success_timestamp_seconds", Help: "Unix timestamp of the last successful indexing attempt."}),
	}
	reg.MustRegister(m.ChainHead, m.CheckpointNext, m.IndexLag, m.OutboxBacklog, m.DeadLetterQueue,
		m.RPCRequests, m.RPCDuration, m.Runs, m.Blocks, m.Reorganizations, m.ReorgDepth, m.DeadLetters, m.LastSuccess)
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) ObserveChain(latest, next uint64) {
	m.ChainHead.Set(float64(latest))
	m.CheckpointNext.Set(float64(next))
	lag := uint64(0)
	if next <= latest {
		lag = latest - next + 1
	}
	m.IndexLag.Set(float64(lag))
}

func (m *Metrics) ObserveBlocks(count uint64)      { m.Blocks.Add(float64(count)) }
func (m *Metrics) ObserveDeadLetters(count uint64) { m.DeadLetters.Add(float64(count)) }
func (m *Metrics) ObserveReorganization(depth uint64) {
	m.Reorganizations.Inc()
	m.ReorgDepth.Observe(float64(depth))
}
func (m *Metrics) ObserveRun(err error) {
	result := "success"
	if err != nil {
		result = "error"
	} else {
		m.LastSuccess.Set(float64(time.Now().Unix()))
	}
	m.Runs.WithLabelValues(result).Inc()
}

func setCount(gauge prometheus.Gauge, count uint64) {
	if count > 1<<53 {
		gauge.Set(math.Pow(2, 53))
		return
	}
	gauge.Set(float64(count))
}

func (m *Metrics) SetBacklogs(outbox, deadLetters uint64) {
	setCount(m.OutboxBacklog, outbox)
	setCount(m.DeadLetterQueue, deadLetters)
}
