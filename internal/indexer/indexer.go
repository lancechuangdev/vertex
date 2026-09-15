package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/example/vertex/internal/ethrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type BlockSource interface {
	BlockNumber(context.Context) (uint64, error)
	BlockByNumber(context.Context, uint64) (ethrpc.Block, error)
	TransactionReceipt(context.Context, string) (ethrpc.Receipt, error)
}

type Store interface {
	NextBlock(context.Context, uint64, uint64) (uint64, error)
	BlockHash(context.Context, uint64, uint64) (string, bool, error)
	Rewind(ctx context.Context, chainID, expectedCheckpoint, replayFrom uint64, compensate bool) error
	CommitRange(context.Context, uint64, uint64, []IndexedBlock) error
	PromoteConfirmed(context.Context, uint64, uint64) (uint64, error)
}

type Observer interface {
	ObserveChain(latest, next uint64)
	ObserveBlocks(uint64)
	ObserveReorganization(uint64)
	ObserveDeadLetters(uint64)
}

type IndexedBlock struct {
	Block       ethrpc.Block
	Receipts    []ethrpc.Receipt
	Transfers   []TokenTransfer
	DeadLetters []DeadLetter
}

type DeadLetter struct {
	TransactionHash string
	LogIndex        uint64
	Address         string
	Topics          []string
	Data            string
	Error           string
}

type TokenTransfer struct {
	TransactionHash string
	LogIndex        uint64
	TokenAddress    string
	FromAddress     string
	ToAddress       string
	Value           string
}

const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

type Service struct {
	Source            BlockSource
	Store             Store
	ChainID           uint64
	Start             uint64
	BatchSize         uint64
	ConfirmationDepth uint64
	Concurrency       int
	Observer          Observer
	Tracer            trace.Tracer
}

// RunOnce fetches and commits at most one bounded range. It returns the number
// of blocks committed.
func (s Service) RunOnce(ctx context.Context) (uint64, error) {
	started := time.Now()
	var span trace.Span
	if s.Tracer != nil {
		ctx, span = s.Tracer.Start(ctx, "indexer.run_once", trace.WithAttributes(attribute.Int64("chain.id", int64(s.ChainID))))
		defer span.End()
	}
	indexed, err := s.runOnce(ctx)
	if span != nil {
		span.SetAttributes(attribute.Int64("indexer.blocks", int64(indexed)), attribute.Int64("indexer.duration_ms", time.Since(started).Milliseconds()))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
	return indexed, err
}

func (s Service) runOnce(ctx context.Context) (uint64, error) {
	if s.BatchSize == 0 {
		return 0, fmt.Errorf("batch size must be positive")
	}
	next, err := s.Store.NextBlock(ctx, s.ChainID, s.Start)
	if err != nil {
		return 0, fmt.Errorf("read checkpoint: %w", err)
	}
	trace.SpanFromContext(ctx).AddEvent("checkpoint.loaded", trace.WithAttributes(
		attribute.Int64("checkpoint.next_block", int64(next)),
	))
	latest, err := s.Source.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("read latest block: %w", err)
	}
	trace.SpanFromContext(ctx).AddEvent("chain_head.observed", trace.WithAttributes(
		attribute.Int64("chain.head", int64(latest)),
	))
	if s.Observer != nil {
		s.Observer.ObserveChain(latest, next)
	}
	next, err = s.recoverReorganization(ctx, next, latest)
	if err != nil {
		return 0, err
	}
	if next > latest {
		return 0, s.promoteConfirmed(ctx, latest)
	}
	end := next + s.BatchSize - 1
	if end < next || end > latest {
		end = latest
	}
	blocks := make([]IndexedBlock, 0, end-next+1)
	var previousHash string
	if next > s.Start {
		var found bool
		previousHash, found, err = s.Store.BlockHash(ctx, s.ChainID, next-1)
		if err != nil {
			return 0, fmt.Errorf("read block %d hash: %w", next-1, err)
		}
		if !found {
			return 0, fmt.Errorf("previous block %d is missing", next-1)
		}
	}
	for number := next; ; number++ {
		block, err := s.Source.BlockByNumber(ctx, number)
		if err != nil {
			return 0, fmt.Errorf("fetch block %d: %w", number, err)
		}
		if previousHash != "" && block.ParentHash != previousHash {
			return 0, fmt.Errorf("block %d parent %s does not match previous hash %s", number, block.ParentHash, previousHash)
		}
		indexed, err := s.indexBlock(ctx, block)
		if err != nil {
			return 0, err
		}
		blocks = append(blocks, indexed)
		previousHash = block.Hash
		if number == end {
			break
		}
	}
	trace.SpanFromContext(ctx).AddEvent("blocks.fetched", trace.WithAttributes(
		attribute.Int64("block.range.start", int64(next)),
		attribute.Int64("block.range.end", int64(end)),
		attribute.Int64("block.count", int64(len(blocks))),
	))
	if err := s.Store.CommitRange(ctx, s.ChainID, next, blocks); err != nil {
		return 0, fmt.Errorf("commit blocks %d-%d: %w", next, end, err)
	}
	trace.SpanFromContext(ctx).AddEvent("range.committed", trace.WithAttributes(
		attribute.Int64("block.range.start", int64(next)),
		attribute.Int64("block.range.end", int64(end)),
		attribute.Int64("block.count", int64(len(blocks))),
	))
	var deadLetters uint64
	for _, block := range blocks {
		deadLetters += uint64(len(block.DeadLetters))
	}
	if deadLetters > 0 {
		trace.SpanFromContext(ctx).AddEvent("dead_letters.created", trace.WithAttributes(
			attribute.Int64("dead_letter.count", int64(deadLetters)),
		))
	}
	if s.Observer != nil {
		s.Observer.ObserveBlocks(uint64(len(blocks)))
		if deadLetters > 0 {
			s.Observer.ObserveDeadLetters(deadLetters)
		}
	}
	if err := s.promoteConfirmed(ctx, latest); err != nil {
		return 0, err
	}
	return uint64(len(blocks)), nil
}

