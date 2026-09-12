package indexer

import (
	"context"
	"testing"

	"github.com/example/vertex/internal/ethrpc"
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
	next      uint64
	committed []IndexedBlock
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
