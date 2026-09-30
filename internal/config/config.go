package config

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr        string
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	httpAddr := os.Getenv("HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
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

	shutdownTimeout := 15 * time.Second
	if value := os.Getenv("SHUTDOWN_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
		}
		if parsed <= 0 {
			return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: must be positive")
		}
		shutdownTimeout = parsed
	}

	return Config{
		HTTPAddr:        httpAddr,
		ShutdownTimeout: shutdownTimeout,
	}, nil
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