func (s Service) indexBlock(ctx context.Context, block ethrpc.Block) (IndexedBlock, error) {
	type result struct {
		receipt     ethrpc.Receipt
		transfers   []TokenTransfer
		deadLetters []DeadLetter
		err         error
	}
	results := make([]result, len(block.Transactions))
	workers := s.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > len(block.Transactions) {
		workers = len(block.Transactions)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				transaction := block.Transactions[i]
				receipt, err := s.Source.TransactionReceipt(ctx, transaction.Hash)
				if err != nil {
					results[i].err = fmt.Errorf("fetch receipt %s: %w", transaction.Hash, err)
					continue
				}
				if receipt.TransactionHash != transaction.Hash || receipt.BlockNumber != block.Number {
					results[i].err = fmt.Errorf("receipt %s does not belong to block %d", receipt.TransactionHash, block.Number)
					continue
				}
				results[i].receipt = receipt
				for _, log := range receipt.Logs {
					transfer, ok, err := DecodeTokenTransfer(transaction.Hash, log)
					if err != nil {
						results[i].deadLetters = append(results[i].deadLetters, DeadLetter{
							TransactionHash: transaction.Hash, LogIndex: log.Index, Address: log.Address,
							Topics: log.Topics, Data: log.Data, Error: err.Error(),
						})
						continue
					}
					if ok {
						results[i].transfers = append(results[i].transfers, transfer)
					}
				}
			}
		}()
	}
	for i := range block.Transactions {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return IndexedBlock{}, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()

	indexed := IndexedBlock{Block: block, Receipts: make([]ethrpc.Receipt, 0, len(results))}
	for _, result := range results {
		if result.err != nil {
			return IndexedBlock{}, result.err
		}
		indexed.Receipts = append(indexed.Receipts, result.receipt)
		indexed.Transfers = append(indexed.Transfers, result.transfers...)
		indexed.DeadLetters = append(indexed.DeadLetters, result.deadLetters...)
	}
	return indexed, nil
}

func (s Service) ReplayFrom(ctx context.Context, from uint64) error {
	next, err := s.Store.NextBlock(ctx, s.ChainID, s.Start)
	if err != nil {
		return fmt.Errorf("read checkpoint for replay: %w", err)
	}
	if from < s.Start {
		return fmt.Errorf("replay block %d is before start block %d", from, s.Start)
	}
	if from > next {
		return fmt.Errorf("replay block %d is ahead of checkpoint %d", from, next)
	}
	if from == next {
		return nil
	}
	if err := s.Store.Rewind(ctx, s.ChainID, next, from, false); err != nil {
		return fmt.Errorf("rewind for replay to block %d: %w", from, err)
	}
	return nil
}

