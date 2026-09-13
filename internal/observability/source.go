package observability

import (
	"context"
	"time"

	"github.com/example/vertex/internal/ethrpc"
	"github.com/example/vertex/internal/indexer"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Source struct {
	Next    indexer.BlockSource
	Metrics *Metrics
	Tracer  trace.Tracer
}

func (s Source) BlockNumber(ctx context.Context) (uint64, error) {
	value, err := observeRPC(ctx, s, "eth_blockNumber", func(ctx context.Context) (uint64, error) { return s.Next.BlockNumber(ctx) })
	return value, err
}

func (s Source) BlockByNumber(ctx context.Context, number uint64) (ethrpc.Block, error) {
	return observeRPC(ctx, s, "eth_getBlockByNumber", func(ctx context.Context) (ethrpc.Block, error) {
		return s.Next.BlockByNumber(ctx, number)
	})
}

func (s Source) TransactionReceipt(ctx context.Context, hash string) (ethrpc.Receipt, error) {
	return observeRPC(ctx, s, "eth_getTransactionReceipt", func(ctx context.Context) (ethrpc.Receipt, error) {
		return s.Next.TransactionReceipt(ctx, hash)
	})
}

func observeRPC[T any](ctx context.Context, source Source, method string, call func(context.Context) (T, error)) (T, error) {
	started := time.Now()
	var span trace.Span
	if source.Tracer != nil {
		ctx, span = source.Tracer.Start(ctx, "evm.rpc", trace.WithAttributes(attribute.String("rpc.system", "ethereum"), attribute.String("rpc.method", method)))
		defer span.End()
	}
	value, err := call(ctx)
	if source.Metrics != nil {
		result := "success"
		if err != nil {
			result = "error"
		}
		source.Metrics.RPCRequests.WithLabelValues(method, result).Inc()
		source.Metrics.RPCDuration.WithLabelValues(method).Observe(time.Since(started).Seconds())
	}
	if err != nil && span != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return value, err
}
