package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/capture"
)

// Mocking deps
type fakeCaptureCreator struct {
	err    error
	called bool
}

func (f *fakeCaptureCreator) CreateCapture(
	_ context.Context, input capture.CreateCaptureInput,
) (*capture.CaptureResult, error) {
	f.called = true

	if input.Frame != nil {
		_ = input.Frame.Discard()
	}
	return nil, f.err
}

func TestCaptureHandlerCreatorErrorUsesInternalError(t *testing.T) {
	body := &bytes.Buffer{}
	multipartWriter := multipart.NewWriter(body)

	if err := multipartWriter.WriteField(
		"manifest",
		`{"capturedAt":"2026-10-03T12:00:00Z","amount":"12.34","currency":"PEN"}`,
	); err != nil {
		t.Fatal(err)
	}

	frameHeader := make(textproto.MIMEHeader)
	frameHeader.Set("Content-Disposition", `form-data; name="frame"; filename="frame.jpg"`)
	frameHeader.Set("Content-Type", "image/jpeg")

	framePart, err := multipartWriter.CreatePart(frameHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := framePart.Write([]byte("frame bytes")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/captures", body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Idempotency-Key", "handler-test-001")

	storage, err := capture.NewFileSystemStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}

	creator := &fakeCaptureCreator{err: errors.New("database failed")}
	handler := CaptureHandler(
		CaptureLimits{
			MaxBodyBytes:     1 << 20,  // 1 MB
			MaxManifestBytes: 64 << 10, // 64 KB
			RequestTimeout:   time.Second,
		},
		storage,
		creator,
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if !creator.called {
		t.Fatal("CreateCapture was not called")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}

	var responseError apierror.PublicError
	if err := json.NewDecoder(response.Body).Decode(&responseError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if responseError != apierror.InternalError {
		t.Errorf("error = %#v, want %#v", responseError, apierror.InternalError)
	}
}

func TestCaptureHandlerUnknownCommitReturnsTransientError(t *testing.T) {
	storage, err := capture.NewFileSystemStorage(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	creator := &fakeCaptureCreator{err: errors.Join(capture.ErrCommitOutcomeUnknown, errors.New("private database details"))}
	handler := CaptureHandler(CaptureLimits{MaxBodyBytes: 1 << 20, MaxManifestBytes: 64 << 10, RequestTimeout: time.Second}, storage, creator)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newCapturePostRequest(t, "unknown-commit", "12.34"))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	var public apierror.PublicError
	if err := json.Unmarshal(response.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if public != apierror.ServiceUnavailable {
		t.Fatalf("error=%#v", public)
	}
}

func TestCaptureHandlerClassifiesInvalidFrames(t *testing.T) {
	tests := []struct {
		name          string
		key           string
		frame         []byte
		maxFrameBytes int64
		maxBodyBytes  int64
		want          apierror.PublicError
	}{
		{name: "invalid key", key: "", frame: []byte("frame"), maxFrameBytes: 2048, maxBodyBytes: 1 << 20, want: apierror.IdempotencyKeyInvalid},
		{name: "empty frame", key: "test-key", maxFrameBytes: 4, maxBodyBytes: 1 << 20, want: apierror.FrameInvalid},
		{name: "oversized frame", key: "test-key", frame: []byte("12345"), maxFrameBytes: 4, maxBodyBytes: 1 << 20, want: apierror.FrameTooLarge},
		{name: "oversized body during frame read", key: "test-key", frame: bytes.Repeat([]byte("x"), 1024), maxFrameBytes: 2048, maxBodyBytes: 300, want: apierror.RequestBodyTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			if err := writer.WriteField("manifest", `{"capturedAt":"2026-10-03T12:00:00Z","amount":"12.34","currency":"PEN"}`); err != nil {
				t.Fatal(err)
			}

			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", `form-data; name="frame"; filename="frame.jpg"`)
			header.Set("Content-Type", "image/jpeg")

			part, err := writer.CreatePart(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(tt.frame); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodPost, "/v1/captures", body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			request.Header.Set("Idempotency-Key", tt.key)

			storage, err := capture.NewFileSystemStorage(t.TempDir(), tt.maxFrameBytes)
			if err != nil {
				t.Fatal(err)
			}

			creator := &fakeCaptureCreator{}
			handler := CaptureHandler(CaptureLimits{
				MaxBodyBytes: tt.maxBodyBytes, MaxManifestBytes: 64 << 10, RequestTimeout: time.Second,
			}, storage, creator)

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want.HTTPCode {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.want.HTTPCode, response.Body.String())
			}
			var got apierror.PublicError
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("error = %#v, want %#v", got, tt.want)
			}
			if creator.called {
				t.Error("invalid upload reached capture creation")
			}
		})
	}
}
