ALTER TABLE blocks
    ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

ALTER TABLE token_transfers
    ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS transaction_receipts_block_number_idx
    ON transaction_receipts (chain_id, block_number);

CREATE TABLE IF NOT EXISTS outbox_messages (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    event_type TEXT NOT NULL,
    deduplication_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    UNIQUE (chain_id, event_type, deduplication_key)
);

CREATE INDEX IF NOT EXISTS outbox_messages_unpublished_idx
    ON outbox_messages (id) WHERE published_at IS NULL;
