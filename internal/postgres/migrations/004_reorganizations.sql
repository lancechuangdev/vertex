ALTER TABLE outbox_messages
    ADD COLUMN IF NOT EXISTS invalidated_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'transactions_chain_id_block_number_fkey'
          AND conrelid = 'transactions'::regclass AND confdeltype = 'c'
    ) THEN
        ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_chain_id_block_number_fkey;
        ALTER TABLE transactions ADD CONSTRAINT transactions_chain_id_block_number_fkey
            FOREIGN KEY (chain_id, block_number) REFERENCES blocks (chain_id, number)
            ON DELETE CASCADE;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'transaction_receipts_chain_id_tx_hash_fkey'
          AND conrelid = 'transaction_receipts'::regclass AND confdeltype = 'c'
    ) THEN
        ALTER TABLE transaction_receipts DROP CONSTRAINT IF EXISTS transaction_receipts_chain_id_tx_hash_fkey;
        ALTER TABLE transaction_receipts ADD CONSTRAINT transaction_receipts_chain_id_tx_hash_fkey
            FOREIGN KEY (chain_id, tx_hash) REFERENCES transactions (chain_id, tx_hash)
            ON DELETE CASCADE;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'transaction_receipts_chain_id_block_number_fkey'
          AND conrelid = 'transaction_receipts'::regclass AND confdeltype = 'c'
    ) THEN
        ALTER TABLE transaction_receipts DROP CONSTRAINT IF EXISTS transaction_receipts_chain_id_block_number_fkey;
        ALTER TABLE transaction_receipts ADD CONSTRAINT transaction_receipts_chain_id_block_number_fkey
            FOREIGN KEY (chain_id, block_number) REFERENCES blocks (chain_id, number)
            ON DELETE CASCADE;
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'token_transfers_chain_id_tx_hash_fkey'
          AND conrelid = 'token_transfers'::regclass AND confdeltype = 'c'
    ) THEN
        ALTER TABLE token_transfers DROP CONSTRAINT IF EXISTS token_transfers_chain_id_tx_hash_fkey;
        ALTER TABLE token_transfers ADD CONSTRAINT token_transfers_chain_id_tx_hash_fkey
            FOREIGN KEY (chain_id, tx_hash) REFERENCES transaction_receipts (chain_id, tx_hash)
            ON DELETE CASCADE;
    END IF;
END $$;
