package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anargu/media-intake/internal/capture"
	"github.com/anargu/media-intake/internal/config"
	"github.com/anargu/media-intake/internal/database"
	"github.com/anargu/media-intake/internal/server"
)

const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}

	databasePool, err := database.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer databasePool.Close()
	if err := database.Migrate(context.Background(), databasePool); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}

	fileSystemstorage, err := capture.NewFileSystemStorage(cfg.CaptureStorageDir, cfg.MaxFrameBytes)
	if err != nil {
		logger.Error("storage directory is not writable")
		os.Exit(1)
	}

	captureService := &capture.CaptureService{}

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpServer := newHTTPServer(cfg.HTTPAddr,
		server.New(
			logger,
			server.CaptureLimits{
				MaxBodyBytes:     cfg.MaxBodyBytes,
				MaxManifestBytes: cfg.MaxManifestBytes,
				RequestTimeout:   cfg.RequestTimeout,
			},
			databasePool,
			fileSystemstorage,
			captureService),
		cfg.RequestTimeout)

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}

	logger.Info("server starting", "address", cfg.HTTPAddr)
	if err := serve(signalContext, httpServer, listener, cfg.ShutdownTimeout); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped")
}

func newHTTPServer(address string, handler http.Handler, requestTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       requestTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func serve(ctx context.Context, httpServer *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}

		if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
