package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/pkoukk/tiktoken-go"
	"github.com/redis/go-redis/v9"

	"cloud-gateway-lab/internal/aigateway"
	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/breaker"
	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/health"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/provider/openai"
	"cloud-gateway-lab/internal/retry"
	"cloud-gateway-lab/internal/store"
	"cloud-gateway-lab/pkg/limiter"
	"cloud-gateway-lab/pkg/prefixcache"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	rdb := redis.NewClient(&redis.Options{
		Addr:         env("REDIS_ADDR", "127.0.0.1:6379"),
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
		PoolSize:     32,
	})
	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		cancel()
		logger.Error("redis unavailable", "error", err)
		os.Exit(1)
	}
	cancel()

	endpoints, err := loadEndpoints()
	if err != nil {
		logger.Error("load endpoints", "error", err)
		os.Exit(1)
	}

	var mysqlStore *store.MySQL
	if dsn := os.Getenv("MYSQL_DSN"); dsn != "" {
		mysqlStore, err = store.OpenMySQL(dsn)
		if err != nil {
			logger.Error("mysql unavailable", "error", err)
			os.Exit(1)
		}
		defer mysqlStore.Close()
		if rows, listErr := mysqlStore.ListEndpoints(context.Background()); listErr != nil {
			logger.Error("list endpoints from mysql", "error", listErr)
			os.Exit(1)
		} else if len(rows) > 0 {
			endpoints = rows
			logger.Info("loaded endpoints from mysql", "count", len(rows))
		}
	}

	pool, err := endpoint.NewPool(endpoints, breaker.Config{
		FailureThreshold: envInt("BREAKER_THRESHOLD", 5),
		Cooldown:         envDuration("BREAKER_COOLDOWN", 10*time.Second),
	})
	if err != nil {
		logger.Error("create endpoint pool", "error", err)
		os.Exit(1)
	}

	authenticator, err := buildAuth(rdb, mysqlStore, logger)
	if err != nil {
		logger.Error("create authenticator", "error", err)
		os.Exit(1)
	}

	window, err := time.ParseDuration(env("RATE_LIMIT_WINDOW", "1s"))
	if err != nil {
		logger.Error("invalid RATE_LIMIT_WINDOW", "error", err)
		os.Exit(1)
	}
	lim, err := limiter.New(rdb, limiter.Config{
		Limit:          int64(envInt("RATE_LIMIT", 20)),
		Window:         window,
		DefaultBalance: int64(envInt("DEFAULT_BALANCE", 100_000)),
	})
	if err != nil {
		logger.Error("create limiter", "error", err)
		os.Exit(1)
	}

	reg := provider.NewRegistry()
	openai.Register(reg)

	index := prefixcache.New(envInt("PREFIX_MIN_MATCH", 16), 0.2)
	for _, prefix := range []string{
		"You are a helpful assistant",
		"You are a helpful AI assistant",
		"You are an expert",
	} {
		index.Insert(prefix)
	}

	var tokenizer aigateway.Tokenizer
	if encoder, encErr := tiktoken.GetEncoding("cl100k_base"); encErr != nil {
		logger.Warn("tiktoken unavailable, falling back to char/4 estimate", "error", encErr)
	} else {
		tokenizer = tiktokenCounter{encoder: encoder}
	}

	handler, err := aigateway.New(aigateway.Config{
		Logger:            logger,
		Auth:              authenticator,
		Limiter:           lim,
		Pool:              pool,
		Providers:         reg,
		Prefix:            index,
		Tokenizer:         tokenizer,
		Ready:             func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		CompletionReserve: int64(envInt("COMPLETION_RESERVE", 256)),
		Retry: retry.Config{
			MaxAttempts: envInt("RETRY_MAX_ATTEMPTS", 3),
			BaseDelay:   envDuration("RETRY_BASE_DELAY", 100*time.Millisecond),
		},
	})
	if err != nil {
		logger.Error("create gateway", "error", err)
		os.Exit(1)
	}

	checker := health.New(pool, envDuration("HEALTH_INTERVAL", 10*time.Second), envDuration("HEALTH_TIMEOUT", 2*time.Second), logger)
	checker.Start()

	refreshStop := make(chan struct{})
	if mysqlStore != nil {
		go refreshEndpoints(mysqlStore, pool, logger, refreshStop)
	}

	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("ai gateway started",
			"address", server.Addr,
			"endpoints", len(endpoints),
			"redis", rdb.Options().Addr,
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("gateway stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	close(refreshStop)
	checker.Stop()
	ctx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func buildAuth(rdb *redis.Client, mysqlStore *store.MySQL, logger *slog.Logger) (*auth.Authenticator, error) {
	if mysqlStore != nil {
		cached := store.NewCachedKeys(mysqlStore, rdb, 30*time.Minute, 5*time.Minute)
		logger.Info("auth backend", "store", "mysql+redis")
		return auth.New(cached), nil
	}
	if raw := os.Getenv("GATEWAY_API_KEYS"); raw != "" {
		mem, err := auth.ParseKeyList(raw)
		if err != nil {
			return nil, err
		}
		cached := store.NewCachedKeys(store.NewMemory(mem, nil), rdb, 30*time.Minute, 5*time.Minute)
		logger.Info("auth backend", "store", "memory+redis", "keys", mem.Len())
		return auth.New(cached), nil
	}
	logger.Warn("GATEWAY_API_KEYS unset; accepting any Bearer token as user id")
	return auth.NewPassthrough(), nil
}

func loadEndpoints() ([]endpoint.Endpoint, error) {
	if path := os.Getenv("ENDPOINTS_FILE"); path != "" {
		return endpoint.LoadYAML(path)
	}
	if _, err := os.Stat("config/endpoints.yaml"); err == nil {
		return endpoint.LoadYAML("config/endpoints.yaml")
	}
	upstream := env("UPSTREAM", "http://localhost:11434/v1")
	model := env("DEFAULT_MODEL", "gpt-5")
	return []endpoint.Endpoint{
		endpoint.Single("default", model, upstream, os.Getenv("UPSTREAM_API_KEY")),
	}, nil
}

func refreshEndpoints(src store.EndpointStore, pool *endpoint.Pool, logger *slog.Logger, stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			rows, err := src.ListEndpoints(context.Background())
			if err != nil {
				logger.Error("refresh endpoints", "error", err)
				continue
			}
			if len(rows) == 0 {
				continue
			}
			if err := pool.Replace(rows); err != nil {
				logger.Error("replace endpoints", "error", err)
			}
		}
	}
}

type tiktokenCounter struct {
	encoder *tiktoken.Tiktoken
}

func (t tiktokenCounter) Count(text string) int64 {
	if text == "" {
		return 0
	}
	return int64(len(t.encoder.Encode(text, nil, nil)))
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}
