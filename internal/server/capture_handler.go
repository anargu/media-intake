package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/capture"
)

type CaptureCreator interface {
	CreateCapture(ctx context.Context, input capture.CreateCaptureInput) (*capture.CaptureResult, error)
}

func CaptureHandler(limits CaptureLimits, fileStorage *capture.FileSystemStorage, captureCreator CaptureCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), limits.RequestTimeout)
		defer cancel()
		r = r.WithContext(ctx)

		idempotencyKey := r.Header.Get("Idempotency-Key")
		if err := capture.ValidateIdempotencyKey(idempotencyKey); err != nil {
			writeError(w, apierror.IdempotencyKeyInvalid)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limits.MaxBodyBytes)

		rawContentTypeHeader := r.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(rawContentTypeHeader)
		if err != nil {
			writeError(w, apierror.MultipartInvalid)
			return
		}
		if mediaType != "multipart/form-data" {
			writeError(w, apierror.MultipartInvalid)
			return
		}
		boundary := params["boundary"]
		if boundary == "" {
			writeError(w, apierror.MultipartInvalid)
			return
		}

		reader := multipart.NewReader(r.Body, boundary)

		var manifest capture.Manifest
		var stagedFrame *capture.StagedFrame

		isSeenFrame := false
		isSeenManifest := false
		handedOff := false
		defer func() {
			if stagedFrame != nil && !handedOff {
				_ = stagedFrame.Discard()
			}
		}()

		for {
			part, err := reader.NextRawPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				writeRequestBodyReadError(w, err)
				return
			}

			var readErr error
			var responseErr apierror.PublicError
			hasResponseErr := false

			switch part.FormName() {
			case "manifest":
				if isSeenManifest {
					responseErr = apierror.MultipartInvalid
					hasResponseErr = true
				} else {
					isSeenManifest = true
					var manifestBytes []byte
					manifestBytes, readErr = io.ReadAll(
						io.LimitReader(part, limits.MaxManifestBytes+1),
					)

					if readErr == nil {
						if int64(len(manifestBytes)) > limits.MaxManifestBytes {
							responseErr = apierror.ManifestTooLarge
							hasResponseErr = true
						} else {
							manifest, err = capture.DecodeManifest(bytes.NewReader(manifestBytes))
							if err != nil {
								responseErr = apierror.ManifestInvalid
								hasResponseErr = true
							}
						}
					}
				}
			case "frame":
				if isSeenFrame {
					responseErr = apierror.MultipartInvalid
					hasResponseErr = true
				} else {
					isSeenFrame = true

					frameType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
					if err != nil || !strings.HasPrefix(frameType, "image/") {
						responseErr = apierror.FrameInvalid
						hasResponseErr = true
					} else {
						stagedFrame, err = fileStorage.Stage(r.Context(), part)
						if err != nil {
							var maxErr *http.MaxBytesError
							if errors.As(err, &maxErr) {
								responseErr = apierror.RequestBodyTooLarge
							} else {
								// TODO: Map specific staging errors, such as an oversized or empty frame.
								responseErr = apierror.CaptureStorageUnavailable
							}
							hasResponseErr = true
						}
					}
				}

			default:
				responseErr = apierror.MultipartInvalid
				hasResponseErr = true
			}

			closeErr := part.Close()
			if readErr != nil {
				writeRequestBodyReadError(w, readErr)
				return
			}
			if closeErr != nil {
				writeRequestBodyReadError(w, closeErr)
				return
			}
			if hasResponseErr {
				writeError(w, responseErr)
				return
			}

		}

		if !isSeenManifest || !isSeenFrame {
			writeError(w, apierror.MultipartInvalid)
			return
		}

		captureInput := capture.CreateCaptureInput{
			IdempotencyKey: idempotencyKey,
			Manifest:       manifest,
			Frame:          stagedFrame,
		}

		handedOff = true
		result, err := captureCreator.CreateCapture(r.Context(), captureInput)
		if err != nil || result == nil {
			if errors.Is(err, capture.ErrCommitOutcomeUnknown) {
				writeError(w, apierror.ServiceUnavailable)
			} else {
				writeError(w, apierror.InternalError)
			}
			return
		}

		status := http.StatusCreated
		if result.Duplicate {
			status = http.StatusOK
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if err := json.NewEncoder(w).Encode(result); err != nil {
			// TODO: Log write errors
		}
	}
}

func writeRequestBodyReadError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, apierror.RequestBodyTooLarge)
		return
	}
	writeError(w, apierror.MultipartInvalid)
}
