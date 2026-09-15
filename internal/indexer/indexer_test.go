package indexer

import (
	"context"
	"testing"

	"github.com/example/vertex/internal/ethrpc"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakeSource struct {
	latest   uint64
	fetched  []uint64
	blocks   map[uint64]ethrpc.Block
	receipts map[string]ethrpc.Receipt
}

func (f *fakeSource) TransactionReceipt(_ context.Context, hash string) (ethrpc.Receipt, error) {
	return f.receipts[hash], nil
}

func (f *fakeSource) BlockNumber(context.Context) (uint64, error) { return f.latest, nil }
func (f *fakeSource) BlockByNumber(_ context.Context, number uint64) (ethrpc.Block, error) {
	f.fetched = append(f.fetched, number)
	if block, ok := f.blocks[number]; ok {
		return block, nil
	}
	return ethrpc.Block{Number: number}, nil
}

type fakeStore struct {
	next             uint64
	committed        []IndexedBlock
	confirmedThrough []uint64
	hashes           map[uint64]string
	rewoundTo        []uint64
	rewindCompensate []bool
}

func (f *fakeStore) BlockHash(_ context.Context, _ uint64, number uint64) (string, bool, error) {
	if f.hashes == nil {
		return "", true, nil
	}
	hash, ok := f.hashes[number]
	return hash, ok, nil
}

func (f *fakeStore) Rewind(_ context.Context, _ uint64, _ uint64, replayFrom uint64, compensate bool) error {
	f.rewoundTo = append(f.rewoundTo, replayFrom)
	f.rewindCompensate = append(f.rewindCompensate, compensate)
	return nil
}

func (f *fakeStore) PromoteConfirmed(_ context.Context, _ uint64, through uint64) (uint64, error) {
	f.confirmedThrough = append(f.confirmedThrough, through)
	return 0, nil
}

func (f *fakeStore) NextBlock(context.Context, uint64, uint64) (uint64, error) { return f.next, nil }
func (f *fakeStore) CommitRange(_ context.Context, _ uint64, _ uint64, blocks []IndexedBlock) error {
	f.committed = append(f.committed, blocks...)
	return nil
}

func TestDecodeTokenTransfer(t *testing.T) {
	log := ethrpc.Log{
		Index:   7,
		Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Topics: []string{
			transferTopic,
			"0x0000000000000000000000001111111111111111111111111111111111111111",
			"0x0000000000000000000000002222222222222222222222222222222222222222",
		},
		Data: "0x000000000000000000000000000000000000000000000000000000000000002a",
	}
	transfer, ok, err := DecodeTokenTransfer("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", log)
	if err != nil || !ok {
		t.Fatalf("DecodeTokenTransfer() = _, %v, %v", ok, err)
	}
	if transfer.FromAddress != "0x1111111111111111111111111111111111111111" ||
		transfer.ToAddress != "0x2222222222222222222222222222222222222222" || transfer.Value != "42" {
		t.Fatalf("DecodeTokenTransfer() = %+v", transfer)
	}
}

func TestDecodeTokenTransferIgnoresOtherLogs(t *testing.T) {
	_, ok, err := DecodeTokenTransfer("0xhash", ethrpc.Log{Topics: []string{"0xother"}})
	if err != nil || ok {
		t.Fatalf("DecodeTokenTransfer() = _, %v, %v; want false, nil", ok, err)
	}
}

func TestRunOnceBoundsRange(t *testing.T) {
	source := &fakeSource{latest: 20}
	store := &fakeStore{next: 10}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 3}

	count, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 3 || len(store.committed) != 3 || source.fetched[len(source.fetched)-3] != 10 || source.fetched[len(source.fetched)-1] != 12 {
		t.Fatalf("unexpected range: count=%d fetched=%v committed=%v", count, source.fetched, store.committed)
	}
}

func TestRunOnceStopsAtLatest(t *testing.T) {
	source := &fakeSource{latest: 11}
	store := &fakeStore{next: 10}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 100}

	count, err := service.RunOnce(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("RunOnce() = %d, %v; want 2, nil", count, err)
	}
}

func TestRunOnceCaughtUp(t *testing.T) {
	source := &fakeSource{latest: 9}
	store := &fakeStore{next: 10}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 100}

	count, err := service.RunOnce(context.Background())
	if err != nil || count != 0 || len(store.committed) != 0 {
		t.Fatalf("RunOnce() = %d, %v; committed=%v", count, err, store.committed)
	}
}

func TestRunOnceFetchesReceiptsAndDecodesTransfers(t *testing.T) {
	const txHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	block := ethrpc.Block{Number: 10, Transactions: []ethrpc.Transaction{{Hash: txHash}}}
	receipt := ethrpc.Receipt{
		TransactionHash: txHash,
		BlockNumber:     10,
		Logs: []ethrpc.Log{{
			Index:   2,
			Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Topics: []string{
				transferTopic,
				"0x0000000000000000000000001111111111111111111111111111111111111111",
				"0x0000000000000000000000002222222222222222222222222222222222222222",
			},
			Data: "0x000000000000000000000000000000000000000000000000000000000000002a",
		}},
	}
	source := &fakeSource{latest: 10, blocks: map[uint64]ethrpc.Block{10: block}, receipts: map[string]ethrpc.Receipt{txHash: receipt}}
	store := &fakeStore{next: 10}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 1}

	count, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 1 || len(store.committed) != 1 || len(store.committed[0].Receipts) != 1 || len(store.committed[0].Transfers) != 1 {
		t.Fatalf("unexpected committed batch: %+v", store.committed)
	}
	if store.committed[0].Transfers[0].Value != "42" {
		t.Fatalf("transfer = %+v", store.committed[0].Transfers[0])
	}
}

