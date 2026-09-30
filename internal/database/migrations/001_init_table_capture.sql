CREATE TABLE Capture (
    id              UUID PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    device_id       TEXT NOT NULL,
    captured_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    amount          NUMERIC(20, 2) NOT NULL,
    currency        TEXT NOT NULL,
    frame_path      TEXT NOT NULL UNIQUE,
    frame_size      BIGINT NOT NULL,

    CONSTRAINT capture_amount_check
        CHECK (amount >= 0 AND amount <> 'NaN'::numeric),

    CONSTRAINT capture_frame_size_check
        CHECK (frame_size BETWEEN 1 AND 10485760)
);

---- create above / drop below ----

DROP TABLE Capture;