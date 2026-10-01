package server

import (
	"context"
	"crypto/rand"
	"net/http"
)

type requestIDKey struct{}

func getRequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, ok := ctx.Value(requestIDKey{}).(string); ok {
		return requestID
	}
	return ""
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := rand.Text()
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
