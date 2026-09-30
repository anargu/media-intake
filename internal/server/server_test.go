package server

import (
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
