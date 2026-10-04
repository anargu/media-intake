package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/anargu/media-intake/internal/apierror"
	"github.com/anargu/media-intake/internal/capture"
	"github.com/anargu/media-intake/internal/requestid"
	"github.com/go-chi/chi/v5"
)

const readinessTimeout = 2 * time.Second

type DatabaseHealth interface {
	Ping(context.Context) error
}

type CaptureService interface {
	CaptureCreator
	CaptureGetter
}

type CaptureLimits struct {
	MaxBodyBytes     int64
	MaxManifestBytes int64
	RequestTimeout   time.Duration
}

func New(logger *slog.Logger,
	captureLimits CaptureLimits,
	database DatabaseHealth,
	fileStorage *capture.FileSystemStorage,
	captureService CaptureService) http.Handler {
	router := chi.NewRouter()

	// Applying Middlewares
	router.Use(withRequestID)
	router.Use(requestLogger(logger))

	// Endpoints
	router.Get("/livez", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, "{\"status\":\"alive\"}\n")
	})
	router.Get("/readyz", func(response http.ResponseWriter, request *http.Request) {
		if database == nil {
			writeError(response, apierror.ServiceUnavailable)
			return
		}

		ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			requestID, _ := requestid.FromContext(request.Context())

			logger.Error("database readiness check failed",
				"request_id", requestID,
				"error", err,
			)
			writeError(response, apierror.ServiceUnavailable)
			return
		}

		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, "{\"status\":\"ready\"}\n")
	})

	router.Route("/v1", func(r chi.Router) {
		r.Post("/captures", CaptureHandler(captureLimits, fileStorage, captureService))

		r.Get("/captures/{key}", GetCaptureHandler(captureService))
	})

	router.NotFound(func(response http.ResponseWriter, _ *http.Request) {
		writeError(response, apierror.NotFound)
	})
	router.MethodNotAllowed(func(response http.ResponseWriter, _ *http.Request) {
		writeError(response, apierror.MethodNotAllowed)
	})

	return router
}
