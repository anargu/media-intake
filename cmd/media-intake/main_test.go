package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestServeDrainsInFlightRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	defer release()

	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		response.WriteHeader(http.StatusNoContent)
	})
	httpServer := newHTTPServer("127.0.0.1:0", handler)
	shutdownStarted := make(chan struct{})
	httpServer.RegisterOnShutdown(func() {
		close(shutdownStarted)
	})
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	shutdownContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(shutdownContext, httpServer, listener, time.Second)
	}()

	type responseResult struct {
		status int
		err    error
	}
	responseDone := make(chan responseResult, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err != nil {
			responseDone <- responseResult{err: err}
			return
		}
		defer response.Body.Close()
		responseDone <- responseResult{status: response.StatusCode}
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}

	cancel()
	select {
	case <-shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start")
	}
	select {
	case err := <-serveDone:
		t.Fatalf("serve returned before request completed: %v", err)
	default:
	}

	release()
	select {
	case result := <-responseDone:
		if result.err != nil {
			t.Fatalf("request error = %v", result.err)
		}
		if result.status != http.StatusNoContent {
			t.Errorf("response status = %d, want %d", result.status, http.StatusNoContent)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not complete after handler release")
	}

	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not exit after in-flight request completed")
	}
}

func TestMainExitsNonzeroOnConfigurationFailure(t *testing.T) {
	if os.Getenv("GO_WANT_CONFIG_FAILURE_HELPER") == "1" {
		main()
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestMainExitsNonzeroOnConfigurationFailure$")
	command.Env = append(os.Environ(),
		"GO_WANT_CONFIG_FAILURE_HELPER=1",
		"HTTP_ADDR=invalid",
		"SHUTDOWN_TIMEOUT=",
	)
	output, err := command.CombinedOutput()

	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() == 0 {
		t.Fatalf("process error = %v, want nonzero exit; output = %q", err, output)
	}

	var entry map[string]any
	if err := json.Unmarshal(output, &entry); err != nil {
		t.Fatalf("log output = %q, want one JSON object: %v", output, err)
	}
	if entry["level"] != "ERROR" {
		t.Errorf("log level = %v, want ERROR", entry["level"])
	}
}

func TestMainShutsDownOnSignal(t *testing.T) {
	if os.Getenv("GO_WANT_SIGNAL_HELPER") == "1" {
		main()
		return
	}

	for name, shutdownSignal := range map[string]os.Signal{
		"SIGINT":  os.Interrupt,
		"SIGTERM": syscall.SIGTERM,
	} {
		t.Run(name, func(t *testing.T) {
			testMainShutsDownOnSignal(t, shutdownSignal)
		})
	}
}

func testMainShutsDownOnSignal(t *testing.T, shutdownSignal os.Signal) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainShutsDownOnSignal$")
	command.Env = append(os.Environ(),
		"GO_WANT_SIGNAL_HELPER=1",
		"HTTP_ADDR=127.0.0.1:0",
		"SHUTDOWN_TIMEOUT=1s",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	for line := range lines {
		if strings.Contains(line, `"msg":"server starting"`) {
			if err := command.Process.Signal(shutdownSignal); err != nil {
				t.Fatalf("Signal() error = %v", err)
			}
			break
		}
	}

	if err := command.Wait(); err != nil {
		t.Fatalf("process error = %v, want successful shutdown", err)
	}
}
