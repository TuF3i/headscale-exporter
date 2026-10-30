// Package config parses and validates the exporter configuration.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultListenAddress = ":9110"
	defaultTelemetryPath = "/metrics"
	defaultScrapeTimeout = 10 * time.Second
	defaultLogLevel      = "info"
	maxScrapeTimeout     = 5 * time.Minute
)

// Config contains the validated runtime configuration.
type Config struct {
	HeadscaleURL  string
	APIKey        string
	ListenAddress string
	TelemetryPath string
	ScrapeTimeout time.Duration
	LogLevel      slog.Level
	ShowVersion   bool
}

type flags struct {
	headscaleURL  *string
	apiKey        *string
	listenAddress *string
	telemetryPath *string
	scrapeTimeout *string
	logLevel      *string
	showVersion   *bool
}

// Parse reads configuration from args and the process environment.
func Parse(args []string) (*Config, error) {
	return ParseWithEnv(args, os.Getenv)
}

// ParseWithEnv reads configuration from args and the supplied environment getter.
// Command-line flags take precedence over environment variables.
func ParseWithEnv(args []string, getenv func(string) string) (*Config, error) {
	fs, f := newFlagSet(getenv, io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		HeadscaleURL:  strings.TrimSpace(*f.headscaleURL),
		APIKey:        strings.TrimSpace(*f.apiKey),
		ListenAddress: strings.TrimSpace(*f.listenAddress),
		TelemetryPath: strings.TrimSpace(*f.telemetryPath),
		ShowVersion:   *f.showVersion,
	}
	if cfg.APIKey == "" {
		// Keep the key out of flag defaults so it is never printed by --help.
		cfg.APIKey = strings.TrimSpace(getenv("HEADSCALE_API_KEY"))
	}

	if cfg.ShowVersion {
		return cfg, nil
	}

	if cfg.HeadscaleURL == "" {
		return nil, errors.New("headscale URL is required (set --headscale.url or HEADSCALE_URL)")
	}
	if err := validateHeadscaleURL(cfg.HeadscaleURL); err != nil {
		return nil, err
	}
	cfg.HeadscaleURL = strings.TrimRight(cfg.HeadscaleURL, "/")

	if cfg.APIKey == "" {
		return nil, errors.New("headscale API key is required (set --headscale.api-key or HEADSCALE_API_KEY)")
	}

	if cfg.ListenAddress == "" {
		return nil, errors.New("listen address must not be empty")
	}
	if _, _, err := net.SplitHostPort(cfg.ListenAddress); err != nil {
		return nil, fmt.Errorf("invalid listen address %q: %w", cfg.ListenAddress, err)
	}

	if err := validateTelemetryPath(cfg.TelemetryPath); err != nil {
		return nil, err
	}

	timeout, err := time.ParseDuration(*f.scrapeTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid scrape timeout %q: %w", *f.scrapeTimeout, err)
	}
	if timeout <= 0 || timeout > maxScrapeTimeout {
		return nil, fmt.Errorf("scrape timeout must be greater than 0 and at most %s", maxScrapeTimeout)
	}
	cfg.ScrapeTimeout = timeout

	level, err := ParseLogLevel(*f.logLevel)
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = level

	return cfg, nil
}

// Usage returns the command-line help text.
func Usage() string {
	var b strings.Builder
	b.WriteString("Usage: headscale-exporter [flags]\n\nFlags:\n")
	fs, _ := newFlagSet(os.Getenv, &b)
	fs.PrintDefaults()
	return b.String()
}

func newFlagSet(getenv func(string) string, out io.Writer) (*flag.FlagSet, *flags) {
	fs := flag.NewFlagSet("headscale-exporter", flag.ContinueOnError)
	fs.SetOutput(out)

	f := &flags{
		headscaleURL:  fs.String("headscale.url", getenv("HEADSCALE_URL"), "Headscale base URL without /api/v1, e.g. http://127.0.0.1:8080 (env HEADSCALE_URL)"),
		apiKey:        fs.String("headscale.api-key", "", "Headscale API key (env HEADSCALE_API_KEY)"),
		listenAddress: fs.String("web.listen-address", envOrDefault(getenv, "LISTEN_ADDRESS", defaultListenAddress), "Address to listen on for the exporter endpoints (env LISTEN_ADDRESS)"),
		telemetryPath: fs.String("web.telemetry-path", envOrDefault(getenv, "TELEMETRY_PATH", defaultTelemetryPath), "Path under which to expose Prometheus metrics (env TELEMETRY_PATH)"),
		scrapeTimeout: fs.String("scrape.timeout", envOrDefault(getenv, "SCRAPE_TIMEOUT", defaultScrapeTimeout.String()), "Timeout for a single Headscale API scrape (env SCRAPE_TIMEOUT)"),
		logLevel:      fs.String("log.level", envOrDefault(getenv, "LOG_LEVEL", defaultLogLevel), "Log level: debug, info, warn, error (env LOG_LEVEL)"),
		showVersion:   fs.Bool("version", false, "Print version information and exit"),
	}

	return fs, f
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}

func validateHeadscaleURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid headscale URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("headscale URL must use http or https, got %q", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("headscale URL must include a host, got %q", raw)
	}
	if u.User != nil {
		return fmt.Errorf("headscale URL must not include user info, got %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("headscale URL must not include a query or fragment, got %q", raw)
	}
	if strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/api/v1") {
		return fmt.Errorf("headscale URL must not include /api/v1, got %q", raw)
	}
	return nil
}

func validateTelemetryPath(path string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("telemetry path must start with /, got %q", path)
	}
	if strings.ContainsAny(path, "?#") {
		return fmt.Errorf("telemetry path must not contain a query or fragment, got %q", path)
	}
	if path == "/" || path == "/healthz" {
		return fmt.Errorf("telemetry path %q is reserved", path)
	}
	return nil
}

// ParseLogLevel parses a textual log level into a slog level.
func ParseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: expected debug, info, warn or error", value)
	}
}
