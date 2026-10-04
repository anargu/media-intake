package capture

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type dbtx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db dbtx
}

func NewPostgresStore(tx dbtx) (*PostgresStore, error) {
	if tx == nil {
		return nil, errors.New("database connection pool is nil")
	}
	return &PostgresStore{db: tx}, nil
}

func (s *PostgresStore) Insert(ctx context.Context, capture Capture) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`INSERT INTO capture (
			id, idempotency_key, captured_at, amount,
			currency, frame_path, frame_size, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (idempotency_key) DO NOTHING`,
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
		return false, fmt.Errorf("insert capture: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PostgresStore) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (Capture, error) {
	var result Capture

	err := s.db.QueryRow(ctx, `
		SELECT id, idempotency_key, captured_at, amount::text,
		       currency, frame_path, frame_size, created_at
		FROM capture
		WHERE idempotency_key = $1`, idempotencyKey).Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.CapturedAt,
		&result.Amount,
		&result.Currency,
		&result.FramePath,
		&result.FrameSize,
		&result.CreatedAt,
	)
	if err != nil {
		return Capture{}, fmt.Errorf("get capture by idempotency key: %w", err)
	}

	return result, nil
}
