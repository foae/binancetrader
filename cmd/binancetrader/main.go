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

	"github.com/foae/binancetrader/exchange"
	"github.com/foae/binancetrader/service"
	"github.com/foae/binancetrader/storage"
	"github.com/go-chi/chi/v5"
	mw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	slogchi "github.com/samber/slog-chi"
	"github.com/shopspring/decimal"

	"github.com/caarlos0/env/v10"
	_ "github.com/joho/godotenv/autoload"
)

type config struct {
	ListenAddress string `env:"HTTP_LISTEN_ADDRESS,required" envDefault:":8123"`
	EnvMode       string `env:"ENV_MODE,required" envDefault:"dev"`
	ServiceName   string `env:"SERVICE_NAME" envDefault:"binancetrader"`
	LogFile       string `env:"LOG_FILE" envDefault:"./binancetrader.log"`

	// Storage (DragonFly/Redis)
	RedisURL string `env:"REDIS_URL,required" envDefault:"redis://localhost:6379/0"`

	// Binance API
	BinanceAPIKey    string `env:"BINANCE_API_KEY,required"`
	BinanceAPISecret string `env:"BINANCE_API_SECRET,required"`
	// "live", "demo" (demo-api.binance.com), or "testnet" (testnet.binance.vision)
	BinanceMode string `env:"BINANCE_MODE" envDefault:"live"`

	// Trading
	EnabledPairs    string `env:"ENABLED_PAIRS,required" envDefault:"BTC/USDT"`
	DryRun          bool   `env:"DRY_RUN" envDefault:"true"`
	BuyOffset       string `env:"BUY_OFFSET" envDefault:"0.001"`
	BuyQuantityUSDT string `env:"BUY_QUANTITY_USDT" envDefault:"5"`
	TakeProfit      string `env:"TAKE_PROFIT" envDefault:"0.01"`
	OrderExpiry     string `env:"ORDER_EXPIRY" envDefault:"1h"`
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

	// Parse and validate enabled pairs
	pairs, err := parsePairs(cfg.EnabledPairs)
	if err != nil {
		log.Fatalf("invalid ENABLED_PAIRS: %v", err)
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

	// Initialize Binance client
	var binanceOpts []exchange.Option
	switch cfg.BinanceMode {
	case "live":
		// production, no options needed
	case "demo":
		binanceOpts = append(binanceOpts, exchange.WithDemo())
	case "testnet":
		binanceOpts = append(binanceOpts, exchange.WithTestnet())
	default:
		log.Fatalf("invalid BINANCE_MODE %q (valid: live, demo, testnet)", cfg.BinanceMode)
	}
	slog.Info("Binance mode", "mode", cfg.BinanceMode)
	binanceClient := exchange.NewClient(cfg.BinanceAPIKey, cfg.BinanceAPISecret, binanceOpts...)

	if err := binanceClient.Ping(ctx); err != nil {
		log.Fatalf("binance API ping failed: %v", err)
	}

	account, err := binanceClient.Account(ctx)
	if err != nil {
		log.Fatalf("binance API authentication failed: %v", err)
	}
	slog.Info("Binance API OK", "can_trade", account.CanTrade)

	// Parse strategy config.
	buyOffset, err := decimal.NewFromString(cfg.BuyOffset)
	if err != nil {
		log.Fatalf("invalid BUY_OFFSET %q: %v", cfg.BuyOffset, err)
	}
	buyQuantityUSDT, err := decimal.NewFromString(cfg.BuyQuantityUSDT)
	if err != nil {
		log.Fatalf("invalid BUY_QUANTITY_USDT %q: %v", cfg.BuyQuantityUSDT, err)
	}
	takeProfit, err := decimal.NewFromString(cfg.TakeProfit)
	if err != nil {
		log.Fatalf("invalid TAKE_PROFIT %q: %v", cfg.TakeProfit, err)
	}
	orderExpiry, err := time.ParseDuration(cfg.OrderExpiry)
	if err != nil {
		log.Fatalf("invalid ORDER_EXPIRY %q: %v", cfg.OrderExpiry, err)
	}

	// Create service
	svc, err := service.New(ctx, binanceClient, storageClient, service.Config{
		Pairs:           pairs,
		DryRun:          cfg.DryRun,
		BuyOffset:       buyOffset,
		BuyQuantityUSDT: buyQuantityUSDT,
		TakeProfit:      takeProfit,
		OrderExpiry:     orderExpiry,
	})
	if err != nil {
		log.Fatalf("failed to initialize service: %v", err)
	}
	defer func() {
		if err := svc.Close(); err != nil {
			slog.Error("Failed to close service", "error", err)
		}
	}()

	slog.Info("Service started", "pairs", pairs)

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

// parsePairs parses and validates a comma-separated list of trading pairs.
// Input format: "BTC/USDT,ETH/USDT" → Output: []PairConfig
func parsePairs(raw string) ([]service.PairConfig, error) {
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool, len(parts))
	pairs := make([]service.PairConfig, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		base, quote, ok := strings.Cut(p, "/")
		if !ok {
			return nil, fmt.Errorf("invalid pair format %q — expected BASE/QUOTE (e.g., BTC/USDT)", p)
		}

		base = strings.TrimSpace(strings.ToUpper(base))
		quote = strings.TrimSpace(strings.ToUpper(quote))

		if base == "" || quote == "" {
			return nil, fmt.Errorf("invalid pair format %q — base and quote must be non-empty", p)
		}

		symbol := base + quote
		if seen[symbol] {
			continue // deduplicate
		}
		seen[symbol] = true
		pairs = append(pairs, service.PairConfig{
			Symbol: symbol,
			Base:   base,
			Quote:  quote,
		})
	}

	if len(pairs) == 0 {
		return nil, fmt.Errorf("at least one pair must be enabled")
	}

	return pairs, nil
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
// If logFilePath is empty or "-", logs only to stdout.
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
