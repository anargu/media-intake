package capture

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

func TestCaptureStoreRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL capture store tests")
	}

	ctx := context.Background()
	testConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer testConn.Close(ctx)

	store := &PostgresStore{db: testConn}

	_, err = testConn.Exec(ctx, `CREATE TEMP TABLE capture (
		id uuid PRIMARY KEY,
		idempotency_key text UNIQUE NOT NULL,
		captured_at timestamptz NOT NULL,
		amount numeric(20,2) NOT NULL,
		currency text NOT NULL,
		frame_path text NOT NULL,
		frame_size bigint NOT NULL,
		created_at timestamptz NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create temporary capture table: %v", err)
	}

	capture := Capture{
		ID:             uuid.UUID{},
		IdempotencyKey: "capture-key",
		CapturedAt:     time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Amount:         "9007199254740993.01",
		Currency:       "USD",
		FramePath:      "00/00000000000000000000000000000000.frame",
		FrameSize:      1234,
		CreatedAt:      time.Date(2026, 9, 30, 12, 1, 0, 0, time.UTC),
	}
	if inserted, err := store.Insert(ctx, capture); err != nil || !inserted {
		t.Fatalf("insert capture: %v", err)
	}

	got, err := store.GetByIdempotencyKey(ctx, capture.IdempotencyKey)
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if got.ID != capture.ID ||
		got.IdempotencyKey != capture.IdempotencyKey ||
		!got.CapturedAt.Equal(capture.CapturedAt) ||
		got.Amount != capture.Amount ||
		got.Currency != capture.Currency ||
		got.FramePath != capture.FramePath ||
		got.FrameSize != capture.FrameSize ||
		!got.CreatedAt.Equal(capture.CreatedAt) {
		t.Errorf("getCapture() = %#v, want matching capture fields", got)
	}
}

func TestGetCaptureNotFound(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL capture store tests")
	}

	ctx := context.Background()
	testConn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer testConn.Close(ctx)

	store := &PostgresStore{db: testConn}

	_, err = testConn.Exec(ctx, `CREATE TEMP TABLE capture (
		id uuid PRIMARY KEY,
		idempotency_key text UNIQUE NOT NULL,
		captured_at timestamptz NOT NULL,
		amount numeric(20,2) NOT NULL,
		currency text NOT NULL,
		frame_path text NOT NULL,
		frame_size bigint NOT NULL,
		created_at timestamptz NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create temporary capture table: %v", err)
	}

	if _, err := store.GetByIdempotencyKey(ctx, "missing"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetByIdempotencyKey() error = %v, want %v", err, pgx.ErrNoRows)
	}
}
