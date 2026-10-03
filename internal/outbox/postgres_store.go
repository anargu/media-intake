package outbox

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

func NewPostgresStore(db dbtx) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("database connection pool is nil")
	}
	return &PostgresStore{db: db}, nil
}

// Insert stores a new accepted-capture event. The database defaults initialize
// it as pending with zero attempts and a due time of now.
func (s *PostgresStore) Insert(ctx context.Context, event Outbox) error {
	_, err := s.db.Exec(ctx, `
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
