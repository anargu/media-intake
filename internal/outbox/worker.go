package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/anargu/media-intake/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deliverer interface {
	Deliver(context.Context, Outbox, func() (io.ReadCloser, error)) DeliveryResult
}

type Worker struct {
	db       *pgxpool.Pool
	delivery Deliverer
	root     *os.Root
	cfg      config.Config
	logger   *slog.Logger
	now      func() time.Time
	randomN  func(int64) int64
	wait     func(context.Context, time.Duration) error
}

func NewWorker(db *pgxpool.Pool, delivery Deliverer, root *os.Root, cfg config.Config, logger *slog.Logger, now func() time.Time, randomN func(int64) int64, wait func(context.Context, time.Duration) error) (*Worker, error) {
	if db == nil || delivery == nil || root == nil || logger == nil || now == nil || randomN == nil {
		return nil, fmt.Errorf("missing worker dependency")
	}
	if cfg.OutboxPollInterval <= 0 || cfg.DownstreamTimeout <= 0 ||
		cfg.OutboxOperationTimeout <= cfg.DownstreamTimeout || cfg.OutboxBaseDelay <= 0 ||
		cfg.OutboxMaxDelay < cfg.OutboxBaseDelay || cfg.OutboxMaxAttempts < 1 {
		return nil, fmt.Errorf("invalid worker configuration")
	}
	if wait == nil {
		wait = waitForPoll
	}

	return &Worker{db: db,
		delivery: delivery, root: root, cfg: cfg,
		logger: logger, now: now,
		randomN: randomN, wait: wait}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.RunUntil(ctx, ctx); err != nil {
		return err
	}
	return ctx.Err()
}

func (w *Worker) RunUntil(stop, work context.Context) error {
	for {
		if stop.Err() != nil {
			return nil
		}
		if work.Err() != nil {
			return work.Err()
		}
		found, err := w.ProcessOne(work)
		if stop.Err() != nil {
			return nil
		}
		if work.Err() != nil {
			return work.Err()
		}
		if err != nil {
			w.logger.Error("outbox operation failed")
		}
		if !found || err != nil {
			if err := w.wait(stop, w.cfg.OutboxPollInterval); err != nil {
				if stop.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func waitForPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ProcessOne holds the row lock from claim through delivery and commit.
// found means an event was claimed, not necessarily that its result committed.
func (w *Worker) ProcessOne(parent context.Context) (found bool, retErr error) {
	ctx, cancel := context.WithTimeout(parent, w.cfg.OutboxOperationTimeout)
	defer cancel()

	tx, err := w.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, fmt.Errorf("begin outbox transaction: %w", err)
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			retErr = errors.Join(retErr, err)
		}
	}()

	var event Outbox
	var framePath string
	err = tx.QueryRow(ctx, `SELECT o.id, o.capture_id, o.payload, o.attempt_count, c.frame_path
 FROM outbox o JOIN capture c ON c.id=o.capture_id
 WHERE o.status IN ('pending','retry') AND o.next_attempt_at <= $1
 ORDER BY o.next_attempt_at, o.created_at, o.id
 LIMIT 1 FOR UPDATE OF o SKIP LOCKED`, w.now()).
		Scan(&event.ID, &event.CaptureID, &event.Payload, &event.AttemptCount, &framePath)

	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim outbox event: %w", err)
	}

	deliveryCtx, cancelDelivery := context.WithTimeout(ctx, w.cfg.DownstreamTimeout)
	result := w.delivery.Deliver(deliveryCtx, event, func() (io.ReadCloser, error) { return w.root.Open(framePath) })
	cancelDelivery()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err := w.recordResult(ctx, tx, event, result, event.AttemptCount+1); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit outbox result: %w", err)
	}
	return true, nil
}

func (w *Worker) recordResult(ctx context.Context, tx pgx.Tx, event Outbox, result DeliveryResult, attempts int) error {
	status := "dead"
	var nextAttemptAt, deliveredAt *time.Time
	var diagnostic *string

	switch result.Kind {
	case Delivered:
		status = "delivered"
		completedAt := w.now()
		deliveredAt = &completedAt

	case TransientFailure, PermanentFailure:
		message := result.Diagnostic
		diagnostic = &message
		if result.ShouldRetry(attempts, w.cfg.OutboxMaxAttempts) {
			delay, err := RetryDelay(attempts, w.cfg.OutboxBaseDelay, w.cfg.OutboxMaxDelay, w.randomN)
			if err != nil {
				return err
			}
			status = "retry"
			nextAt := w.now().Add(delay)
			nextAttemptAt = &nextAt
		}
	default:
		return fmt.Errorf("invalid delivery classification")
	}

	tag, err := tx.Exec(ctx, `UPDATE outbox SET status=$2, attempt_count=$3,
 next_attempt_at=$4, last_error=$5, delivered_at=$6 WHERE id=$1`,
		event.ID, status, attempts, nextAttemptAt, diagnostic, deliveredAt)
	if err != nil {
		return fmt.Errorf("record outbox result: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("claimed outbox event was not updated")
	}
	return nil
}
