CREATE TABLE outbox (
    id              UUID PRIMARY KEY,
    capture_id      UUID NOT NULL UNIQUE REFERENCES capture (id),
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ DEFAULT now(),
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ,

    CONSTRAINT outbox_event_type_check
        CHECK (event_type = 'capture.accepted'),

    CONSTRAINT outbox_payload_object_check
        CHECK (jsonb_typeof(payload) = 'object'),

    CONSTRAINT outbox_state_check
        CHECK (
            (status = 'pending'
                AND attempt_count = 0
                AND next_attempt_at IS NOT NULL
                AND last_error IS NULL
                AND delivered_at IS NULL)
            OR
            (status = 'retry'
                AND attempt_count >= 1
                AND next_attempt_at IS NOT NULL
                AND last_error IS NOT NULL
                AND delivered_at IS NULL)
            OR
            (status = 'delivered'
                AND attempt_count >= 1
                AND next_attempt_at IS NULL
                AND last_error IS NULL
                AND delivered_at IS NOT NULL)
            OR
            (status = 'dead'
                AND attempt_count >= 1
                AND next_attempt_at IS NULL
                AND last_error IS NOT NULL
                AND delivered_at IS NULL)
        )
);

CREATE INDEX outbox_due_work_idx
    ON outbox (next_attempt_at, created_at)
    WHERE status IN ('pending', 'retry');

---- create above / drop below ----

DROP TABLE outbox;
