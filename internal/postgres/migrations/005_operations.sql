CREATE TABLE IF NOT EXISTS dead_letters (
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    block_number BIGINT NOT NULL CHECK (block_number >= 0),
    block_hash TEXT NOT NULL CHECK (block_hash ~ '^0x[0-9a-f]{64}$'),
    tx_hash TEXT NOT NULL CHECK (tx_hash ~ '^0x[0-9a-f]{64}$'),
    log_index BIGINT NOT NULL CHECK (log_index >= 0),
    error TEXT NOT NULL,
    payload JSONB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 1 CHECK (attempts > 0),
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (chain_id, tx_hash, log_index, block_hash)
);

CREATE INDEX IF NOT EXISTS dead_letters_unresolved_idx
    ON dead_letters (chain_id, block_number) WHERE resolved_at IS NULL;
