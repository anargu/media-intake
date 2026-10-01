package capture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dbtx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db dbtx
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("database connection pool is nil")
	}
	return &PostgresStore{db: pool}, nil
}

func (s *PostgresStore) Insert(ctx context.Context, capture Capture) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO capture (
			id, idempotency_key, captured_at, amount,
			currency, frame_path, frame_size, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		capture.ID,
		capture.IdempotencyKey,
		capture.CapturedAt,
		capture.Amount,
		capture.Currency,
		capture.FramePath,
		capture.FrameSize,
		capture.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert capture: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (Capture, error) {
	var result Capture
	var capturedAt, createdAt time.Time
	err := s.db.QueryRow(ctx, `
		SELECT id, idempotency_key, captured_at, amount::text,
		       currency, frame_path, frame_size, created_at
		FROM capture
		WHERE idempotency_key = $1`, idempotencyKey).Scan(
		&result.ID,
		&result.IdempotencyKey,
		&capturedAt,
		&result.Amount,
		&result.Currency,
		&result.FramePath,
		&result.FrameSize,
		&createdAt,
	)
	if err != nil {
		return Capture{}, fmt.Errorf("get capture by idempotency key: %w", err)
	}

	result.CapturedAt = capturedAt.UTC().Format(time.RFC3339Nano)
	result.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	return result, nil
}
