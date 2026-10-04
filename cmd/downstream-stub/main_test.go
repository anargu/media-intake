package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/anargu/media-intake/internal/outbox"
)

func newStub(t *testing.T, status int) (*stub, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	return &stub{status: status, logger: slog.New(slog.NewJSONHandler(logs, nil)), seen: make(map[string]receipt)}, logs
}
func metadataBytes(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(outbox.CaptureAcceptedPayload{Version: 1, Capture: outbox.CaptureMetadata{ID: uuid.New().String(), CapturedAt: time.Now().UTC(), Amount: "9007199254740993.01", Currency: "PEN"}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func multipartRequest(t *testing.T, key string, metadata, frame []byte, extra bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range []struct {
		name, contentType string
		body              []byte
	}{{"metadata", "application/json", metadata}, {"frame", "application/octet-stream", frame}} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+part.name+`"`)
		header.Set("Content-Type", part.contentType)
		destination, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := destination.Write(part.body); err != nil {
			t.Fatal(err)
		}
	}
	if extra {
		if err := writer.WriteField("extra", "unexpected"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/captures", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Idempotency-Key", key)
	return request
}

func TestStubRecordsFrameAndAcceptsDuplicates(t *testing.T) {
	s, logs := newStub(t, 204)
	key := uuid.New().String()
	metadata := metadataBytes(t)
	frame := []byte("opaque-private-frame-contents")
	first := httptest.NewRecorder()

	s.handler().ServeHTTP(first, multipartRequest(t, key, metadata, frame, false))
	if first.Code != 204 || len(s.seen) != 1 {
		t.Fatalf("status=%d receipts=%d", first.Code, len(s.seen))
	}

	sum := sha256.Sum256(frame)
	receipt := s.seen[key]
	if receipt.frameSize != int64(len(frame)) || receipt.checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("receipt=%#v", receipt)
	}
	// A duplicate remains successful even when failure/delay controls are active.
	s.status = 503
	s.delay = time.Hour

	second := httptest.NewRecorder()
	s.handler().ServeHTTP(second, multipartRequest(t, key, metadata, frame, false))

	if second.Code != 204 || len(s.seen) != 1 {
		t.Fatalf("duplicate status=%d receipts=%d", second.Code, len(s.seen))
	}
	if strings.Contains(logs.String(), string(frame)) || strings.Contains(logs.String(), "9007199254740993.01") {
		t.Fatal("logs contain frame or manifest values")
	}
}

func TestStubFailureModesDoNotAcceptKey(t *testing.T) {
	for _, status := range []int{422, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _ := newStub(t, status)
			response := httptest.NewRecorder()
			s.handler().ServeHTTP(response, multipartRequest(t, uuid.New().String(), metadataBytes(t), []byte("frame"), false))

			if response.Code != status || len(s.seen) != 0 {
				t.Fatalf("status=%d receipts=%d", response.Code, len(s.seen))
			}
		})
	}
}

func TestStubRejectsMalformedRequests(t *testing.T) {
	metadata := metadataBytes(t)
	for _, test := range []struct {
		name, key       string
		metadata, frame []byte
		extra           bool
	}{
		{"bad key", "invalid", metadata, []byte("frame"), false},
		{"bad metadata", uuid.New().String(), []byte(`{"version":2}`), []byte("frame"), false},
		{"trailing JSON", uuid.New().String(), append(bytes.Clone(metadata), []byte(` {}`)...), []byte("frame"), false},
		{"empty frame", uuid.New().String(), metadata, nil, false},
		{"oversized frame", uuid.New().String(), metadata, make([]byte, maxFrameBytes+1), false},
		{"extra part", uuid.New().String(), metadata, []byte("frame"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := newStub(t, 204)
			response := httptest.NewRecorder()
			s.handler().ServeHTTP(response, multipartRequest(t, test.key, test.metadata, test.frame, test.extra))
			if response.Code != 400 || len(s.seen) != 0 {
				t.Fatalf("status=%d receipts=%d", response.Code, len(s.seen))
			}
		})
	}
	s, _ := newStub(t, 204)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/captures", strings.NewReader("not multipart"))
	request.Header.Set("Idempotency-Key", uuid.New().String())

	s.handler().ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatalf("non-multipart=%d", response.Code)
	}
}

func TestStubDelayCancellationDoesNotRecordAcceptance(t *testing.T) {
	s, _ := newStub(t, 204)
	s.delay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := multipartRequest(t, uuid.New().String(), metadataBytes(t), []byte("frame"), false).
		WithContext(ctx)
	s.handler().ServeHTTP(httptest.NewRecorder(), request)
	if len(s.seen) != 0 {
		t.Fatal("canceled delivery recorded as accepted")
	}

	response := httptest.NewRecorder()
	s.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != 200 {
		t.Fatalf("delayed stub liveness=%d", response.Code)
	}
}

func TestStubConcurrentDuplicates(t *testing.T) {
	s, _ := newStub(t, 204)
	metadata := metadataBytes(t)
	key := uuid.New().String()
	requests := []*http.Request{
		multipartRequest(t, key, metadata, []byte("frame"), false),
		multipartRequest(t, key, metadata, []byte("frame"), false),
	}
	responses := make(chan int, 2)

	var group sync.WaitGroup
	start := make(chan struct{})
	for _, request := range requests {
		group.Add(1)
		go func(request *http.Request) {
			defer group.Done()
			<-start
			response := httptest.NewRecorder()
			s.handler().ServeHTTP(response, request)
			responses <- response.Code
		}(request)
	}
	close(start)
	group.Wait()

	for range requests {
		if code := <-responses; code != 204 {
			t.Fatalf("status=%d", code)
		}
	}
	if len(s.seen) != 1 {
		t.Fatalf("receipts=%d", len(s.seen))
	}
}
