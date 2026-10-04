package outbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestWorkerCancellationAndConnectionLossAllowStableReplay(t *testing.T) {
	for _, scenario := range []string{"cancellation", "connection-loss"} {
		t.Run(scenario, func(t *testing.T) {
			f := newWorkerFixture(t)
			event := f.seed(t, "pending", f.now)
			entered := make(chan string, 2)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				entered <- r.Header.Get("Idempotency-Key")
				select {
				case <-release:
				case <-r.Context().Done():
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			defer unblock()
			client, err := NewDownstreamClient(server.URL, time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			worker := f.worker(t, client)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := worker.ProcessOne(ctx); done <- err }()
			select {
			case key := <-entered:
				if key != event.ID.String() {
					t.Fatal("incorrect delivery key")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("delivery did not start")
			}
			if scenario == "cancellation" {
				cancel()
			} else {
				// Kill the backend while the downstream request is in flight. Its
				// transaction cannot record success even though downstream accepts it.
				var terminated bool
				if err := f.pool.QueryRow(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_locks
    WHERE relation=$1::regclass AND mode='RowShareLock' AND granted LIMIT 1`, f.schema+".outbox").Scan(&terminated); err != nil || !terminated {
					t.Fatalf("terminate=%t err=%v", terminated, err)
				}
			}
			unblock()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("transaction loss returned success")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not finish")
			}
			f.assertState(t, event, "pending", 0)
			// A new worker resumes the same persisted event with its original key.
			resumed := f.worker(t, client)
			process(t, resumed, true)
			if key := <-entered; key != event.ID.String() {
				t.Fatalf("replay key=%s", key)
			}
			f.assertState(t, event, "delivered", 1)
			process(t, resumed, false)
		})
	}
}
