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

	"github.com/redis/go-redis/v9"

	"cloud-gateway-lab/internal/gateway"
	"cloud-gateway-lab/internal/quota"
	"cloud-gateway-lab/internal/ratelimit"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	routes, err := gateway.ParseRoutes(env("ROUTES", "users=http://localhost:8081,products=http://localhost:8082"))
	if err != nil {
		logger.Error("invalid routes", "error", err)
		os.Exit(1)
	}

	cfg, err := buildConfig(routes, logger)
	if err != nil {
		logger.Error("invalid traffic config", "error", err)
		os.Exit(1)
	}
	handler, err := gateway.New(cfg)
	if err != nil {
		logger.Error("create gateway", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           requestLog(handler, logger),
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("gateway started", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("gateway stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func buildConfig(routes map[string]string, logger *slog.Logger) (gateway.Config, error) {
	limit := envInt("RATE_LIMIT_LIMIT", 100)
	window, err := time.ParseDuration(env("RATE_LIMIT_WINDOW", "1s"))
	if err != nil {
		return gateway.Config{}, err
	}
	if limit < 1 || window <= 0 {
		return gateway.Config{}, errors.New("RATE_LIMIT_LIMIT and RATE_LIMIT_WINDOW must be positive")
	}

	cfg := gateway.Config{Routes: routes, Logger: logger}
	backend := env("RATE_LIMIT_BACKEND", "memory")
	quotaDefault := envInt("QUOTA_DEFAULT", 0)

	switch backend {
	case "memory":
		cfg.Limiter = ratelimit.NewMemory(limit, window)
		if quotaDefault > 0 {
			cfg.Quota = quota.NewMemory(int64(quotaDefault))
		}
	case "redis":
		rdb := redis.NewClient(&redis.Options{
			Addr:         env("REDIS_ADDR", "127.0.0.1:6379"),
			DialTimeout:  500 * time.Millisecond,
			ReadTimeout:  200 * time.Millisecond,
			WriteTimeout: 200 * time.Millisecond,
			PoolSize:     20,
		})
		cfg.Limiter = ratelimit.NewRedis(rdb, limit, window)
		cfg.Ready = func(ctx context.Context) error { return rdb.Ping(ctx).Err() }
		if quotaDefault > 0 {
			cfg.Quota = quota.NewRedis(rdb, int64(quotaDefault))
		}
	default:
		return gateway.Config{}, errors.New("RATE_LIMIT_BACKEND must be memory or redis")
	}

	logger.Info("traffic control ready",
		"backend", backend,
		"rate_limit", limit,
		"window", window.String(),
		"quota_default", quotaDefault,
	)
	return cfg, nil
}

func requestLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
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
