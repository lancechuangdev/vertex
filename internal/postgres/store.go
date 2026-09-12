package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/example/vertex/internal/indexer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Migrate(ctx context.Context) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range files {
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := s.pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) NextBlock(ctx context.Context, chainID, start uint64) (uint64, error) {
	var next uint64
	err := s.pool.QueryRow(ctx, `
		SELECT next_block FROM chain_checkpoints WHERE chain_id = $1
	`, chainID).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return start, nil
	}
	return next, err
}

func (s *Store) CommitRange(ctx context.Context, chainID, expectedNext uint64, blocks []indexer.IndexedBlock) error {
	if len(blocks) == 0 {
		return nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var actualNext uint64
	err = tx.QueryRow(ctx, `
		INSERT INTO chain_checkpoints (chain_id, next_block) VALUES ($1, $2)
		ON CONFLICT (chain_id) DO UPDATE SET next_block = chain_checkpoints.next_block
		RETURNING next_block
	`, chainID, expectedNext).Scan(&actualNext)
	if err != nil {
		return err
	}
	if actualNext != expectedNext {
		return fmt.Errorf("checkpoint moved: expected %d, found %d", expectedNext, actualNext)
	}

	for i, indexed := range blocks {
		block := indexed.Block
		want := expectedNext + uint64(i)
		if block.Number != want {
			return fmt.Errorf("non-contiguous range: got block %d, want %d", block.Number, want)
		}
		var hash string
		err = tx.QueryRow(ctx, `
			INSERT INTO blocks (chain_id, number, hash, parent_hash, block_timestamp)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (chain_id, number) DO UPDATE SET hash = blocks.hash
			RETURNING hash
		`, chainID, block.Number, block.Hash, block.ParentHash, block.Timestamp).Scan(&hash)
		if err != nil {
			return err
		}
		if hash != block.Hash {
			return fmt.Errorf("block %d conflicts with stored hash %s", block.Number, hash)
		}
		for _, transaction := range block.Transactions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO transactions
					(chain_id, tx_hash, block_number, tx_index, from_address, to_address, value)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (chain_id, tx_hash) DO UPDATE SET tx_hash = transactions.tx_hash
			`, chainID, transaction.Hash, block.Number, transaction.Index, transaction.From, transaction.To, transaction.Value); err != nil {
				return fmt.Errorf("insert transaction %s: %w", transaction.Hash, err)
			}
		}
		for _, receipt := range indexed.Receipts {
			if _, err := tx.Exec(ctx, `
				INSERT INTO transaction_receipts
					(chain_id, tx_hash, block_number, status, gas_used, contract_address)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (chain_id, tx_hash) DO UPDATE SET tx_hash = transaction_receipts.tx_hash
			`, chainID, receipt.TransactionHash, receipt.BlockNumber, receipt.Status, receipt.GasUsed, receipt.ContractAddress); err != nil {
				return fmt.Errorf("insert receipt %s: %w", receipt.TransactionHash, err)
			}
		}
		for _, transfer := range indexed.Transfers {
			if _, err := tx.Exec(ctx, `
				INSERT INTO token_transfers
					(chain_id, tx_hash, log_index, token_address, from_address, to_address, value)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (chain_id, tx_hash, log_index)
				DO UPDATE SET tx_hash = token_transfers.tx_hash
			`, chainID, transfer.TransactionHash, transfer.LogIndex, transfer.TokenAddress, transfer.FromAddress, transfer.ToAddress, transfer.Value); err != nil {
				return fmt.Errorf("insert token transfer %s/%d: %w", transfer.TransactionHash, transfer.LogIndex, err)
			}
		}
	}

	next := expectedNext + uint64(len(blocks))
	command, err := tx.Exec(ctx, `
		UPDATE chain_checkpoints SET next_block = $2, updated_at = now()
		WHERE chain_id = $1 AND next_block = $3
	`, chainID, next, expectedNext)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("checkpoint changed while committing range")
	}
	return tx.Commit(ctx)
}
