package indexer

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/vertex/internal/ethrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type retrySource struct {
	fakeSource
	failures atomic.Int32
}

func (s *retrySource) BlockNumber(context.Context) (uint64, error) {
	if s.failures.Add(-1) >= 0 {
		return 0, errors.New("temporary RPC failure")
	}
	return s.latest, nil
}

func TestRunnerRetriesTransientFailure(t *testing.T) {
	source := &retrySource{fakeSource: fakeSource{latest: 9}}
	source.failures.Store(2)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := Service{
		Source: source, Store: &fakeStore{next: 10}, ChainID: 1, BatchSize: 1,
		Tracer: provider.Tracer("test"),
	}
	runner := Runner{Service: service, RetryInitial: time.Nanosecond, MaxRetries: 2}

	if _, err := runner.runWithRetry(context.Background()); err != nil {
		t.Fatalf("runWithRetry() error = %v", err)
	}
	span := endedSpanNamed(t, recorder, "indexer.run_with_retry")
	var retries int
	for _, event := range span.Events() {
		if event.Name == "indexer.retry_scheduled" {
			retries++
		}
	}
	if retries != 2 {
		t.Fatalf("retry events = %d, want 2", retries)
	}
}

func TestRunnerStopsDuringPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := Runner{
		Service:      Service{Source: &fakeSource{latest: 9}, Store: &fakeStore{next: 10}, ChainID: 1, BatchSize: 1},
		PollInterval: time.Hour,
		RetryInitial: time.Millisecond,
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

type retryAfterTestError struct{ delay time.Duration }

func (e retryAfterTestError) Error() string             { return "limited" }
func (e retryAfterTestError) RetryAfter() time.Duration { return e.delay }

func TestRetryDelayHonorsServerMinimum(t *testing.T) {
	serverDelay := 10 * time.Second
	got := retryDelay(fmt.Errorf("wrapped: %w", retryAfterTestError{delay: serverDelay}), time.Second)
	if got < serverDelay || got > serverDelay+serverDelay/4 {
		t.Fatalf("retryDelay() = %v, want between %v and %v", got, serverDelay, serverDelay+serverDelay/4)
	}
}

type concurrentReceiptSource struct {
	current atomic.Int32
	maximum atomic.Int32
}

func (*concurrentReceiptSource) BlockNumber(context.Context) (uint64, error) {
	return 0, nil
}
func (*concurrentReceiptSource) BlockByNumber(context.Context, uint64) (ethrpc.Block, error) {
	return ethrpc.Block{}, nil
}
func (s *concurrentReceiptSource) TransactionReceipt(_ context.Context, hash string) (ethrpc.Receipt, error) {
	current := s.current.Add(1)
	defer s.current.Add(-1)
	for {
		maximum := s.maximum.Load()
		if current <= maximum || s.maximum.CompareAndSwap(maximum, current) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	return ethrpc.Receipt{TransactionHash: hash, BlockNumber: 7}, nil
}

func TestIndexBlockBoundsReceiptConcurrency(t *testing.T) {
	source := &concurrentReceiptSource{}
	transactions := make([]ethrpc.Transaction, 8)
	for i := range transactions {
		transactions[i].Hash = string(rune('a' + i))
	}
	service := Service{Source: source, Concurrency: 2}
	if _, err := service.indexBlock(context.Background(), ethrpc.Block{Number: 7, Transactions: transactions}); err != nil {
		t.Fatalf("indexBlock() error = %v", err)
	}
	if got := source.maximum.Load(); got != 2 {
		t.Fatalf("maximum concurrency = %d, want 2", got)
	}
}

func TestIndexBlockDeadLettersMalformedTransfer(t *testing.T) {
	const hash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	source := &fakeSource{receipts: map[string]ethrpc.Receipt{
		hash: {
			TransactionHash: hash,
			BlockNumber:     7,
			Logs: []ethrpc.Log{{
				Index: 3, Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Topics: []string{transferTopic}, Data: "0x00",
			}},
		},
	}}
	service := Service{Source: source, Concurrency: 2}
	indexed, err := service.indexBlock(context.Background(), ethrpc.Block{
		Number: 7, Transactions: []ethrpc.Transaction{{Hash: hash}},
	})
	if err != nil {
		t.Fatalf("indexBlock() error = %v", err)
	}
	if len(indexed.DeadLetters) != 1 || len(indexed.Transfers) != 0 {
		t.Fatalf("indexed block = %+v", indexed)
	}
}
