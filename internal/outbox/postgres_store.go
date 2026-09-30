package outbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("database connection pool is nil")
	}
	return &PostgresStore{pool: pool}, nil
}

// Insert stores a new accepted-capture event. The database defaults initialize
// it as pending with zero attempts and a due time of now.
func (s *PostgresStore) Insert(ctx context.Context, event Outbox) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outbox (id, capture_id, event_type, payload)
		VALUES ($1, $2, $3, $4)`,
		event.ID,
		event.CaptureID,
		EventTypeCaptureAccepted,
		event.Payload,
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}
