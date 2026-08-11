-- Small durable notifications for resumable hub event streams. These rows carry
-- identities only; clients refetch list and session resources for current data.
CREATE TABLE IF NOT EXISTS steering_events (
    sequence   bigserial   PRIMARY KEY,
    event_type text        NOT NULL,
    session_id text        NOT NULL,
    item_id    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
