package indexer

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/example/vertex/internal/ethrpc"
)

type BlockSource interface {
	BlockNumber(context.Context) (uint64, error)
	BlockByNumber(context.Context, uint64) (ethrpc.Block, error)
	TransactionReceipt(context.Context, string) (ethrpc.Receipt, error)
}

type Store interface {
	NextBlock(context.Context, uint64, uint64) (uint64, error)
	CommitRange(context.Context, uint64, uint64, []IndexedBlock) error
}

type IndexedBlock struct {
	Block     ethrpc.Block
	Receipts  []ethrpc.Receipt
	Transfers []TokenTransfer
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
	blocks := make([]IndexedBlock, 0, end-next+1)
	for number := next; ; number++ {
		block, err := s.Source.BlockByNumber(ctx, number)
		if err != nil {
			return 0, fmt.Errorf("fetch block %d: %w", number, err)
		}
		indexed := IndexedBlock{Block: block, Receipts: make([]ethrpc.Receipt, 0, len(block.Transactions))}
		for _, transaction := range block.Transactions {
			receipt, err := s.Source.TransactionReceipt(ctx, transaction.Hash)
			if err != nil {
				return 0, fmt.Errorf("fetch receipt %s: %w", transaction.Hash, err)
			}
			if receipt.TransactionHash != transaction.Hash || receipt.BlockNumber != block.Number {
				return 0, fmt.Errorf("receipt %s does not belong to block %d", receipt.TransactionHash, block.Number)
			}
			indexed.Receipts = append(indexed.Receipts, receipt)
			for _, log := range receipt.Logs {
				transfer, ok, err := DecodeTokenTransfer(transaction.Hash, log)
				if err != nil {
					return 0, fmt.Errorf("decode transaction %s log %d: %w", transaction.Hash, log.Index, err)
				}
				if ok {
					indexed.Transfers = append(indexed.Transfers, transfer)
				}
			}
		}
		blocks = append(blocks, indexed)
		if number == end {
			break
		}
	}
	if err := s.Store.CommitRange(ctx, s.ChainID, next, blocks); err != nil {
		return 0, fmt.Errorf("commit blocks %d-%d: %w", next, end, err)
	}
	return uint64(len(blocks)), nil
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
