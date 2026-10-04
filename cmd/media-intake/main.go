package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anargu/media-intake/internal/capture"
	"github.com/anargu/media-intake/internal/config"
	"github.com/anargu/media-intake/internal/database"
	"github.com/anargu/media-intake/internal/outbox"
	"github.com/anargu/media-intake/internal/server"
)

const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, logger)
	stop()
	if err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Database
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("database startup failed")
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		return errors.New("database migration failed")
	}

	// Storage
	storage, err := capture.NewFileSystemStorage(cfg.CaptureStorageDir, cfg.MaxFrameBytes)
	if err != nil {
		return errors.New("storage startup failed")
	}
	root, err := os.OpenRoot(cfg.CaptureStorageDir)
	if err != nil {
		return errors.New("storage root startup failed")
	}
	defer root.Close()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()

	client, err := outbox.NewDownstreamClient(cfg.DownstreamURL, cfg.DownstreamTimeout, transport)
	if err != nil {
		return err
	}
	// Worker
	worker, err := outbox.NewWorker(pool,
		client, root, cfg, logger,
		time.Now, rand.Int64N, nil)
	if err != nil {
		return err
	}

	ready := &runtimeReadiness{database: pool}
	handler := server.New(
		logger,
		server.CaptureLimits{
			MaxBodyBytes:     cfg.MaxBodyBytes,
			MaxManifestBytes: cfg.MaxManifestBytes,
			RequestTimeout:   cfg.RequestTimeout,
		},
		ready, storage, capture.NewCaptureService(pool))

	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()

	httpServer := newHTTPServer(cfg.HTTPAddr, handler, cfg.RequestTimeout)
	httpServer.BaseContext = func(net.Listener) context.Context { return workCtx }
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return errors.New("HTTP listener startup failed")
	}
	defer listener.Close()

	return serveWithWorker(ctx, workCtx, cancelWork,
		httpServer, listener, worker,
		ready, cfg.ShutdownTimeout, logger)
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
