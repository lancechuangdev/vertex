package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"sync"
	"time"

	"github.com/example/vertex/internal/indexer"
	"github.com/example/vertex/internal/outbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/propagation"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Backlogs(ctx context.Context, chainID uint64) (uint64, uint64, error) {
	var outbox, deadLetters uint64
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM outbox_messages
			 WHERE chain_id = $1 AND published_at IS NULL AND invalidated_at IS NULL),
			(SELECT count(*) FROM dead_letters
			 WHERE chain_id = $1 AND resolved_at IS NULL)
	`, chainID).Scan(&outbox, &deadLetters)
	if err != nil {
		return 0, 0, fmt.Errorf("read operational backlogs: %w", err)
	}
	return outbox, deadLetters, nil
}

// ClaimOutbox leases a batch so multiple relay processes can safely compete.
// A crashed worker's messages become claimable again when locked_until expires.
func (s *Store) ClaimOutbox(ctx context.Context, chainID uint64, workerID string, limit int, lease time.Duration) ([]outbox.Message, error) {
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM outbox_messages
			WHERE chain_id = $1 AND published_at IS NULL AND invalidated_at IS NULL
			  AND available_at <= now()
			  AND (locked_until IS NULL OR locked_until < now())
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE outbox_messages AS message
		SET locked_by = $3, locked_until = now() + $4::interval,
		    attempts = message.attempts + 1, last_error = NULL
		FROM candidates
		WHERE message.id = candidates.id
		RETURNING message.id, message.chain_id, message.event_type,
		          message.deduplication_key, message.payload,
		          COALESCE(message.traceparent, ''), COALESCE(message.tracestate, ''),
		          message.attempts
	`, chainID, limit, workerID, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]outbox.Message, 0, limit)
	for rows.Next() {
		var message outbox.Message
		if err := rows.Scan(&message.ID, &message.ChainID, &message.EventType, &message.DeduplicationKey, &message.Payload, &message.Traceparent, &message.Tracestate, &message.Attempts); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id int64, workerID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET published_at = now(), locked_by = NULL, locked_until = NULL, last_error = NULL
		WHERE id = $1 AND locked_by = $2 AND published_at IS NULL
	`, id, workerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("outbox message %d lease was lost", id)
	}
	return nil
}

func (s *Store) ReleaseOutbox(ctx context.Context, id int64, workerID, lastError string, retryAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE outbox_messages
		SET available_at = $3, locked_by = NULL, locked_until = NULL, last_error = left($4, 2000)
		WHERE id = $1 AND locked_by = $2 AND published_at IS NULL
	`, id, workerID, retryAt, lastError)
	return err
}

