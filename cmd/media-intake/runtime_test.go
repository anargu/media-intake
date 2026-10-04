package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeWorkerFunc func(context.Context, context.Context) error

func (f runtimeWorkerFunc) RunUntil(stop, work context.Context) error { return f(stop, work) }

type runtimeHealth struct{ calls atomic.Int32 }

func (h *runtimeHealth) Ping(context.Context) error { h.calls.Add(1); return nil }

func TestRuntimeReadinessFlag(t *testing.T) {
	health := &runtimeHealth{}
	ready := &runtimeReadiness{database: health}

	if err := ready.Ping(context.Background()); err == nil {
		t.Fatal("ready before startup")
	}
	if health.calls.Load() != 0 {
		t.Fatal("database pinged before startup")
	}

	ready.ready.Store(true)
	if err := ready.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}

	ready.ready.Store(false)
	if err := ready.Ping(context.Background()); err == nil {
		t.Fatal("ready during drain")
	}
}

func TestRuntimeDrainsHTTPAndWorker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	workerEntered := make(chan struct{})
	claimsStopped := make(chan struct{})
	releaseWorker := make(chan struct{})

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()

	srv := newHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestEntered)
		select {
		case <-releaseRequest:
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
	}), time.Second)
	srv.BaseContext = func(net.Listener) context.Context { return workCtx }

	worker := runtimeWorkerFunc(func(claims, work context.Context) error {
		close(workerEntered)
		<-claims.Done()
		close(claimsStopped)
		select {
		case <-releaseWorker:
			return nil
		case <-work.Done():
			return work.Err()
		}
	})

	ready := &runtimeReadiness{database: &runtimeHealth{}}
	done := make(chan error, 1)
	go func() {
		done <- serveWithWorker(ctx, workCtx, cancelWork, srv, listener, worker, ready, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	<-workerEntered

	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
			if response.StatusCode != 204 {
				err = errors.New("request did not complete")
			}
		}
		requestDone <- err
	}()
	<-requestEntered

	stop()
	<-claimsStopped

	if ready.ready.Load() {
		t.Fatal("ready while draining")
	}
	if workCtx.Err() != nil {
		t.Fatal("in-flight work canceled before deadline")
	}
	select {
	case err := <-done:
		t.Fatalf("runtime exited before drain: %v", err)
	default:
	}

	close(releaseRequest)
	close(releaseWorker)

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not drain")
	}

	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDeadlineCancelsWorker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()

	entered := make(chan struct{})
	canceled := make(chan struct{})
	worker := runtimeWorkerFunc(func(claims, work context.Context) error {
		close(entered)
		<-work.Done()
		close(canceled)
		return work.Err()
	})

	srv := newHTTPServer("", http.NotFoundHandler(), time.Second)
	ready := &runtimeReadiness{database: &runtimeHealth{}}
	done := make(chan error, 1)

	go func() {
		done <- serveWithWorker(ctx, workCtx, cancelWork, srv, listener, worker, ready, 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	<-entered

	stop()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("forced shutdown returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel worker")
	}

	<-canceled
	if ready.ready.Load() {
		t.Fatal("ready after forced shutdown")
	}
}

func TestRuntimeWorkerFailureStopsHTTP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()

	ready := &runtimeReadiness{database: &runtimeHealth{}}
	worker := runtimeWorkerFunc(func(context.Context, context.Context) error { return errors.New("failure") })
	mockHttpHandler := newHTTPServer("", http.NotFoundHandler(), time.Second)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err = serveWithWorker(context.Background(), workCtx, cancelWork,
		mockHttpHandler,
		listener, worker, ready, time.Second, logger)

	if err == nil || ready.ready.Load() {
		t.Fatalf("failure result=%v readiness=%t", err, ready.ready.Load())
	}
}
