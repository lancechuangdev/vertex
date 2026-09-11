CREATE TABLE IF NOT EXISTS blocks (
    chain_id BIGINT NOT NULL CHECK (chain_id > 0),
    number BIGINT NOT NULL CHECK (number >= 0),
    hash TEXT NOT NULL CHECK (hash ~ '^0x[0-9a-f]{64}$'),
    parent_hash TEXT NOT NULL CHECK (parent_hash ~ '^0x[0-9a-f]{64}$'),
    block_timestamp BIGINT NOT NULL CHECK (block_timestamp >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, number),
    UNIQUE (chain_id, hash)
);

CREATE TABLE IF NOT EXISTS chain_checkpoints (
    chain_id BIGINT PRIMARY KEY CHECK (chain_id > 0),
    next_block BIGINT NOT NULL CHECK (next_block >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
