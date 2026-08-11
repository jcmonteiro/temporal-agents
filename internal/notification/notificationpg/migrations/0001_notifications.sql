CREATE TABLE IF NOT EXISTS notifications (
    id text PRIMARY KEY,
    kind text NOT NULL,
    recipient text NOT NULL DEFAULT '',
    title text NOT NULL,
    body text NOT NULL DEFAULT '',
    url text NOT NULL DEFAULT '',
    session_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS notifications_recipient_created_idx
    ON notifications (recipient, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id text NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    principal text NOT NULL,
    read_at timestamptz NOT NULL,
    PRIMARY KEY (notification_id, principal)
);
