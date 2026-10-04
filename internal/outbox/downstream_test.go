package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"
)

func testDeliveryEvent(t *testing.T) Outbox {
	t.Helper()
	captureID := uuid.New()
	payload, err := json.Marshal(CaptureAcceptedPayload{
		Version: 1,
		Capture: CaptureMetadata{
			ID:         captureID.String(),
			Amount:     "9007199254740993.01",
			Currency:   "PEN",
			CapturedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Outbox{ID: uuid.New(), CaptureID: captureID, Payload: payload}
}

func TestDownstreamMultipartAndStableKey(t *testing.T) {
	event := testDeliveryEvent(t)
	original := []byte{0, 1, 255, 13, 10, 0, 42}
	path := filepath.Join(t.TempDir(), "frame")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != event.ID.String() {
			t.Error("incorrect method or delivery key")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		if part.FormName() != "metadata" || part.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect metadata headers")
		}
		metadata, err := io.ReadAll(part)
		if err != nil || !bytes.Equal(metadata, event.Payload) {
			t.Errorf("metadata=%s err=%v", metadata, err)
		}
		part, err = reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		if part.FormName() != "frame" ||
			part.FileName() != "frame" ||
			part.Header.Get("Content-Type") != "application/octet-stream" {
			t.Error("incorrect frame headers")
		}
		frame, err := io.ReadAll(part)
		if err != nil || !bytes.Equal(frame, original) {
			t.Errorf("frame=%v err=%v", frame, err)
		}
		if _, err := reader.NextPart(); err != io.EOF {
			t.Errorf("unexpected additional part: %v", err)
		}
		if calls == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()

	client, err := NewDownstreamClient(server.URL, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}

	open := func() (io.ReadCloser, error) { return os.Open(path) }
	if got := client.Deliver(context.Background(), event, open); got.Kind != TransientFailure {
		t.Fatalf("first=%#v", got)
	}
	if got := client.Deliver(context.Background(), event, open); got.Kind != Delivered {
		t.Fatalf("retry=%#v", got)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestDownstreamResponseClassification(t *testing.T) {
	for _, test := range []struct {
		status int
		want   DeliveryKind
	}{
		{200, Delivered}, {201, Delivered}, {204, Delivered}, {408, TransientFailure},
		{425, TransientFailure}, {429, TransientFailure}, {500, TransientFailure}, {503, TransientFailure},
		{400, PermanentFailure}, {401, PermanentFailure}, {404, PermanentFailure}, {422, PermanentFailure},
		{302, PermanentFailure},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			redirected := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					redirected = true
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("private downstream body"))
			}))
			defer server.Close()
			client, err := NewDownstreamClient(server.URL, time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := client.Deliver(context.Background(), testDeliveryEvent(t), func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("frame"))), nil })
			if got.Kind != test.want {
				t.Fatalf("result=%#v", got)
			}
			if len(got.Diagnostic) > 64 || bytes.Contains([]byte(got.Diagnostic), []byte("private")) {
				t.Fatalf("unsafe diagnostic: %q", got.Diagnostic)
			}
			if redirected {
				t.Fatal("redirect followed")
			}
		})
	}
}

func TestDownstreamTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release
	}))
	defer server.Close()
	defer close(release)

	client, err := NewDownstreamClient(server.URL, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := client.Deliver(context.Background(), testDeliveryEvent(t), func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte("frame"))), nil })
	if got.Kind != TransientFailure || got.Diagnostic != "downstream transport failure" {
		t.Fatalf("timeout=%#v", got)
	}
}

func TestDownstreamLocalFailures(t *testing.T) {
	client, err := NewDownstreamClient("http://localhost:1", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	event := testDeliveryEvent(t)
	open := func() (io.ReadCloser, error) { return nil, errors.New("private storage path") }
	if got := client.Deliver(context.Background(), event, open); got.Kind != TransientFailure || got.Diagnostic != "frame unavailable" {
		t.Fatalf("frame error=%#v", got)
	}
	event.Payload = json.RawMessage(`{"version":2}`)
	if got := client.Deliver(context.Background(), event, open); got.Kind != PermanentFailure {
		t.Fatalf("invalid event=%#v", got)
	}
}
