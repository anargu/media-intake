package server

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/anargu/media-intake/internal/requestid"
)

// withRequestID middleware to generate request ID.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := rand.Text()

		ctx := requestid.WithRequestID(r.Context(), requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status          int
	isWrittenHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.isWrittenHeader {
		w.status = status
		w.isWrittenHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

// requestLogger Structured Logging middleware
func requestLogger(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			requestID, _ := requestid.FromContext(r.Context())

			next.ServeHTTP(response, r)

			logger.Info("http request",
				"method", r.Method,
				"status", response.status,
				"url", r.URL.String(),
				"request_id", requestID,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
