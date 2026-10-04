package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/capture"
	"github.com/anargu/media-intake/internal/outbox"
)

func TestCapturePostPersistsCaptureFrameAndOutbox(t *testing.T) {
	pool, handler, storageRoot := newCaptureIntegrationHandler(t)
	idempotencyKey := integrationKey("post-success")
	cleanupCaptureByKey(t, pool, idempotencyKey)
	t.Cleanup(func() { cleanupCaptureByKey(t, pool, idempotencyKey) })

	request := newCapturePostRequest(t, idempotencyKey, "9007199254740993.01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}

	var result struct {
		Duplicate bool `json:"duplicate"`
		Capture   struct {
			ID     string `json:"id"`
			Amount string `json:"amount"`
		} `json:"capture"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Duplicate {
		t.Fatal("duplicate = true, want false for a new capture")
	}
	if got := result.Capture.Amount; got != "9007199254740993.01" {
		t.Errorf("response amount = %q, want %q", got, "9007199254740993.01")
	}

	store, err := capture.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	storedCapture, err := store.GetByIdempotencyKey(request.Context(), idempotencyKey)
	if err != nil {
		t.Fatalf("get stored capture: %v", err)
	}
	if storedCapture.ID.String() != result.Capture.ID {
		t.Errorf("stored capture ID = %q, want response ID %q", storedCapture.ID, result.Capture.ID)
	}
	if storedCapture.Amount != "9007199254740993.01" {
		t.Errorf("stored amount = %q, want %q", storedCapture.Amount, "9007199254740993.01")
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/v1/captures/"+idempotencyKey, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d; body = %s", getResponse.Code, http.StatusOK, getResponse.Body.String())
	}

	var retrieved struct {
		ID         string    `json:"id"`
		CapturedAt time.Time `json:"capturedAt"`
		Amount     string    `json:"amount"`
		Currency   string    `json:"currency"`
	}
	if err := json.NewDecoder(getResponse.Body).Decode(&retrieved); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if retrieved.ID != result.Capture.ID {
		t.Errorf("GET ID = %q, want %q", retrieved.ID, result.Capture.ID)
	}
	if retrieved.Amount != "9007199254740993.01" {
		t.Errorf("GET amount = %q, want %q", retrieved.Amount, "9007199254740993.01")
	}
	if retrieved.Currency != "PEN" {
		t.Errorf("GET currency = %q, want %q", retrieved.Currency, "PEN")
	}
	if want := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC); !retrieved.CapturedAt.Equal(want) {
		t.Errorf("GET capturedAt = %s, want %s", retrieved.CapturedAt, want)
	}

	framePath := filepath.Join(storageRoot, storedCapture.FramePath)
	frame, err := os.ReadFile(framePath)
	if err != nil {
		t.Fatalf("read published frame: %v", err)
	}
	if string(frame) != integrationFrameBytes {
		t.Errorf("published frame = %q, want %q", frame, integrationFrameBytes)
	}

	var outboxID, outboxCaptureID, eventType string
	var payloadBytes []byte
	err = pool.QueryRow(request.Context(), `
		SELECT id::text, capture_id::text, event_type, payload
		FROM outbox
		WHERE capture_id = $1`, storedCapture.ID).Scan(
		&outboxID,
		&outboxCaptureID,
		&eventType,
		&payloadBytes,
	)
	if err != nil {
		t.Fatalf("get outbox event: %v", err)
	}
	if outboxID == "" || outboxID == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("outbox ID = %q, want a generated ID", outboxID)
	}
	if outboxCaptureID != storedCapture.ID.String() {
		t.Errorf("outbox capture ID = %q, want %q", outboxCaptureID, storedCapture.ID)
	}
	if eventType != outbox.EventTypeCaptureAccepted {
		t.Errorf("event type = %q, want %q", eventType, outbox.EventTypeCaptureAccepted)
	}

	var payload outbox.CaptureAcceptedPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if payload.Version != 1 || payload.Capture.ID != storedCapture.ID.String() || payload.Capture.Amount != "9007199254740993.01" {
		t.Errorf("outbox payload = %#v, want version 1 metadata for stored capture", payload)
	}

	stagingEntries, err := os.ReadDir(filepath.Join(storageRoot, ".staging"))
	if err != nil {
		t.Fatalf("read staging directory: %v", err)
	}
	if len(stagingEntries) != 0 {
		t.Errorf("staging directory contains %d entries, want none", len(stagingEntries))
	}
}

func TestCaptureGetReturnsNotFoundForUnknownKey(t *testing.T) {
	_, handler, _ := newCaptureIntegrationHandler(t)
	idempotencyKey := integrationKey("get-missing")

	request := httptest.NewRequest(http.MethodGet, "/v1/captures/"+idempotencyKey, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want %d; body = %s", response.Code, http.StatusNotFound, response.Body.String())
	}

	var got apierror.PublicError
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET error: %v", err)
	}
	if got != apierror.CaptureNotFound {
		t.Errorf("GET error = %#v, want %#v", got, apierror.CaptureNotFound)
	}
}
