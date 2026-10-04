package outbox

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestWorkerStopsPollsAndDrainsCurrentDelivery(t *testing.T) {
	f := newWorkerFixture(t)
	first := f.seed(t, "pending", f.now.Add(-time.Second))
	second := f.seed(t, "pending", f.now)
	entered := make(chan struct{})
	release := make(chan struct{})
	w := f.worker(t, deliveryFunc(func(ctx context.Context, event Outbox, _ func() (io.ReadCloser, error)) DeliveryResult {
		if event.ID != first.ID {
			t.Error("worker claimed another event during drain")
		}
		close(entered)
		select {
		case <-release:
			if ctx.Err() != nil {
				t.Error("current delivery canceled during graceful drain")
			}
			return DeliveryResult{Kind: Delivered}
		case <-ctx.Done():
			return DeliveryResult{Kind: TransientFailure, Diagnostic: "canceled"}
		}
	}))
	claims, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- w.RunUntil(claims, context.Background()) }()
	<-entered
	stop()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish its current delivery")
	}
	f.assertState(t, first, "delivered", 1)
	f.assertState(t, second, "pending", 0)
}
