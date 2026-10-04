package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/capture"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type CaptureGetter interface {
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*capture.CaptureResponse, error)
}

func GetCaptureHandler(captureGetter CaptureGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inputKey := chi.URLParam(r, "key")
		err := capture.ValidateIdempotencyKey(inputKey)
		if err != nil {
			writeError(w, apierror.IdempotencyKeyInvalid)
			return
		}

		captureResponse, err := captureGetter.GetByIdempotencyKey(r.Context(), inputKey)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, apierror.CaptureNotFound)
				return
			}

			writeError(w, apierror.InternalError)
			return
		}
		if captureResponse == nil {
			writeError(w, apierror.InternalError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(captureResponse); err != nil {
			// TODO: Handle error json encoder
		}
	}
}
