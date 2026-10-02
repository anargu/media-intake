package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/requestid"
)

type fakeDatabaseHealth struct {
	ping func(context.Context) error
}

func (f *fakeDatabaseHealth) Ping(ctx context.Context) error {
	if f.ping != nil {
		return f.ping(ctx)
	}
	return nil
}

func TestLivez(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	logger := testLogger()

	New(logger, &fakeDatabaseHealth{}).ServeHTTP(response, request)

	result := response.Result()
	defer result.Body.Close()

	if result.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", result.StatusCode, http.StatusOK)
	}
	if got := result.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := response.Body.String(); got != "{\"status\":\"alive\"}\n" {
		t.Errorf("body = %q, want %q", got, "{\"status\":\"alive\"}\n")
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantStatus int
	}{
		{name: "database available", wantStatus: http.StatusOK},
		{name: "database unavailable", pingErr: errors.New("database unavailable"), wantStatus: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pingCalled := false
			database := &fakeDatabaseHealth{ping: func(ctx context.Context) error {
				pingCalled = true
				if _, ok := ctx.Deadline(); !ok {
					t.Error("Ping context has no deadline")
				}
				return tc.pingErr
			}}
			request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			response := httptest.NewRecorder()
			New(testLogger(), database).ServeHTTP(response, request)

			if !pingCalled {
				t.Fatal("database Ping was not called")
			}
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			if tc.pingErr == nil {
				if got, want := response.Body.String(), "{\"status\":\"ready\"}\n"; got != want {
					t.Errorf("body = %q, want %q", got, want)
				}
				return
			}

			var got apierror.PublicError
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode readiness error: %v", err)
			}
			if got != apierror.ServiceUnavailable {
				t.Errorf("error envelope = %#v, want %#v", got, apierror.ServiceUnavailable)
			}
		})
	}
}

func TestRouterErrorsUseSharedEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantError  apierror.PublicError
	}{
		{name: "not found", method: http.MethodGet, path: "/missing", wantStatus: http.StatusNotFound, wantError: apierror.NotFound},
		{name: "method not allowed", method: http.MethodPost, path: "/livez", wantStatus: http.StatusMethodNotAllowed, wantError: apierror.MethodNotAllowed},
	}

	handler := New(testLogger(), nil)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tc.wantStatus)
			}
			var got apierror.PublicError
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if got != tc.wantError {
				t.Errorf("error envelope = %#v, want %#v", got, tc.wantError)
			}
		})
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// Happy path
func TestWithRequestIDReturnsID(t *testing.T) {
	var dependencyRequestID string
	var dependencyFoundRequestID bool

	fakeDependency := func(ctx context.Context) {
		dependencyRequestID, dependencyFoundRequestID = requestid.FromContext(ctx)
	}

	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fakeDependency(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if !dependencyFoundRequestID {
		t.Fatal("dependency could not read request ID from context")
	}
	if dependencyRequestID == "" {
		t.Error("dependency received an empty request ID")
	}
}

func TestRequestLoggerLogsRequestMetadata(t *testing.T) {
	var logOutput bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	handler := withRequestID(requestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})))

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var entry struct {
		Method    string `json:"method"`
		Status    int    `json:"status"`
		RequestID string `json:"request_id"`
		Duration  int64  `json:"duration_ms"`
	}

	if err := json.Unmarshal(logOutput.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to unmarshal log entry: %v", err)
	}

	if entry.Method != http.MethodPost {
		t.Errorf("Logged method = %q, want %q", entry.Method, http.MethodPost)
	}
	if entry.Status != http.StatusUnprocessableEntity {
		t.Errorf("Logged status = %d, want %d", entry.Status, http.StatusUnprocessableEntity)
	}
	if entry.RequestID == "" {
		t.Errorf("Logged request_id is empty")
	}
	if entry.Duration < 0 {
		t.Errorf("Logged duration_ms = %d, want non-negative", entry.Duration)
	}
}
