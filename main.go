package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	liblogger "github.com/hibare/headscale-client-go/logger"
	"github.com/hibare/headscale-client-go/utils"
	hsClient "github.com/hibare/headscale-client-go/v1/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TuF3i/headscale-exporter/internal/collector"
	"github.com/TuF3i/headscale-exporter/internal/config"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const landingPage = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <title>Headscale Exporter</title>
</head>
<body>
  <h1>Headscale Exporter</h1>
  <p>Version: %s</p>
  <p><a href="%s">Metrics</a></p>
</body>
</html>
`

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(config.Usage())
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s", err, config.Usage())
		os.Exit(1)
	}

	if cfg.ShowVersion {
		fmt.Printf("headscale-exporter %s (commit %s, built %s, %s)\n", version, commit, date, runtime.Version())
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	apiClient, err := hsClient.NewClient(cfg.HeadscaleURL, cfg.APIKey, hsClient.ClientOptions{
		HTTPClient: &http.Client{Timeout: cfg.ScrapeTimeout},
		UserAgent:  utils.ToPtr("headscale-exporter/" + version),
		LogLevel:   utils.ToPtr(libraryLogLevel(cfg.LogLevel)),
	})
	if err != nil {
		logger.Error("failed to create Headscale client", "error", err)
		os.Exit(1)
	}

	prometheus.MustRegister(collector.New(apiClient, cfg.ScrapeTimeout, logger, collector.BuildInfo{
		Version: version,
		Commit:  commit,
	}))

	mux := http.NewServeMux()
	mux.Handle(cfg.TelemetryPath, promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, landingPage, html.EscapeString(version), html.EscapeString(cfg.TelemetryPath))
	})

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting headscale-exporter",
			"listen_address", cfg.ListenAddress,
			"telemetry_path", cfg.TelemetryPath,
			"headscale_url", cfg.HeadscaleURL,
			"version", version,
		)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down headscale-exporter")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}
}

// The client library logs every API call at info level. Keep it quiet unless
// debug logging was explicitly requested.
func libraryLogLevel(level slog.Level) liblogger.LogLevel {
	switch {
	case level <= slog.LevelDebug:
		return liblogger.LevelDebug
	case level <= slog.LevelWarn:
		return liblogger.LevelWarn
	default:
		return liblogger.LevelError
	}
}
