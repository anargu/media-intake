package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr           = ":8080"
	defaultCaptureStorageDir  = "/data/captures"
	defaultMaxFrameBytes      = 10 * 1024 * 1024 // 10 MB
	defaultMaxManifestBytes   = 64 * 1024        // 64 KB
	defaultMaxBodyBytes       = 11 * 1024 * 1024 // 11 MB
	defaultRequestTimeout     = 30 * time.Second
	defaultShutdownTimeout    = 15 * time.Second
	defaultDownstreamTimeout  = 5 * time.Second
	defaultOutboxOperationTTL = 10 * time.Second
	defaultOutboxPollInterval = 500 * time.Millisecond
	defaultOutboxBaseDelay    = time.Second
	defaultOutboxMaxDelay     = time.Minute
	defaultOutboxMaxAttempts  = 8
	defaultLogLevel           = "info"
)

type Config struct {
	DatabaseURL            string
	HTTPAddr               string
	ShutdownTimeout        time.Duration
	CaptureStorageDir      string
	MaxFrameBytes          int64
	MaxManifestBytes       int64
	MaxBodyBytes           int64
	RequestTimeout         time.Duration
	DownstreamTimeout      time.Duration
	OutboxOperationTimeout time.Duration
	OutboxPollInterval     time.Duration
	OutboxBaseDelay        time.Duration
	OutboxMaxDelay         time.Duration
	OutboxMaxAttempts      int
	LogLevel               string
}

func Load() (Config, error) {
	databaseURL, err := requiredPostgresURL("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	httpAddr := envOrDefault("HTTP_ADDR", defaultHTTPAddr)
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR: %w", err)
	}
	if !validTCPHost(host) {
		return Config{}, fmt.Errorf("HTTP_ADDR: invalid TCP host %q", host)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR: invalid TCP port %q", port)
	}

	shutdownTimeout, err := durationFromEnv("SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := durationFromEnv("REQUEST_TIMEOUT", defaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}
	downstreamTimeout, err := durationFromEnv("DOWNSTREAM_TIMEOUT", defaultDownstreamTimeout)
	if err != nil {
		return Config{}, err
	}
	outboxOperationTimeout, err := durationFromEnv("OUTBOX_OPERATION_TIMEOUT", defaultOutboxOperationTTL)
	if err != nil {
		return Config{}, err
	}
	logLevel := envOrDefault("LOG_LEVEL", defaultLogLevel)
	if !validLogLevel(logLevel) {
		return Config{}, fmt.Errorf("LOG_LEVEL: unsupported log level %q", logLevel)
	}

	return Config{
		DatabaseURL:            databaseURL,
		HTTPAddr:               httpAddr,
		ShutdownTimeout:        shutdownTimeout,
		CaptureStorageDir:      envOrDefault("CAPTURE_STORAGE_DIR", defaultCaptureStorageDir),
		MaxFrameBytes:          defaultMaxFrameBytes,
		MaxManifestBytes:       defaultMaxManifestBytes,
		MaxBodyBytes:           defaultMaxBodyBytes,
		RequestTimeout:         requestTimeout,
		DownstreamTimeout:      downstreamTimeout,
		OutboxOperationTimeout: outboxOperationTimeout,
		OutboxPollInterval:     defaultOutboxPollInterval,
		OutboxBaseDelay:        defaultOutboxBaseDelay,
		OutboxMaxDelay:         defaultOutboxMaxDelay,
		OutboxMaxAttempts:      defaultOutboxMaxAttempts,
		LogLevel:               logLevel,
	}, nil
}

func requiredPostgresURL(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s: required", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return "", fmt.Errorf("%s: must be a usable PostgreSQL URL", name)
	}
	return value, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration", name)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s: must be positive", name)
	}
	return parsed, nil
}

func validLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validTCPHost(host string) bool {
	if host == "" {
		return true
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) > 253 {
		return false
	}

	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || !alphaNumeric(label[0]) || !alphaNumeric(label[len(label)-1]) {
			return false
		}
		for index := 1; index < len(label)-1; index++ {
			if !alphaNumeric(label[index]) && label[index] != '-' {
				return false
			}
		}
	}
	return true
}

func alphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}
