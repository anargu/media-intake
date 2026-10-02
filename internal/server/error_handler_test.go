package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/anargu/media-intake/internal/apierror"
)

func TestWriteError(t *testing.T) {
	publicErrors := []apierror.PublicError{
		apierror.ManifestInvalid,
		apierror.IdempotencyKeyInvalid,
		apierror.MultipartInvalid,
		apierror.FrameTooLarge,
		apierror.ManifestTooLarge,
		apierror.RequestBodyTooLarge,
		apierror.FrameInvalid,
		apierror.CaptureNotFound,
		apierror.InternalError,
		apierror.ServiceUnavailable,
		apierror.CaptureStorageUnavailable,
	}

	for _, publicErr := range publicErrors {
		t.Run(publicErr.ErrorClass, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeError(recorder, publicErr)

			if recorder.Code != publicErr.HTTPCode {
				t.Fatalf("status = %d, want %d", recorder.Code, publicErr.HTTPCode)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			var got apierror.PublicError
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got != publicErr {
				t.Errorf("response envelope = %#v, want %#v", got, publicErr)
			}
		})
	}
}
