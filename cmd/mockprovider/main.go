package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

type provider struct {
	name       string
	apiKey     string
	failStatus int
	delay      time.Duration
}

func main() {
	if os.Getenv("CRASH_ON_START") == "1" {
		fmt.Fprintln(os.Stderr, "CRASH_ON_START=1: exiting to demonstrate CrashLoopBackOff")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	p := provider{
		name:       env("PROVIDER_NAME", "mock-provider"),
		apiKey:     os.Getenv("MOCK_API_KEY"),
		failStatus: envInt("FAIL_STATUS", 0),
		delay:      time.Duration(envInt("DELAY_MS", 0)) * time.Millisecond,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/v1/models", p.models)
	mux.HandleFunc("/v1/chat/completions", p.chat)

	server := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("mock provider started", "provider", p.name, "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("mock provider stopped unexpectedly", "error", err)
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

func health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (p provider) models(w http.ResponseWriter, r *http.Request) {
	if !p.authorize(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{{
			"id":       "mock-model",
			"object":   "model",
			"owned_by": p.name,
		}},
	})
}

func (p provider) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !p.authorize(w, r) {
		return
	}
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	if p.failStatus >= 400 {
		writeError(w, p.failStatus, p.name+" configured failure")
		return
	}

	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(req.Model) == "" || len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "model and messages are required")
		return
	}

	instance, node := identity()
	content := fmt.Sprintf("response from %s instance=%s node=%s", p.name, instance, node)
	if req.Stream {
		p.stream(w, req.Model, content)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       "chatcmpl-" + p.name,
		"object":   "chat.completion",
		"created":  time.Now().Unix(),
		"model":    req.Model,
		"output":   content,
		"instance": instance,
		"node":     node,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{
			"prompt_tokens":     4,
			"completion_tokens": 4,
			"total_tokens":      8,
		},
	})
}

func identity() (instance, node string) {
	instance, err := os.Hostname()
	if err != nil || instance == "" {
		instance = env("POD_NAME", "unknown")
	}
	if pod := os.Getenv("POD_NAME"); pod != "" {
		instance = pod
	}
	node = env("NODE_NAME", "unknown")
	return instance, node
}

func (p provider) stream(w http.ResponseWriter, model, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	chunks := []map[string]any{
		{
			"id": "chatcmpl-" + p.name, "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant"}}},
		},
		{
			"id": "chatcmpl-" + p.name, "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": model,
			"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": content}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 4, "completion_tokens": 4, "total_tokens": 8},
		},
	}
	for _, chunk := range chunks {
		body, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (p provider) authorize(w http.ResponseWriter, r *http.Request) bool {
	if p.apiKey == "" || r.Header.Get("Authorization") == "Bearer "+p.apiKey {
		return true
	}
	writeError(w, http.StatusUnauthorized, "invalid API key")
	return false
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"message": message, "type": "mock_provider_error"},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return fallback
	}
	return value
}
