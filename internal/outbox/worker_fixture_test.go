package outbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/config"
	"github.com/anargu/media-intake/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type deliveryFunc func(context.Context, Outbox, func() (io.ReadCloser, error)) DeliveryResult

func (f deliveryFunc) Deliver(ctx context.Context, event Outbox, open func() (io.ReadCloser, error)) DeliveryResult {
	return f(ctx, event, open)
}

type workerFixture struct {
	pool        *pgxpool.Pool
	root        *os.Root
	dir, schema string
	now         time.Time
	cfg         config.Config
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL worker tests")
	}
	ctx := context.Background()
	admin, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if err := database.Migrate(ctx, admin); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("worker_test_%d", time.Now().UnixNano())
	name := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE") })
	// Clone the real migrated checks and indexes, isolating work from other tests.
	for _, table := range []string{"capture", "outbox"} {
		if _, err := admin.Exec(ctx, "CREATE TABLE "+name+"."+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return &workerFixture{pool: pool, root: root, dir: dir, schema: schema, now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), cfg: config.Config{
		DownstreamTimeout: time.Second, OutboxOperationTimeout: 3 * time.Second, OutboxPollInterval: time.Second,
		OutboxBaseDelay: time.Second, OutboxMaxDelay: time.Minute, OutboxMaxAttempts: 3,
	}}
}
func (f *workerFixture) worker(t *testing.T, delivery Deliverer) *Worker {
	t.Helper()
	w, err := NewWorker(f.pool, delivery, f.root, f.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return f.now }, func(int64) int64 { return 0 }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func (f *workerFixture) seed(t *testing.T, status string, due time.Time) Outbox {
	t.Helper()
	ctx := context.Background()
	event := testDeliveryEvent(t)
	path := event.CaptureID.String() + ".frame"
	if err := os.WriteFile(filepath.Join(f.dir, path), []byte("worker frame"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO capture(id,idempotency_key,captured_at,amount,currency,frame_path,frame_size)
 VALUES($1,$2,$3,'12.34','PEN',$4,12)`, event.CaptureID, event.ID.String(), f.now, path); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	var next, delivered *time.Time
	var diagnostic *string
	switch status {
	case "pending":
		next = &due
	case "retry":
		attempts = 1
		next = &due
		message := "transient"
		diagnostic = &message
	case "dead":
		attempts = 3
		message := "exhausted"
		diagnostic = &message
	case "delivered":
		attempts = 1
		delivered = &f.now
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO outbox(id,capture_id,event_type,payload,status,attempt_count,next_attempt_at,last_error,created_at,delivered_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, event.ID, event.CaptureID, EventTypeCaptureAccepted, event.Payload, status, attempts, next, diagnostic, f.now, delivered); err != nil {
		t.Fatal(err)
	}
	return event
}
func (f *workerFixture) assertState(t *testing.T, event Outbox, want string, attempts int) {
	t.Helper()
	var status string
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT status,attempt_count FROM outbox WHERE id=$1", event.ID).Scan(&status, &count); err != nil {
		t.Fatal(err)
	}
	if status != want || count != attempts {
		t.Fatalf("state=%s attempts=%d want=%s/%d", status, count, want, attempts)
	}
}
func process(t *testing.T, w *Worker, wantFound bool) {
	t.Helper()
	found, err := w.ProcessOne(context.Background())
	if err != nil || found != wantFound {
		t.Fatalf("found=%t err=%v", found, err)
	}
}
