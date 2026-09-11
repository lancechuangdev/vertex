package indexer

import (
	"context"
	"fmt"

	"github.com/example/vertex/internal/ethrpc"
)

type BlockSource interface {
	BlockNumber(context.Context) (uint64, error)
	BlockByNumber(context.Context, uint64) (ethrpc.Block, error)
}

type Store interface {
	NextBlock(context.Context, uint64, uint64) (uint64, error)
	CommitRange(context.Context, uint64, uint64, []ethrpc.Block) error
}

type Service struct {
	Source    BlockSource
	Store     Store
	ChainID   uint64
	Start     uint64
	BatchSize uint64
}

// RunOnce fetches and commits at most one bounded range. It returns the number
// of blocks committed.
func (s Service) RunOnce(ctx context.Context) (uint64, error) {
	if s.BatchSize == 0 {
		return 0, fmt.Errorf("batch size must be positive")
	}
	next, err := s.Store.NextBlock(ctx, s.ChainID, s.Start)
	if err != nil {
		return 0, fmt.Errorf("read checkpoint: %w", err)
	}
	latest, err := s.Source.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("read latest block: %w", err)
	}
	if next > latest {
		return 0, nil
	}
	end := next + s.BatchSize - 1
	if end < next || end > latest {
		end = latest
	}
	blocks := make([]ethrpc.Block, 0, end-next+1)
	for number := next; ; number++ {
		block, err := s.Source.BlockByNumber(ctx, number)
		if err != nil {
			return 0, fmt.Errorf("fetch block %d: %w", number, err)
		}
		blocks = append(blocks, block)
		if number == end {
			break
		}
	}
	if err := s.Store.CommitRange(ctx, s.ChainID, next, blocks); err != nil {
		return 0, fmt.Errorf("commit blocks %d-%d: %w", next, end, err)
	}
	return uint64(len(blocks)), nil
}