func TestRunOnceEmitsProgressAndDeadLetterEvents(t *testing.T) {
	const txHash = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	source := &fakeSource{
		latest: 7,
		blocks: map[uint64]ethrpc.Block{
			7: {Number: 7, Transactions: []ethrpc.Transaction{{Hash: txHash}}},
		},
		receipts: map[string]ethrpc.Receipt{
			txHash: {
				TransactionHash: txHash,
				BlockNumber:     7,
				Logs: []ethrpc.Log{{
					Index: 3, Address: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					Topics: []string{transferTopic}, Data: "0x00",
				}},
			},
		},
	}
	service := Service{
		Source: source, Store: &fakeStore{next: 7}, ChainID: 1, BatchSize: 1,
		Tracer: provider.Tracer("test"),
	}

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	span := endedSpanNamed(t, recorder, "indexer.run_once")
	want := map[string]bool{
		"checkpoint.loaded":      false,
		"chain_head.observed":    false,
		"blocks.fetched":         false,
		"range.committed":        false,
		"dead_letters.created":   false,
		"confirmations.promoted": false,
	}
	for _, event := range span.Events() {
		if _, ok := want[event.Name]; ok {
			want[event.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("span event %q not emitted", name)
		}
	}
}

func TestRunOncePromotesOnlyDeepEnoughBlocks(t *testing.T) {
	source := &fakeSource{latest: 20}
	store := &fakeStore{next: 21}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 10, ConfirmationDepth: 6}

	count, err := service.RunOnce(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	if len(store.confirmedThrough) != 1 || store.confirmedThrough[0] != 14 {
		t.Fatalf("confirmed through = %v, want [14]", store.confirmedThrough)
	}
}

func TestRunOnceDoesNotPromoteBeforeDepth(t *testing.T) {
	source := &fakeSource{latest: 5}
	store := &fakeStore{next: 6}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 10, ConfirmationDepth: 6}

	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.confirmedThrough) != 0 {
		t.Fatalf("confirmed through = %v, want none", store.confirmedThrough)
	}
}

func TestRunOnceRewindsToCommonAncestorAndReplays(t *testing.T) {
	const (
		commonHash = "0xcommon"
		newHash11  = "0xnew11"
		newHash12  = "0xnew12"
	)
	source := &fakeSource{
		latest: 13,
		blocks: map[uint64]ethrpc.Block{
			10: {Number: 10, Hash: commonHash},
			11: {Number: 11, Hash: newHash11, ParentHash: commonHash},
			12: {Number: 12, Hash: newHash12, ParentHash: newHash11},
			13: {Number: 13, Hash: "0xnew13", ParentHash: newHash12},
		},
	}
	store := &fakeStore{
		next:   13,
		hashes: map[uint64]string{10: commonHash, 11: "0xold11", 12: "0xold12"},
	}
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := Service{
		Source: source, Store: store, ChainID: 1, Start: 10, BatchSize: 3, ConfirmationDepth: 100,
		Tracer: provider.Tracer("test"),
	}

	count, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 3 || len(store.rewoundTo) != 1 || store.rewoundTo[0] != 11 {
		t.Fatalf("count=%d rewound=%v, want count=3 rewound=[11]", count, store.rewoundTo)
	}
	if len(store.committed) != 3 || store.committed[0].Block.Number != 11 || store.committed[2].Block.Number != 13 {
		t.Fatalf("replayed blocks = %+v", store.committed)
	}
	span := endedSpanNamed(t, recorder, "indexer.run_once")
	for _, event := range span.Events() {
		if event.Name == "chain.reorganization_detected" {
			return
		}
	}
	t.Fatal("chain.reorganization_detected event not emitted")
}

func TestRunOnceRejectsRegressedNodeHead(t *testing.T) {
	source := &fakeSource{latest: 10}
	store := &fakeStore{next: 12, hashes: map[uint64]string{11: "0xstored"}}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 1}

	if _, err := service.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() error = nil, want node-behind error")
	}
	if len(store.rewoundTo) != 0 {
		t.Fatalf("rewound = %v, want none", store.rewoundTo)
	}
}

func TestReplayFromRewindsWithoutReorgCompensation(t *testing.T) {
	store := &fakeStore{next: 20}
	service := Service{Store: store, ChainID: 1, Start: 10}
	if err := service.ReplayFrom(context.Background(), 15); err != nil {
		t.Fatalf("ReplayFrom() error = %v", err)
	}
	if len(store.rewoundTo) != 1 || store.rewoundTo[0] != 15 || store.rewindCompensate[0] {
		t.Fatalf("rewind = %v compensate = %v", store.rewoundTo, store.rewindCompensate)
	}
}

func endedSpanNamed(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("ended span %q not found", name)
	return nil
}