// recoverReorganization verifies the last indexed block against the node. On
// a mismatch it walks backward to the common ancestor and atomically rewinds
// storage so the normal range path can replay the canonical chain.
func (s Service) recoverReorganization(ctx context.Context, next, latest uint64) (uint64, error) {
	if next <= s.Start {
		return next, nil
	}
	tip := next - 1
	if tip > latest {
		return 0, fmt.Errorf("node head %d is behind indexed tip %d", latest, tip)
	}

	for number := tip; ; number-- {
		storedHash, found, err := s.Store.BlockHash(ctx, s.ChainID, number)
		if err != nil {
			return 0, fmt.Errorf("read stored block %d: %w", number, err)
		}
		if !found {
			return 0, fmt.Errorf("checkpoint references missing block %d", number)
		}
		canonical, err := s.Source.BlockByNumber(ctx, number)
		if err != nil {
			return 0, fmt.Errorf("verify canonical block %d: %w", number, err)
		}
		if canonical.Hash == storedHash {
			if number == tip {
				return next, nil
			}
			replayFrom := number + 1
			depth := tip - number
			if s.Observer != nil {
				s.Observer.ObserveReorganization(depth)
			}
			if err := s.Store.Rewind(ctx, s.ChainID, next, replayFrom, true); err != nil {
				return 0, fmt.Errorf("rewind to block %d: %w", replayFrom, err)
			}
			trace.SpanFromContext(ctx).AddEvent("chain.reorganization_detected", trace.WithAttributes(
				attribute.Int64("reorg.depth", int64(depth)),
				attribute.Int64("replay.from_block", int64(replayFrom)),
			))
			return replayFrom, nil
		}
		if number == s.Start {
			depth := tip - s.Start + 1
			if s.Observer != nil {
				s.Observer.ObserveReorganization(depth)
			}
			if err := s.Store.Rewind(ctx, s.ChainID, next, s.Start, true); err != nil {
				return 0, fmt.Errorf("rewind to start block %d: %w", s.Start, err)
			}
			trace.SpanFromContext(ctx).AddEvent("chain.reorganization_detected", trace.WithAttributes(
				attribute.Int64("reorg.depth", int64(depth)),
				attribute.Int64("replay.from_block", int64(s.Start)),
			))
			return s.Start, nil
		}
	}
}

func (s Service) promoteConfirmed(ctx context.Context, latest uint64) error {
	if latest < s.ConfirmationDepth {
		return nil
	}
	confirmedThrough := latest - s.ConfirmationDepth
	promoted, err := s.Store.PromoteConfirmed(ctx, s.ChainID, confirmedThrough)
	if err != nil {
		return fmt.Errorf("promote blocks through %d: %w", confirmedThrough, err)
	}
	trace.SpanFromContext(ctx).AddEvent("confirmations.promoted", trace.WithAttributes(
		attribute.Int64("confirmed.through_block", int64(confirmedThrough)),
		attribute.Int64("confirmed.count", int64(promoted)),
	))
	return nil
}

// DecodeTokenTransfer recognizes the canonical ERC-20 Transfer event. Events
// with a different signature (including ERC-721's four-topic form) are ignored.
func DecodeTokenTransfer(transactionHash string, log ethrpc.Log) (TokenTransfer, bool, error) {
	if len(log.Topics) == 0 || log.Topics[0] != transferTopic {
		return TokenTransfer{}, false, nil
	}
	if len(log.Topics) != 3 || len(log.Data) != 66 {
		return TokenTransfer{}, false, fmt.Errorf("malformed ERC-20 Transfer event")
	}
	from, err := addressFromTopic(log.Topics[1])
	if err != nil {
		return TokenTransfer{}, false, fmt.Errorf("from address: %w", err)
	}
	to, err := addressFromTopic(log.Topics[2])
	if err != nil {
		return TokenTransfer{}, false, fmt.Errorf("to address: %w", err)
	}
	value, ok := new(big.Int).SetString(log.Data[2:], 16)
	if !ok {
		return TokenTransfer{}, false, fmt.Errorf("invalid transfer value %q", log.Data)
	}
	return TokenTransfer{
		TransactionHash: transactionHash,
		LogIndex:        log.Index,
		TokenAddress:    log.Address,
		FromAddress:     from,
		ToAddress:       to,
		Value:           value.String(),
	}, true, nil
}

func addressFromTopic(topic string) (string, error) {
	if len(topic) != 66 || !strings.HasPrefix(topic, "0x") {
		return "", fmt.Errorf("invalid address topic %q", topic)
	}
	prefix := topic[2:26]
	if prefix != strings.Repeat("0", 24) {
		return "", fmt.Errorf("address topic has non-zero padding")
	}
	address := topic[26:]
	if _, err := hex.DecodeString(address); err != nil {
		return "", fmt.Errorf("invalid address topic %q", topic)
	}
	return "0x" + strings.ToLower(address), nil
}
