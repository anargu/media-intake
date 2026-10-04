package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type captureFailureFixture struct {
	pool     *pgxpool.Pool
	service  *CaptureService
	storage  *FileSystemStorage
	input    CreateCaptureInput
	injected error
}

func newCaptureFailureFixture(t *testing.T) *captureFailureFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL failure tests")
	}
	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}

	key := "issue10-v2-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000000000")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM outbox WHERE capture_id IN (SELECT id FROM capture WHERE idempotency_key=$1)`, key); err != nil {
			t.Errorf("delete outbox: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM capture WHERE idempotency_key=$1`, key); err != nil {
			t.Errorf("delete capture: %v", err)
		}
	})

	storage, staged := stageFrameForPublish(t, []byte("frame"))
	return &captureFailureFixture{
		pool: pool, service: NewCaptureService(pool), storage: storage,
		input: CreateCaptureInput{IdempotencyKey: key, Frame: staged, Manifest: Manifest{
			CapturedAt: time.Now().UTC(), Amount: decimal.RequireFromString("12.34"), Currency: "PEN",
		}},
		injected: errors.New("injected failure"),
	}
}

func TestCaptureFailureOutcomesV2(t *testing.T) {
	type expectation struct {
		captures, outbox, finalFrames, stagedFiles int
		accepted, unknownCommit                    bool
	}
	cases := []struct {
		name   string
		inject func(*testing.T, *captureFailureFixture, context.Context) context.Context
		want   expectation
	}{
		{
			name: "capture insert fails",
			inject: func(_ *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.input.Manifest.Amount = decimal.RequireFromString("-1.00")
				return ctx
			},
			want: expectation{},
		},
		{
			name: "frame link fails before publication",
			inject: func(_ *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.storage.fileOps.link = func(string, string) error { return f.injected }
				return ctx
			},
			want: expectation{},
		},
		{
			name: "directory sync fails after link",
			inject: func(_ *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.storage.fileOps.syncDirectory = func(string) error { return f.injected }
				return ctx
			},
			want: expectation{},
		},
		{
			name: "request canceled after publication",
			inject: func(t *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				canceledCtx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				f.storage.fileOps.syncDirectory = func(string) error { cancel(); return f.injected }
				return canceledCtx
			},
			want: expectation{},
		},
		{
			name: "commit confirms rollback",
			inject: func(t *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.service.commit = func(ctx context.Context, tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, "SELECT 1/0"); err == nil {
						t.Fatal("expected aborted transaction")
					}
					return tx.Commit(ctx)
				}
				return ctx
			},
			want: expectation{},
		},
		{
			name: "commit succeeds but acknowledgment is lost",
			inject: func(t *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.service.commit = func(ctx context.Context, tx pgx.Tx) error {
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					return f.injected
				}
				return ctx
			},
			want: expectation{captures: 1, outbox: 1, finalFrames: 1, unknownCommit: true},
		},
		{
			name: "commit fails with unknown outcome",
			inject: func(_ *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.service.commit = func(context.Context, pgx.Tx) error { return f.injected }
				return ctx
			},
			want: expectation{finalFrames: 1, unknownCommit: true},
		},
		{
			name: "staging cleanup fails after commit",
			inject: func(_ *testing.T, f *captureFailureFixture, ctx context.Context) context.Context {
				f.storage.fileOps.remove = func(path string) error {
					if strings.HasSuffix(path, ".part") {
						return f.injected
					}
					return os.Remove(path)
				}
				return ctx
			},
			want: expectation{captures: 1, outbox: 1, finalFrames: 1, stagedFiles: 1, accepted: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCaptureFailureFixture(t)
			requestCtx := tc.inject(t, f, context.Background())
			result, err := f.service.CreateCapture(requestCtx, f.input)

			if tc.want.accepted {
				if err != nil || result == nil || result.Duplicate {
					t.Fatalf("accepted capture: result=%v err=%v", result, err)
				}
			} else if err == nil || result != nil {
				t.Fatalf("failed capture: result=%v err=%v", result, err)
			}
			if tc.want.unknownCommit && !errors.Is(err, ErrCommitOutcomeUnknown) {
				t.Fatalf("error=%v, want unknown commit outcome", err)
			}
			assertCaptureFailureState(t, f, tc.want.captures, tc.want.outbox, tc.want.finalFrames, tc.want.stagedFiles)
		})
	}
}

func assertCaptureFailureState(t *testing.T, f *captureFailureFixture, wantCaptures, wantOutbox, wantFrames, wantStaged int) {
	t.Helper()
	ctx := context.Background()
	var captures, events int

	if err := f.pool.QueryRow(ctx, `SELECT count(*), (SELECT count(*) FROM outbox WHERE capture_id IN
		(SELECT id FROM capture WHERE idempotency_key=$1)) FROM capture WHERE idempotency_key=$1`, f.input.IdempotencyKey).Scan(&captures, &events); err != nil {
		t.Fatal(err)
	}
	if captures != wantCaptures || events != wantOutbox {
		t.Errorf("captures=%d outbox=%d; want %d and %d", captures, events, wantCaptures, wantOutbox)
	}

	frames, err := filepath.Glob(filepath.Join(f.storage.rootDir, "*.frame"))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != wantFrames {
		t.Errorf("final frames=%d, want %d", len(frames), wantFrames)
	}

	staged, err := os.ReadDir(filepath.Join(f.storage.rootDir, ".staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != wantStaged {
		t.Errorf("staged files=%d, want %d", len(staged), wantStaged)
	}
}

func TestCaptureRetryAfterLostCommitAcknowledgmentV2(t *testing.T) {
	f := newCaptureFailureFixture(t)
	f.service.commit = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return f.injected
	}

	result, err := f.service.CreateCapture(context.Background(), f.input)
	if result != nil || !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("first request: result=%v err=%v", result, err)
	}

	frames, err := filepath.Glob(filepath.Join(f.storage.rootDir, "*.frame"))
	if err != nil || len(frames) != 1 {
		t.Fatalf("committed frames=%v err=%v", frames, err)
	}

	retryFrame, err := f.storage.Stage(context.Background(), strings.NewReader("retry frame"))
	if err != nil {
		t.Fatal(err)
	}
	f.input.Frame = retryFrame

	duplicate, err := f.service.CreateCapture(context.Background(), f.input)
	if err != nil || duplicate == nil || !duplicate.Duplicate {
		t.Fatalf("retry: result=%v err=%v", duplicate, err)
	}

	frame, err := os.ReadFile(frames[0])
	if err != nil || string(frame) != "frame" {
		t.Fatalf("original frame=%q err=%v", frame, err)
	}
	assertCaptureFailureState(t, f, 1, 1, 1, 0)
}
