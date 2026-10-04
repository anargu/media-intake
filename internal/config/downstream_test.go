package config

import (
	"testing"
	"time"
)

func TestDownstreamConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("DOWNSTREAM_URL", "http://localhost:9090/deliver")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownstreamURL != "http://localhost:9090/deliver" || cfg.OutboxPollInterval != 500*time.Millisecond || cfg.OutboxBaseDelay != time.Second || cfg.OutboxMaxDelay != time.Minute || cfg.OutboxMaxAttempts != 8 {
		t.Fatalf("unexpected delivery configuration: %#v", cfg)
	}
}
