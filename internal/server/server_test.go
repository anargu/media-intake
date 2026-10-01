package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivez(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	New().ServeHTTP(response, request)

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

// Happy path
func TestWithRequestIDReturnsID(t *testing.T) {
	var dependencyRequestID string

	fakeDependency := func(ctx context.Context) {
		dependencyRequestID = getRequestIDFromContext(ctx)
	}

	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fakeDependency(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if dependencyRequestID == "" {
		t.Errorf("Request ID is empty")
	}
}
