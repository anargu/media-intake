package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/anargu/media-intake/internal/server"
)

type runtimeReadiness struct {
	database server.DatabaseHealth
	ready    atomic.Bool
}

func (r *runtimeReadiness) Ping(ctx context.Context) error {
	if !r.ready.Load() {
		return errors.New("service is not ready")
	}
	if err := r.database.Ping(ctx); err != nil {
		return err
	}
	if !r.ready.Load() {
		return errors.New("service is draining")
	}
	return nil
}

type runtimeWorker interface {
	RunUntil(context.Context, context.Context) error
}

func serveWithWorker(ctx, workCtx context.Context, cancelWork context.CancelFunc, httpServer *http.Server, listener net.Listener, worker runtimeWorker, readiness *runtimeReadiness, timeout time.Duration, logger *slog.Logger) error {
	claimsCtx, stopClaims := context.WithCancel(workCtx)
	defer stopClaims()

	httpDone := make(chan error, 1)
	workerDone := make(chan error, 1)

	go func() { httpDone <- httpServer.Serve(listener) }()
	go func() { workerDone <- worker.RunUntil(claimsCtx, workCtx) }()

	readiness.ready.Store(true)
	logger.Info("server starting", "address", listener.Addr().String())
	var cause error
	httpStopped, workerStopped := false, false

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case <-httpDone:
		httpStopped = true
		cause = errors.New("HTTP component stopped unexpectedly")
	case <-workerDone:
		workerStopped = true
		cause = errors.New("worker component stopped unexpectedly")
	}
	readiness.ready.Store(false)
	stopClaims()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		cancelWork()
		_ = httpServer.Close()
		cause = errors.Join(cause, errors.New("HTTP shutdown deadline exceeded"))
	}
	if !workerStopped {
		select {
		case err := <-workerDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				cause = errors.Join(cause, errors.New("worker shutdown failed"))
			}
		case <-shutdownCtx.Done():
			cancelWork()
			<-workerDone // bounded operation/rollback cleanup before closing the pool
			cause = errors.Join(cause, errors.New("worker shutdown deadline exceeded"))
		}
	}

	if !httpStopped {
		<-httpDone
	}
	cancelWork()
	logger.Info("server stopped", "forced", cause != nil)
	return cause
}