// AcquireChainLock holds a PostgreSQL session advisory lock until release is
// called, preventing multiple processes from advancing the same chain.
func (s *Store) AcquireChainLock(ctx context.Context, chainID uint64) (func(context.Context) error, bool, error) {
	if chainID > math.MaxInt64 {
		return nil, false, fmt.Errorf("chain ID %d exceeds advisory lock range", chainID)
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire lock connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(chainID)).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("acquire chain advisory lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	var once sync.Once
	var releaseErr error
	release := func(releaseCtx context.Context) error {
		once.Do(func() {
			defer conn.Release()
			var unlocked bool
			if err := conn.QueryRow(releaseCtx, `SELECT pg_advisory_unlock($1)`, int64(chainID)).Scan(&unlocked); err != nil {
				releaseErr = fmt.Errorf("release chain advisory lock: %w", err)
			} else if !unlocked {
				releaseErr = fmt.Errorf("chain advisory lock was not held")
			}
		})
		return releaseErr
	}
	return release, true, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback(ctx)
	// Every chain service starts from the same image and may start at the same
	// time. Serialize schema changes across those services.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1447383637)`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range files {
		sql, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
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

func (s *Store) BlockHash(ctx context.Context, chainID, number uint64) (string, bool, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `
		SELECT hash FROM blocks WHERE chain_id = $1 AND number = $2
	`, chainID, number).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

// Rewind removes orphaned chain rows and moves the checkpoint in one
// transaction. Pending orphan outbox messages are removed; published records
// are retained as invalidated deduplication tombstones and receive an
// idempotent compensating event.
func (s *Store) Rewind(ctx context.Context, chainID, expectedCheckpoint, replayFrom uint64, compensate bool) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin rewind transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	command, err := tx.Exec(ctx, `
		UPDATE chain_checkpoints
		SET next_block = $2, updated_at = now()
		WHERE chain_id = $1 AND next_block = $3
	`, chainID, replayFrom, expectedCheckpoint)
	if err != nil {
		return fmt.Errorf("rewind checkpoint: %w", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("checkpoint changed while rewinding")
	}
	if compensate {
		traceparent, tracestate := traceHeaders(ctx)
		if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages
			(chain_id, event_type, deduplication_key, payload, traceparent, tracestate)
		SELECT message.chain_id,
		       'token_transfer.reverted',
		       'revert:' || message.deduplication_key,
		       message.payload || jsonb_build_object(
		           'event_type', 'token_transfer.reverted',
		           'original_event_key', message.deduplication_key,
		           'orphaned_block_hash', block.hash,
		           'reason', 'chain_reorganization'
		       ), NULLIF($3, ''), NULLIF($4, '')
		FROM outbox_messages AS message
		JOIN blocks AS block
		  ON block.chain_id = message.chain_id
		 AND block.number = (message.payload->>'block_number')::numeric
		WHERE message.chain_id = $1
		  AND message.event_type = 'token_transfer.confirmed'
		  AND message.published_at IS NOT NULL
		  AND message.invalidated_at IS NULL
		  AND block.number >= $2
		ON CONFLICT (chain_id, event_type, deduplication_key) DO NOTHING
		`, chainID, replayFrom, traceparent, tracestate); err != nil {
			return fmt.Errorf("enqueue orphan reversal messages: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM outbox_messages
		WHERE chain_id = $1
		  AND event_type = 'token_transfer.confirmed'
		  AND published_at IS NULL
		  AND (payload->>'block_number')::numeric >= $2
	`, chainID, replayFrom); err != nil {
		return fmt.Errorf("delete pending orphan messages: %w", err)
	}
	if compensate {
		if _, err := tx.Exec(ctx, `
			UPDATE outbox_messages
			SET invalidated_at = now()
			WHERE chain_id = $1
			  AND event_type = 'token_transfer.confirmed'
			  AND published_at IS NOT NULL AND invalidated_at IS NULL
			  AND (payload->>'block_number')::numeric >= $2
		`, chainID, replayFrom); err != nil {
			return fmt.Errorf("invalidate published orphan messages: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM blocks
		WHERE chain_id = $1 AND number >= $2
	`, chainID, replayFrom); err != nil {
		return fmt.Errorf("delete orphan blocks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rewind: %w", err)
	}
	return nil
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
		for _, deadLetter := range indexed.DeadLetters {
			payload, err := json.Marshal(map[string]any{
				"address": deadLetter.Address,
				"topics":  deadLetter.Topics,
				"data":    deadLetter.Data,
			})
			if err != nil {
				return fmt.Errorf("encode dead letter %s/%d: %w", deadLetter.TransactionHash, deadLetter.LogIndex, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO dead_letters
					(chain_id, block_number, block_hash, tx_hash, log_index, error, payload)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT (chain_id, tx_hash, log_index, block_hash) DO UPDATE
				SET error = EXCLUDED.error, payload = EXCLUDED.payload,
				    attempts = dead_letters.attempts + 1, last_seen_at = now()
			`, chainID, block.Number, block.Hash, deadLetter.TransactionHash, deadLetter.LogIndex, deadLetter.Error, payload); err != nil {
				return fmt.Errorf("insert dead letter %s/%d: %w", deadLetter.TransactionHash, deadLetter.LogIndex, err)
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

// PromoteConfirmed marks observed blocks and transfers as confirmed and creates
// their downstream messages in the same transaction. Repeating the operation
// is safe: only previously unconfirmed rows are promoted, and each event has a
// stable deduplication key.
func (s *Store) PromoteConfirmed(ctx context.Context, chainID, confirmedThrough uint64) (uint64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin confirmation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var promoted uint64
	traceparent, tracestate := traceHeaders(ctx)
	err = tx.QueryRow(ctx, `
		WITH newly_confirmed_blocks AS (
			UPDATE blocks
			SET confirmed_at = now()
			WHERE chain_id = $1 AND number <= $2 AND confirmed_at IS NULL
			RETURNING number, hash
		), newly_confirmed_transfers AS (
			UPDATE token_transfers AS transfer
			SET confirmed_at = now()
			FROM transaction_receipts AS receipt
			JOIN newly_confirmed_blocks AS block
			  ON block.number = receipt.block_number
			WHERE transfer.chain_id = $1
			  AND receipt.chain_id = transfer.chain_id
			  AND receipt.tx_hash = transfer.tx_hash
			  AND transfer.confirmed_at IS NULL
			RETURNING transfer.tx_hash, transfer.log_index, transfer.token_address,
			          transfer.from_address, transfer.to_address, transfer.value,
			          receipt.block_number, block.hash AS block_hash
		), inserted_messages AS (
			INSERT INTO outbox_messages
				(chain_id, event_type, deduplication_key, payload, traceparent, tracestate)
			SELECT $1, 'token_transfer.confirmed',
			       tx_hash || ':' || log_index::text || ':' || block_hash,
			       jsonb_build_object(
			           'event_type', 'token_transfer.confirmed',
			           'chain_id', $1,
			           'block_number', block_number,
			           'block_hash', block_hash,
			           'transaction_hash', tx_hash,
			           'log_index', log_index,
			           'token_address', token_address,
			           'from_address', from_address,
			           'to_address', to_address,
			           'value', value::text
			       ), NULLIF($3, ''), NULLIF($4, '')
			FROM newly_confirmed_transfers
			ON CONFLICT (chain_id, event_type, deduplication_key) DO UPDATE
			SET invalidated_at = NULL,
			    payload = EXCLUDED.payload
			RETURNING id
		)
		SELECT count(*) FROM newly_confirmed_blocks
	`, chainID, confirmedThrough, traceparent, tracestate).Scan(&promoted)
	if err != nil {
		return 0, fmt.Errorf("promote confirmed data: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit confirmed data: %w", err)
	}
	return promoted, nil
}

func traceHeaders(ctx context.Context) (string, string) {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent"), carrier.Get("tracestate")
}
