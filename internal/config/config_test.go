package config

import (
	"errors"
	"flag"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func validArgs() []string {
	return []string{
		"-headscale.url", "http://127.0.0.1:8080",
		"-headscale.api-key", "test-key",
	}
}

func TestParseWithEnvRequiresURLAndAPIKey(t *testing.T) {
	t.Parallel()

	if _, err := ParseWithEnv(nil, env(nil)); err == nil || !strings.Contains(err.Error(), "headscale URL is required") {
		t.Fatalf("expected URL required error, got %v", err)
	}

	if _, err := ParseWithEnv([]string{"-headscale.url", "http://127.0.0.1:8080"}, env(nil)); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("expected API key required error, got %v", err)
	}
}

func TestParseWithEnvFromEnvironment(t *testing.T) {
	t.Parallel()

	cfg, err := ParseWithEnv(nil, env(map[string]string{
		"HEADSCALE_URL":     "http://headscale:8080/",
		"HEADSCALE_API_KEY": "env-key",
		"LISTEN_ADDRESS":    "127.0.0.1:9999",
		"TELEMETRY_PATH":    "/custom",
		"SCRAPE_TIMEOUT":    "15s",
		"LOG_LEVEL":         "debug",
	}))
	if err != nil {
		t.Fatalf("ParseWithEnv() error = %v", err)
	}

	if cfg.HeadscaleURL != "http://headscale:8080" {
		t.Errorf("HeadscaleURL = %q, want %q", cfg.HeadscaleURL, "http://headscale:8080")
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "env-key")
	}
	if cfg.ListenAddress != "127.0.0.1:9999" {
		t.Errorf("ListenAddress = %q, want %q", cfg.ListenAddress, "127.0.0.1:9999")
	}
	if cfg.TelemetryPath != "/custom" {
		t.Errorf("TelemetryPath = %q, want %q", cfg.TelemetryPath, "/custom")
	}
	if cfg.ScrapeTimeout != 15*time.Second {
		t.Errorf("ScrapeTimeout = %s, want 15s", cfg.ScrapeTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %s, want debug", cfg.LogLevel)
	}
}

func TestParseWithEnvFlagsOverrideEnvironment(t *testing.T) {
	t.Parallel()

	cfg, err := ParseWithEnv([]string{
		"-headscale.url", "https://flag.example",
		"-headscale.api-key", "flag-key",
		"-web.listen-address", ":1234",
		"-web.telemetry-path", "/flag-metrics",
		"-scrape.timeout", "20s",
		"-log.level", "error",
	}, env(map[string]string{
		"HEADSCALE_URL":     "http://env.example:8080",
		"HEADSCALE_API_KEY": "env-key",
		"LISTEN_ADDRESS":    ":9999",
		"TELEMETRY_PATH":    "/env-metrics",
		"SCRAPE_TIMEOUT":    "15s",
		"LOG_LEVEL":         "debug",
	}))
	if err != nil {
		t.Fatalf("ParseWithEnv() error = %v", err)
	}

	if cfg.HeadscaleURL != "https://flag.example" {
		t.Errorf("HeadscaleURL = %q, want flag value", cfg.HeadscaleURL)
	}
	if cfg.APIKey != "flag-key" {
		t.Errorf("APIKey = %q, want flag value", cfg.APIKey)
	}
	if cfg.ListenAddress != ":1234" {
		t.Errorf("ListenAddress = %q, want flag value", cfg.ListenAddress)
	}
	if cfg.TelemetryPath != "/flag-metrics" {
		t.Errorf("TelemetryPath = %q, want flag value", cfg.TelemetryPath)
	}
	if cfg.ScrapeTimeout != 20*time.Second {
		t.Errorf("ScrapeTimeout = %s, want 20s", cfg.ScrapeTimeout)
	}
	if cfg.LogLevel != slog.LevelError {
		t.Errorf("LogLevel = %s, want error", cfg.LogLevel)
	}
}

