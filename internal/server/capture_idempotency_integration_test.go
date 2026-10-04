package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/anargu/media-intake/internal/capture"
)

func TestConcurrentCaptureDuplicates(t *testing.T) {
	pool, handler, storageRoot := newCaptureIntegrationHandler(t)
	key := integrationKey("concurrent")

	t.Cleanup(func() { cleanupCaptureByKey(t, pool, key) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())

	// A SHARE lock holds both requests at INSERT until we release them together.
	if _, err := blocker.Exec(ctx, "LOCK TABLE capture IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}

	requests := []*http.Request{
		newCapturePostRequest(t, key, "12.34"),
		newCapturePostRequest(t, key, "98.76"),
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, request := range requests {
		go func(request *http.Request) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request.WithContext(ctx))
			responses <- response
		}(request)
	}

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	// Wait for both blocked INSERTs so this is a real race, not sequential requests.
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks
			WHERE relation = 'capture'::regclass AND mode = 'RowExclusiveLock' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("requests did not overlap at capture INSERT")
		case <-ticker.C:
		}
	}

	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var original json.RawMessage
	created, duplicates := 0, 0
	for range requests {
		var response *httptest.ResponseRecorder
		select {
		case response = <-responses:
		case <-ctx.Done():
			t.Fatal("requests did not finish")
		}
		var result struct {
			Duplicate bool            `json:"duplicate"`
			Capture   json.RawMessage `json:"capture"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}

		switch response.Code {
		case http.StatusCreated:
			created++
			if result.Duplicate {
				t.Fatal("winner marked duplicate")
			}
		case http.StatusOK:
			duplicates++
			if !result.Duplicate {
				t.Fatal("loser not marked duplicate")
			}
		default:
			t.Fatalf("status = %d: %s", response.Code, response.Body.String())
		}

		if original == nil {
			original = result.Capture
		} else if !bytes.Equal(original, result.Capture) {
			t.Fatalf("capture responses differ: %s and %s", original, result.Capture)
		}
	}
	if created != 1 || duplicates != 1 {
		t.Fatalf("created=%d duplicates=%d", created, duplicates)
	}

	// A later different body must still return the winner.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newCapturePostRequest(t, key, "55.55"))
	var repeated struct {
		Duplicate bool            `json:"duplicate"`
		Capture   json.RawMessage `json:"capture"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &repeated); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !repeated.Duplicate || !bytes.Equal(original, repeated.Capture) {
		t.Fatalf("repeated response = %d %s", response.Code, response.Body.String())
	}

	var captures, events int

	err = pool.QueryRow(ctx,
		`SELECT count(*), (SELECT count(*) FROM outbox WHERE capture_id IN
		(SELECT id FROM capture WHERE idempotency_key=$1)) FROM capture WHERE idempotency_key=$1`,
		key).Scan(&captures, &events)
	if err != nil {
		t.Fatal(err)
	}
	if captures != 1 || events != 1 {
		t.Fatalf("captures=%d events=%d", captures, events)
	}

	entries, err := os.ReadDir(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	frames := 0
	for _, entry := range entries {
		if entry.Name() != ".staging" {
			frames++
		}
	}
	if frames != 1 {
		t.Fatalf("final frames=%d", frames)
	}

	staged, err := os.ReadDir(filepath.Join(storageRoot, ".staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("staging files=%d", len(staged))
	}
}

func TestCaptureContenderWinsAfterFirstTransactionRollsBack(t *testing.T) {
	pool, handler, storageRoot := newCaptureIntegrationHandler(t)
	key := integrationKey("winner-rollback")

	t.Cleanup(func() { cleanupCaptureByKey(t, pool, key) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(context.Background())

	store, err := capture.NewPostgresStore(first)
	if err != nil {
		t.Fatal(err)
	}
	firstID := uuid.New()

	// Hold an uncommitted row with this key; the HTTP contender must await its outcome.
	inserted, err := store.Insert(ctx, capture.Capture{
		ID: firstID, IdempotencyKey: key,
		CapturedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		Amount:     "12.34", Currency: "PEN", FramePath: "rolled-back.frame",
		FrameSize: int64(len(integrationFrameBytes)), CreatedAt: time.Now(),
	})
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted=%t error=%v", inserted, err)
	}

	var firstPID int32
	if err := first.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&firstPID); err != nil {
		t.Fatal(err)
	}

	request := newCapturePostRequest(t, key, "98.76").WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		done <- response
	}()

	// Prove the HTTP contender is blocked by the uncommitted first insert.
	// This avoids turning rollback coverage into a sequential retry test.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE $1::integer = ANY(pg_blocking_pids(pid))
			AND query LIKE 'INSERT INTO capture%'
		)`, firstPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case response := <-done:
			t.Fatalf("contender finished before rollback: %d %s", response.Code, response.Body.String())
		case <-ctx.Done():
			t.Fatal("contender did not block on the first transaction")
		case <-ticker.C:
		}
	}

	// Rollback releases the key, allowing the waiting request to become the winner.
	if err := first.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var response *httptest.ResponseRecorder
	select {
	case response = <-done:
	case <-ctx.Done():
		t.Fatal("contender did not finish after rollback")
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var result struct {
		Duplicate bool `json:"duplicate"`
		Capture   struct {
			ID     string `json:"id"`
			Amount string `json:"amount"`
		} `json:"capture"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Duplicate || result.Capture.ID == firstID.String() || result.Capture.Amount != "98.76" {
		t.Fatalf("contender did not become winner: %s", response.Body.String())
	}

	committedStore, err := capture.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := committedStore.GetByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID.String() != result.Capture.ID || stored.Amount != "98.76" {
		t.Fatalf("stored capture does not match contender: %#v", stored)
	}

	var captures, events, rolledBack int
	if err := pool.QueryRow(ctx, `SELECT count(*),
		(SELECT count(*) FROM outbox WHERE capture_id IN (SELECT id FROM capture WHERE idempotency_key=$1)),
		(SELECT count(*) FROM capture WHERE id=$2)
		FROM capture WHERE idempotency_key=$1`, key, firstID).Scan(&captures, &events, &rolledBack); err != nil {
		t.Fatal(err)
	}
	if captures != 1 || events != 1 || rolledBack != 0 {
		t.Fatalf("captures=%d events=%d rolled-back rows=%d", captures, events, rolledBack)
	}

	frame, err := os.ReadFile(filepath.Join(storageRoot, stored.FramePath))
	if err != nil {
		t.Fatal(err)
	}
	if string(frame) != integrationFrameBytes {
		t.Fatalf("frame=%q", frame)
	}
	entries, err := os.ReadDir(storageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("storage entries=%d, want staging directory and one frame", len(entries))
	}

	staged, err := os.ReadDir(filepath.Join(storageRoot, ".staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("staging files=%d", len(staged))
	}
}
