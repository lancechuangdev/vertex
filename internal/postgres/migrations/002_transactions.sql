CREATE TABLE IF NOT EXISTS transactions (
    chain_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL CHECK (tx_hash ~ '^0x[0-9a-f]{64}$'),
    block_number BIGINT NOT NULL CHECK (block_number >= 0),
    tx_index BIGINT NOT NULL CHECK (tx_index >= 0),
    from_address TEXT NOT NULL CHECK (from_address ~ '^0x[0-9a-f]{40}$'),
    to_address TEXT CHECK (to_address ~ '^0x[0-9a-f]{40}$'),
    value NUMERIC(78, 0) NOT NULL CHECK (value >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, tx_hash),
    UNIQUE (chain_id, block_number, tx_index),
    FOREIGN KEY (chain_id, block_number) REFERENCES blocks (chain_id, number)
);

CREATE TABLE IF NOT EXISTS transaction_receipts (
    chain_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    block_number BIGINT NOT NULL CHECK (block_number >= 0),
    status SMALLINT CHECK (status IN (0, 1)),
    gas_used BIGINT NOT NULL CHECK (gas_used >= 0),
    contract_address TEXT CHECK (contract_address ~ '^0x[0-9a-f]{40}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, tx_hash),
    FOREIGN KEY (chain_id, tx_hash) REFERENCES transactions (chain_id, tx_hash),
    FOREIGN KEY (chain_id, block_number) REFERENCES blocks (chain_id, number)
);

CREATE TABLE IF NOT EXISTS token_transfers (
    chain_id BIGINT NOT NULL,
    tx_hash TEXT NOT NULL,
    log_index BIGINT NOT NULL CHECK (log_index >= 0),
    token_address TEXT NOT NULL CHECK (token_address ~ '^0x[0-9a-f]{40}$'),
    from_address TEXT NOT NULL CHECK (from_address ~ '^0x[0-9a-f]{40}$'),
    to_address TEXT NOT NULL CHECK (to_address ~ '^0x[0-9a-f]{40}$'),
    value NUMERIC(78, 0) NOT NULL CHECK (value >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, tx_hash) REFERENCES transaction_receipts (chain_id, tx_hash)
);

CREATE INDEX IF NOT EXISTS token_transfers_token_address_idx
    ON token_transfers (chain_id, token_address);
CREATE INDEX IF NOT EXISTS token_transfers_from_address_idx
    ON token_transfers (chain_id, from_address);
CREATE INDEX IF NOT EXISTS token_transfers_to_address_idx
    ON token_transfers (chain_id, to_address);
