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

func TestWorkerDueOrderAndCompletedExclusion(t *testing.T) {
	f := newWorkerFixture(t)
	later := f.seed(t, "pending", f.now.Add(-time.Second))
	earlier := f.seed(t, "retry", f.now.Add(-2*time.Second))
	f.seed(t, "pending", f.now.Add(time.Second))
	f.seed(t, "dead", f.now)
	f.seed(t, "delivered", f.now)
	var ids []string
	w := f.worker(t, deliveryFunc(func(ctx context.Context, event Outbox, open func() (io.ReadCloser, error)) DeliveryResult {
		file, err := open()
		if err != nil {
			t.Error(err)
			return DeliveryResult{Kind: TransientFailure}
		}
		defer file.Close()
		frame, err := io.ReadAll(file)
		if err != nil || string(frame) != "worker frame" {
			t.Errorf("frame=%q err=%v", frame, err)
		}
		ids = append(ids, event.ID.String())
		return DeliveryResult{Kind: Delivered}
	}))
	process(t, w, true)
	process(t, w, true)
	process(t, w, false)
	if len(ids) != 2 || ids[0] != earlier.ID.String() || ids[1] != later.ID.String() {
		t.Fatalf("order=%v", ids)
	}
	f.assertState(t, earlier, "delivered", 2)
	f.assertState(t, later, "delivered", 1)
}

func TestWorkersSkipLockedEventAndProgressDistinctRows(t *testing.T) {
	f := newWorkerFixture(t)
	first := f.seed(t, "pending", f.now.Add(-time.Second))
	entered := make(chan string, 3)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		key := r.Header.Get("Idempotency-Key")
		entered <- key
		if key == first.ID.String() {
			<-release
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	defer unblock()
	client, err := NewDownstreamClient(server.URL, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	workerA := f.worker(t, client)
	workerB := f.worker(t, client)
	done := make(chan error, 1)
	go func() { _, err := workerA.ProcessOne(context.Background()); done <- err }()
	select {
	case key := <-entered:
		if key != first.ID.String() {
			t.Fatal("unexpected first event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery did not start")
	}
	// No other event exists: B must return without a second HTTP call.
	process(t, workerB, false)
	select {
	case key := <-entered:
		t.Fatalf("concurrent duplicate: %s", key)
	default:
	}
	second := f.seed(t, "pending", f.now)
	process(t, workerB, true)
	if key := <-entered; key != second.ID.String() {
		t.Fatalf("second call=%s", key)
	}
	f.assertState(t, first, "pending", 0)
	f.assertState(t, second, "delivered", 1)
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first worker did not finish")
	}
	f.assertState(t, first, "delivered", 1)
	process(t, workerB, false)
}
