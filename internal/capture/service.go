package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/anargu/media-intake/internal/outbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var ErrCommitOutcomeUnknown = errors.New("capture commit outcome unknown")

type transactionOutcome uint8

const (
	transactionPending transactionOutcome = iota
	transactionRolledBack
	transactionCommitted
	transactionUnknown
)

type CaptureService struct {
	db     *pgxpool.Pool
	commit func(context.Context, pgx.Tx) error
}

func NewCaptureService(db *pgxpool.Pool) *CaptureService {
	return &CaptureService{
		db: db,
		commit: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
	}
}

func (c *CaptureService) CreateCapture(ctx context.Context, input CreateCaptureInput) (result *CaptureResult, retErr error) {
	var tx pgx.Tx
	outcome := transactionPending
	var publicationResult PublicationResult

	defer func() {
		retErr = cleanupCreateCapture(tx,
			outcome,
			publicationResult,
			input.Frame,
			result != nil,
			retErr)
	}()

	var err error
	tx, err = c.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("start capture transaction: %w", err)
	}

	outboxStore, err := outbox.NewPostgresStore(tx)
	if err != nil {
		return nil, fmt.Errorf("failed creating outbox store: %w", err)
	}

	store, err := NewPostgresStore(tx)
	if err != nil {
		return nil, fmt.Errorf("failed creating store: %w", err)
	}

	captureID := uuid.New()

	framePath, err := finalFrameRelativePath(captureID)
	if err != nil {
		return nil, err
	}

	capture := Capture{
		ID:             captureID,
		IdempotencyKey: input.IdempotencyKey,
		CapturedAt:     input.Manifest.CapturedAt,
		Amount:         input.Manifest.Amount.StringFixedBank(2),
		Currency:       input.Manifest.Currency,
		FramePath:      framePath,
		FrameSize:      input.Frame.size,
		CreatedAt:      time.Now(),
	}

	alreadyInserted, err := store.Insert(ctx, capture)
	if err != nil {
		return nil, fmt.Errorf("insert capture: %w", err)
	}

	stored, err := store.GetByIdempotencyKey(ctx, input.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("read accepted capture: %w", err)
	}
	response, err := responseFromCapture(stored)
	if err != nil {
		return nil, err
	}

	if !alreadyInserted {
		return &CaptureResult{Duplicate: true, Capture: response}, nil
	}
	capture = stored

	publicationResult, err = input.Frame.Publish(captureID)
	if err != nil {
		return nil, fmt.Errorf("publish frame: %w", err)
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
		return nil, fmt.Errorf("Failed Inserting outbox: %w", err)
	}

	// outcome flag to validate when deferring/cleanup
	outcome = transactionUnknown
	err = c.commit(ctx, tx)
	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			outcome = transactionRolledBack
			return nil, fmt.Errorf("capture commit rolled back: %w", err)
		}

		slog.Warn("capture commit outcome unknown; preserving frame",
			"capture_id", capture.ID.String(),
			"frame_path", publicationResult.RelativePath,
			"error", err)
		return nil, errors.Join(ErrCommitOutcomeUnknown, err)
	}

	outcome = transactionCommitted

	return &CaptureResult{Duplicate: false, Capture: response}, nil
}

func cleanupCreateCapture(tx pgx.Tx, outcome transactionOutcome, publication PublicationResult, frame *StagedFrame, accepted bool, resultErr error) error {
	if tx != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rollbackErr := tx.Rollback(cleanupCtx)

		cancel()
		if outcome == transactionPending {
			if rollbackErr == nil {
				outcome = transactionRolledBack
			} else {
				outcome = transactionUnknown
				resultErr = errors.Join(resultErr, rollbackErr)
			}
		}
	}

	if publication.State == Published && outcome == transactionRolledBack {
		path := filepath.Join(frame.storage.rootDir, publication.RelativePath)

		err := frame.storage.fileOps.remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("failed to remove rolled-back frame",
				"frame_path", publication.RelativePath,
				"error", err)

			resultErr = errors.Join(resultErr, err)
		}
	}

	if frame != nil {
		if err := frame.Discard(); err != nil {
			slog.Warn("failed to discard staged frame", "error", err)

			if !accepted {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}
	return resultErr
}

func (c *CaptureService) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*CaptureResponse, error) {
	store, err := NewPostgresStore(c.db)
	if err != nil {
		return nil, fmt.Errorf("Failed creating store: %w", err)
	}

	capture, err := store.GetByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("Failed getting capture by Key: %w", err)
	}

	response, err := responseFromCapture(capture)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

// responseFromCapture Maps CaptureResponse
func responseFromCapture(capture Capture) (CaptureResponse, error) {
	amount, err := decimal.NewFromString(capture.Amount)
	if err != nil {
		return CaptureResponse{}, fmt.Errorf("parse stored amount: %w", err)
	}
	return CaptureResponse{
		ID:         capture.ID.String(),
		Amount:     amount,
		CapturedAt: capture.CapturedAt,
		Currency:   capture.Currency,
	}, nil
}
