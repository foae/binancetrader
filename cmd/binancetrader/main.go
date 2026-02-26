package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/foae/binancetrader/service"
	"github.com/foae/binancetrader/storage"
	"github.com/go-chi/chi/v5"
	mw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	slogchi "github.com/samber/slog-chi"

	"github.com/caarlos0/env/v10"
	_ "github.com/joho/godotenv/autoload"
)

// validAssets defines the set of supported asset identifiers.
var validAssets = map[string]bool{
	"btc": true,
	"eth": true,
	"xrp": true,
	"sol": true,
}

type config struct {
	ListenAddress string `env:"HTTP_LISTEN_ADDRESS,required" envDefault:":8123"`
	EnvMode       string `env:"ENV_MODE,required" envDefault:"dev"`
	ServiceName   string `env:"SERVICE_NAME" envDefault:"binancetrader"`
	LogFile       string `env:"LOG_FILE" envDefault:"./binancetrader.log"`

	// Storage (DragonFly/Redis)
	RedisURL string `env:"REDIS_URL,required" envDefault:"redis://localhost:6379/0"`

	// Strategy
	EnabledAssets string `env:"ENABLED_ASSETS" envDefault:"btc"`
	DryRun        bool   `env:"DRY_RUN" envDefault:"false"`
}

func main() {
	log.Print("Booting up")

	// Create context that listens for the interrupt signal from the OS
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := &config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("unable to load env vars into config: %v", err)
	}

	logLvl := slog.LevelDebug
	if cfg.EnvMode == "prod" {
		logLvl = slog.LevelInfo
	}

	// Set up logging to both stdout and file
	logFile, logger, err := setupLogger(cfg.LogFile, logLvl)
	if err != nil {
		log.Fatalf("failed to setup logger: %v", err)
	}
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()
	slog.SetDefault(logger)

	// Parse and validate enabled assets
	assets, err := parseAssets(cfg.EnabledAssets)
	if err != nil {
		log.Fatalf("invalid ENABLED_ASSETS: %v", err)
	}

	// Initialize storage client
	storageClient, err := storage.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to create storage client: %v", err)
	}
	defer func() {
		if err := storageClient.Close(); err != nil {
			slog.Error("Failed to close storage client", "error", err)
		}
	}()

	// Create one service per enabled asset.
	services := make([]*service.Service, 0, len(assets))
	for _, asset := range assets {
		svc, err := service.New(ctx, storageClient, service.Config{
			Asset:  asset,
			DryRun: cfg.DryRun,
		})
		if err != nil {
			log.Fatalf("failed to initialize %s service: %v", asset, err)
		}
		services = append(services, svc)
	}
	defer func() {
		for _, svc := range services {
			if err := svc.Close(); err != nil {
				slog.Error("Failed to close service", "error", err)
			}
		}
	}()

	slog.Info("Services started", "assets", assets, "count", len(services))

	r := chi.NewRouter()
	r.Use(
		slogchi.NewWithFilters(logger, slogchi.IgnorePath("/health", "/ready", "/metrics")),
		mw.Recoverer,
		mw.RealIP,
		mw.RedirectSlashes,
		mw.CleanPath,
	)

	r.Get("/health", handleHealth)
	r.Get("/ready", handleReady(storageClient))
	r.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           r,
		MaxHeaderBytes:    1 << 20,          // 1 MB limit on headers
		ReadHeaderTimeout: 10 * time.Second, // Time to read request headers
		ReadTimeout:       30 * time.Second, // Total time to read request
		WriteTimeout:      30 * time.Second, // Total time to write response
		IdleTimeout:       2 * time.Minute,  // Keep-alive connection timeout
	}

	// Start server
	go func() {
		slog.Info("Started server", "addr", cfg.ListenAddress, "runtime", runtime.Version(), "env", cfg.EnvMode)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server with a timeout of 30 seconds
	<-ctx.Done()
	slog.Info("Shutting down server...")

	// Create context with timeout for shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	} else {
		slog.Info("Server shutdown gracefully")
	}
}

// parseAssets parses and validates a comma-separated list of asset identifiers.
func parseAssets(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool, len(parts))
	assets := make([]string, 0, len(parts))

	for _, p := range parts {
		asset := strings.TrimSpace(strings.ToLower(p))
		if asset == "" {
			continue
		}
		if !validAssets[asset] {
			return nil, fmt.Errorf("unknown asset %q (valid: btc, eth, xrp, sol)", asset)
		}
		if seen[asset] {
			continue // deduplicate
		}
		seen[asset] = true
		assets = append(assets, asset)
	}

	if len(assets) == 0 {
		return nil, fmt.Errorf("at least one asset must be enabled")
	}

	return assets, nil
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"health": "ok"}`))
}

func handleReady(db *storage.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"ready": false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ready": true}`))
	}
}

// setupLogger creates a logger that writes to stdout and optionally to a log file.
// If logFilePath is empty or "-", logs only to stdout (useful for Docker).
func setupLogger(logFilePath string, level slog.Level) (*os.File, *slog.Logger, error) {
	var writer io.Writer = os.Stdout
	var logFile *os.File

	if logFilePath != "" && logFilePath != "-" {
		logDir := filepath.Dir(logFilePath)
		if logDir != "." && logDir != "/" {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				return nil, nil, err
			}
		}

		var err error
		logFile, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, nil, err
		}

		writer = io.MultiWriter(os.Stdout, logFile)
		log.Printf("Logging to stdout and file: %s", logFilePath)
	} else {
		log.Printf("Logging to stdout only")
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: level,
	})

	logger := slog.New(handler)

	return logFile, logger, nil
}
