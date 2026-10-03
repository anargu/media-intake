package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/anargu/media-intake/internal/outbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CaptureService struct {
	db *pgxpool.Pool
}

func NewCaptureService(db *pgxpool.Pool) *CaptureService {
	return &CaptureService{db: db}
}

func (c *CaptureService) CreateCapture(ctx context.Context, input CreateCaptureInput) (result *CaptureResult, retErr error) {
	defer func() {
		if input.Frame != nil {
			retErr = errors.Join(retErr, input.Frame.Discard())
		}
	}()

	tx, err := c.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("Failed starting transaction: %w", err)
	}
	defer func() {
		tx.Rollback(ctx)
	}()

	outboxStore, err := outbox.NewPostgresStore(tx)
	if err != nil {
		return nil, fmt.Errorf("Failed creating outbox store: %w", err)
	}

	store, err := NewPostgresStore(tx)
	if err != nil {
		return nil, fmt.Errorf("Failed creating store: %w", err)
	}

	captureID := uuid.New()

	isCommitted := false
	var publicationResult PublicationResult

	defer func() {
		if !isCommitted && publicationResult.State == Published {
			framePath := filepath.Join(input.Frame.storage.rootDir, publicationResult.RelativePath)

			err := input.Frame.storage.fileOps.remove(framePath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				retErr = errors.Join(retErr, err)
			}
		}
	}()

	publicationResult, err = input.Frame.Publish(captureID)
	if err != nil {
		return nil, fmt.Errorf("Failed publishing frame: %w", err)
	}

	capture := Capture{
		ID:             captureID,
		IdempotencyKey: input.IdempotencyKey,
		CapturedAt:     input.Manifest.CapturedAt,
		Amount:         input.Manifest.Amount.StringFixedBank(2),
		Currency:       input.Manifest.Currency,
		FramePath:      publicationResult.RelativePath,
		FrameSize:      input.Frame.size,
		CreatedAt:      time.Now(),
	}

	err = store.Insert(ctx, capture)
	if err != nil {
		isCommitted = false
		return nil, fmt.Errorf("Failed Inserting capture: %w", err)
	}

	payload, err := json.Marshal(outbox.CaptureAcceptedPayload{
		Version: 1,
		Capture: outbox.CaptureMetadata{
			ID:         capture.ID.String(),
			CapturedAt: capture.CapturedAt,
			Amount:     capture.Amount,
			Currency:   capture.Currency,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal capture event payload: %w", err)
	}

	event := &outbox.Outbox{
		ID:        uuid.New(),
		CaptureID: capture.ID,
		Payload:   json.RawMessage(payload),
	}

	err = outboxStore.Insert(ctx, *event)
	if err != nil {
		isCommitted = false
		return nil, fmt.Errorf("Failed Inserting outbox: %w", err)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed on commit tx: %w", err)
	}
	isCommitted = true

	return &CaptureResult{
		Duplicate: false,
		Capture: CaptureResponse{
			ID:         capture.ID.String(),
			CapturedAt: capture.CapturedAt,
			Amount:     input.Manifest.Amount,
			Currency:   capture.Currency,
		},
	}, nil
}
