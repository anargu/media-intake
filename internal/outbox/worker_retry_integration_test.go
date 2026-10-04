package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestWorkerIdlePollingUsesInjectedWait(t *testing.T) {
	f := newWorkerFixture(t)
	called := 0
	w := f.worker(t, deliveryFunc(func(context.Context, Outbox, func() (io.ReadCloser, error)) DeliveryResult {
		called++
		return DeliveryResult{Kind: Delivered}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := 0
	w.wait = func(ctx context.Context, d time.Duration) error {
		waits++
		if d != f.cfg.OutboxPollInterval {
			t.Errorf("interval=%s", d)
		}
		cancel()
		return ctx.Err()
	}
	if err := w.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v", err)
	}
	if waits != 1 || called != 0 {
		t.Fatalf("waits=%d deliveries=%d", waits, called)
	}
}

func TestWorkerRetryAndTerminalResults(t *testing.T) {
	for _, kind := range []DeliveryKind{TransientFailure, PermanentFailure} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			f := newWorkerFixture(t)
			event := f.seed(t, "pending", f.now)
			w := f.worker(t, deliveryFunc(func(context.Context, Outbox, func() (io.ReadCloser, error)) DeliveryResult {
				return DeliveryResult{kind, "downstream HTTP 503"}
			}))
			process(t, w, true)
			if kind == PermanentFailure {
				f.assertState(t, event, "dead", 1)
				process(t, w, false)
				return
			}
			f.assertState(t, event, "retry", 1)
			var next time.Time
			if err := f.pool.QueryRow(context.Background(), "SELECT next_attempt_at FROM outbox WHERE id=$1", event.ID).Scan(&next); err != nil {
				t.Fatal(err)
			}
			if !next.Equal(f.now.Add(500 * time.Millisecond)) {
				t.Fatalf("next=%s", next)
			}
			process(t, w, false)
			f.now = next
			process(t, w, true)
			f.now = f.now.Add(time.Second)
			process(t, w, true)
			f.assertState(t, event, "dead", 3)
			process(t, w, false)
		})
	}
}
