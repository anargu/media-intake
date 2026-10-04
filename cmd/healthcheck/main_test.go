package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckStatusAndRedirect(t *testing.T) {
	for _, status := range []int{200, 503, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/readyz")
				w.WriteHeader(status)
			}))
			defer server.Close()
			err := check(server.URL)
			if (err == nil) != (status == 200) {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}
