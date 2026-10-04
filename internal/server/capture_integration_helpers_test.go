package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/capture"
	"github.com/anargu/media-intake/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

const integrationFrameBytes = "integration frame bytes"

func newCaptureIntegrationHandler(t *testing.T) (*pgxpool.Pool, http.Handler, string) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL capture endpoint tests")
	}

	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	storageRoot := t.TempDir()
	storage, err := capture.NewFileSystemStorage(storageRoot, 10*1024*1024)
	if err != nil {
		t.Fatalf("create test frame storage: %v", err)
	}
	handler := New(
		testLogger(),
		CaptureLimits{
			MaxBodyBytes:     11 * 1024 * 1024,
			MaxManifestBytes: 64 * 1024,
			RequestTimeout:   30 * time.Second,
		},
		pool,
		storage,
		capture.NewCaptureService(pool),
	)
	return pool, handler, storageRoot
}

func newCapturePostRequest(t *testing.T, idempotencyKey, amount string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	multipartWriter := multipart.NewWriter(body)
	manifest := fmt.Sprintf(
		`{"capturedAt":"2026-10-03T12:00:00Z","amount":%q,"currency":"PEN"}`,
		amount,
	)
	if err := multipartWriter.WriteField("manifest", manifest); err != nil {
		t.Fatalf("write manifest part: %v", err)
	}

	frameHeader := make(textproto.MIMEHeader)
	frameHeader.Set("Content-Disposition", `form-data; name="frame"; filename="frame.jpg"`)
	frameHeader.Set("Content-Type", "image/jpeg")
	framePart, err := multipartWriter.CreatePart(frameHeader)
	if err != nil {
		t.Fatalf("create frame part: %v", err)
	}
	if _, err := io.WriteString(framePart, integrationFrameBytes); err != nil {
		t.Fatalf("write frame part: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/captures", body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Idempotency-Key", idempotencyKey)
	return request
}

func cleanupCaptureByKey(t *testing.T, pool *pgxpool.Pool, idempotencyKey string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		DELETE FROM outbox
		WHERE capture_id IN (SELECT id FROM capture WHERE idempotency_key = $1)`, idempotencyKey); err != nil {
		t.Errorf("delete test outbox rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM capture WHERE idempotency_key = $1`, idempotencyKey); err != nil {
		t.Errorf("delete test capture row: %v", err)
	}
}

func integrationKey(prefix string) string {
	return fmt.Sprintf("issue08-%s-%d", prefix, time.Now().UnixNano())
}
