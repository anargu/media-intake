package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
	"uuid"

	"github.com/anargu/media-intake/internal/outbox"
)

const maxFrameBytes = 10 * 1024 * 1024

var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})\.[0-9]{2}$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type receipt struct {
	captureID string
	frameSize int64
	checksum  string
}

type stub struct {
	logger *slog.Logger
	status int
	delay  time.Duration
	mu     sync.Mutex
	seen   map[string]receipt
}

func stubSettings() (int, time.Duration, error) {
	status := http.StatusNoContent
	if value := os.Getenv("STUB_STATUS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 200 || parsed > 599 {
			return 0, 0, errors.New("invalid STUB_STATUS")
		}
		status = parsed
	}

	var delay time.Duration
	if value := os.Getenv("STUB_DELAY"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("invalid STUB_DELAY")
		}
		delay = parsed
	}
	return status, delay, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	status, delay, err := stubSettings()
	if err != nil {
		logger.Error("stub configuration failed", "error", err)
		os.Exit(1)
	}

	s := &stub{logger: logger, status: status, delay: delay, seen: make(map[string]receipt)}
	srv := &http.Server{Addr: ":8080", Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: time.Minute}
	logger.Info("stub starting", "status", status, "delay", delay.String())

	if err := srv.ListenAndServe(); err != nil {
		logger.Error("stub server stopped")
		os.Exit(1)
	}
}

func (s *stub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /v1/captures", s.capture)
	return mux
}

func validID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != (uuid.UUID{}) && id.String() == value
}

func (s *stub) capture(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if !validID(key) {
		http.Error(w, "invalid delivery key", 400)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 11*1024*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "invalid multipart", 400)
		return
	}

	metadataPart, err := reader.NextPart()
	if err != nil || metadataPart.FormName() != "metadata" || metadataPart.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "metadata required", 400)
		return
	}

	data, err := io.ReadAll(io.LimitReader(metadataPart, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		http.Error(w, "invalid metadata", 400)
		return
	}

	var metadata outbox.CaptureAcceptedPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil || metadata.Version != 1 || !validID(metadata.Capture.ID) || metadata.Capture.CapturedAt.IsZero() || !amountPattern.MatchString(metadata.Capture.Amount) || !currencyPattern.MatchString(metadata.Capture.Currency) {
		http.Error(w, "invalid metadata", 400)
		return
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		http.Error(w, "invalid metadata", 400)
		return
	}

	framePart, err := reader.NextPart()
	if err != nil || framePart.FormName() != "frame" || framePart.Header.Get("Content-Type") != "application/octet-stream" {
		http.Error(w, "frame required", 400)
		return
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(framePart, maxFrameBytes+1))
	if err != nil || size == 0 || size > maxFrameBytes {
		http.Error(w, "invalid frame", 400)
		return
	}
	if _, err := reader.NextPart(); err != io.EOF {
		http.Error(w, "unexpected multipart content", 400)
		return
	}

	received := receipt{captureID: metadata.Capture.ID, frameSize: size, checksum: hex.EncodeToString(hash.Sum(nil))}
	s.mu.Lock()
	_, duplicate := s.seen[key]
	s.mu.Unlock()
	if duplicate {
		s.logger.Info("duplicate delivery", "delivery_key", key)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.logger.Info("delivery started", "delivery_key", key, "capture_id", received.captureID, "frame_bytes", size)
	if err := waitDelay(r.Context(), s.delay); err != nil {
		return
	}
	status := s.status
	s.mu.Lock()
	_, duplicate = s.seen[key]
	if duplicate {
		status = http.StatusNoContent
	} else if status >= 200 && status < 300 {
		s.seen[key] = received
	}
	s.mu.Unlock()

	s.logger.Info("delivery received", "delivery_key", key, "capture_id", received.captureID, "frame_bytes", size, "duplicate", duplicate, "status", status)
	w.WriteHeader(status)
}

func waitDelay(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
