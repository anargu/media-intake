package requestid

import (
	"context"
)

type contextKey struct{}

// WithRequestID middleware to generate request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, ok := ctx.Value(contextKey{}).(string)
	return requestID, ok
}
