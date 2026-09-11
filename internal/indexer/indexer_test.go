package indexer

import (
	"context"
	"testing"

	"github.com/example/vertex/internal/ethrpc"
)

type fakeSource struct {
	latest  uint64
	fetched []uint64
}

func (f *fakeSource) BlockNumber(context.Context) (uint64, error) { return f.latest, nil }
func (f *fakeSource) BlockByNumber(_ context.Context, number uint64) (ethrpc.Block, error) {
	f.fetched = append(f.fetched, number)
	return ethrpc.Block{Number: number}, nil
}

type fakeStore struct {
	next      uint64
	committed []ethrpc.Block
}

func (f *fakeStore) NextBlock(context.Context, uint64, uint64) (uint64, error) { return f.next, nil }
func (f *fakeStore) CommitRange(_ context.Context, _ uint64, _ uint64, blocks []ethrpc.Block) error {
	f.committed = append(f.committed, blocks...)
	return nil
}

func TestRunOnceBoundsRange(t *testing.T) {
	source := &fakeSource{latest: 20}
	store := &fakeStore{next: 10}
	service := Service{Source: source, Store: store, ChainID: 1, BatchSize: 3}

	count, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if count != 3 || len(store.committed) != 3 || source.fetched[0] != 10 || source.fetched[2] != 12 {
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
