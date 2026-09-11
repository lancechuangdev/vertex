package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/example/vertex/internal/ethrpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Migrate(ctx context.Context) error {
	sql, err := migrations.ReadFile("migrations/001_blocks.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if _, err := s.pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply migration: %w", err)
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

func (s *Store) CommitRange(ctx context.Context, chainID, expectedNext uint64, blocks []ethrpc.Block) error {
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

	for i, block := range blocks {
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
