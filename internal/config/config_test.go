package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsHTTPAddr(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
}

func TestLoadDefaultsShutdownTimeout(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 15*time.Second)
	}
}

func TestLoadUsesShutdownTimeout(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "2m30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ShutdownTimeout != 2*time.Minute+30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 2*time.Minute+30*time.Second)
	}
}

func TestLoadRejectsInvalidShutdownTimeout(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", "")
			t.Setenv("SHUTDOWN_TIMEOUT", value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want invalid SHUTDOWN_TIMEOUT error")
			}
			if !strings.Contains(err.Error(), "SHUTDOWN_TIMEOUT") {
				t.Errorf("Load() error = %q, want it to identify SHUTDOWN_TIMEOUT", err)
			}
		})
	}
}

func TestLoadRejectsInvalidHTTPAddr(t *testing.T) {
	t.Setenv("HTTP_ADDR", "localhost")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid HTTP_ADDR error")
	}
	if !strings.Contains(err.Error(), "HTTP_ADDR") {
		t.Errorf("Load() error = %q, want it to identify HTTP_ADDR", err)
	}
}
