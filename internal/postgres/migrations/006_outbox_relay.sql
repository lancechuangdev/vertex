ALTER TABLE outbox_messages
    ADD COLUMN IF NOT EXISTS traceparent TEXT,
    ADD COLUMN IF NOT EXISTS tracestate TEXT,
    ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS locked_by TEXT,
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error TEXT;

DROP INDEX IF EXISTS outbox_messages_unpublished_idx;
CREATE INDEX IF NOT EXISTS outbox_messages_relay_idx
    ON outbox_messages (available_at, id)
    WHERE published_at IS NULL AND invalidated_at IS NULL;