func TestParseWithEnvVersionDoesNotRequireConfig(t *testing.T) {
	t.Parallel()

	cfg, err := ParseWithEnv([]string{"-version"}, env(nil))
	if err != nil {
		t.Fatalf("ParseWithEnv() error = %v", err)
	}
	if !cfg.ShowVersion {
		t.Fatal("ShowVersion = false, want true")
	}
}

func TestParseWithEnvHelp(t *testing.T) {
	t.Parallel()

	_, err := ParseWithEnv([]string{"-h"}, env(nil))
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("ParseWithEnv() error = %v, want flag.ErrHelp", err)
	}
}

func TestParseWithEnvInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "url without scheme",
			args: []string{"-headscale.url", "localhost:8080", "-headscale.api-key", "key"},
			want: "must use http or https",
		},
		{
			name: "unsupported url scheme",
			args: []string{"-headscale.url", "ftp://localhost:8080", "-headscale.api-key", "key"},
			want: "must use http or https",
		},
		{
			name: "url with api prefix",
			args: []string{"-headscale.url", "http://localhost:8080/api/v1", "-headscale.api-key", "key"},
			want: "must not include /api/v1",
		},
		{
			name: "url with query",
			args: []string{"-headscale.url", "http://localhost:8080?foo=bar", "-headscale.api-key", "key"},
			want: "must not include a query or fragment",
		},
		{
			name: "url with user info",
			args: []string{"-headscale.url", "http://user:pass@localhost:8080", "-headscale.api-key", "key"},
			want: "must not include user info",
		},
		{
			name: "invalid listen address",
			args: append(validArgs(), "-web.listen-address", "9110"),
			want: "invalid listen address",
		},
		{
			name: "telemetry path without slash",
			args: append(validArgs(), "-web.telemetry-path", "metrics"),
			want: "must start with /",
		},
		{
			name: "reserved telemetry path",
			args: append(validArgs(), "-web.telemetry-path", "/"),
			want: "is reserved",
		},
		{
			name: "reserved health path",
			args: append(validArgs(), "-web.telemetry-path", "/healthz"),
			want: "is reserved",
		},
		{
			name: "telemetry path with query",
			args: append(validArgs(), "-web.telemetry-path", "/metrics?foo=bar"),
			want: "must not contain a query or fragment",
		},
		{
			name: "invalid timeout syntax",
			args: append(validArgs(), "-scrape.timeout", "abc"),
			want: "invalid scrape timeout",
		},
		{
			name: "zero timeout",
			args: append(validArgs(), "-scrape.timeout", "0s"),
			want: "scrape timeout must be greater than 0",
		},
		{
			name: "timeout above maximum",
			args: append(validArgs(), "-scrape.timeout", "10m"),
			want: "at most 5m0s",
		},
		{
			name: "invalid log level",
			args: append(validArgs(), "-log.level", "trace"),
			want: "invalid log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWithEnv(tt.args, env(nil))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseWithEnv() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
	}

	for _, tt := range tests {
		got, err := ParseLogLevel(tt.input)
		if err != nil {
			t.Errorf("ParseLogLevel(%q) error = %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseLogLevel(%q) = %s, want %s", tt.input, got, tt.want)
		}
	}

	if _, err := ParseLogLevel("trace"); err == nil {
		t.Fatal("ParseLogLevel(trace) error = nil, want error")
	}
}

func TestUsage(t *testing.T) {
	t.Parallel()

	usage := Usage()
	for _, want := range []string{"headscale.url", "headscale.api-key", "web.listen-address", "web.telemetry-path"} {
		if !strings.Contains(usage, want) {
			t.Errorf("Usage() missing %q", want)
		}
	}
}

func TestUsageDoesNotLeakAPIKey(t *testing.T) {
	t.Setenv("HEADSCALE_API_KEY", "super-secret-key")

	if usage := Usage(); strings.Contains(usage, "super-secret-key") {
		t.Fatal("Usage() leaked the API key from the environment")
	}
}
