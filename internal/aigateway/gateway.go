package aigateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cloud-gateway-lab/internal/auth"
	"cloud-gateway-lab/internal/endpoint"
	"cloud-gateway-lab/internal/provider"
	"cloud-gateway-lab/internal/retry"
	"cloud-gateway-lab/internal/types"
	"cloud-gateway-lab/pkg/limiter"
	"cloud-gateway-lab/pkg/prefixcache"
)

const (
	prefixCacheHeader = "X-Prefix-Cache-Hit"
	requestIDHeader   = "X-Request-ID"
)

type Tokenizer interface {
	Count(text string) int64
}

type Config struct {
	Logger            *slog.Logger
	Auth              *auth.Authenticator
	Limiter           *limiter.Limiter
	Pool              *endpoint.Pool
	Providers         *provider.Registry
	Prefix            *prefixcache.PrefixIndexer
	Tokenizer         Tokenizer
	Ready             func(context.Context) error
	CompletionReserve int64
	Retry             retry.Config
}

type Gateway struct {
	logger            *slog.Logger
	auth              *auth.Authenticator
	limiter           *limiter.Limiter
	pool              *endpoint.Pool
	providers         *provider.Registry
	prefix            *prefixcache.PrefixIndexer
	tokenizer         Tokenizer
	ready             func(context.Context) error
	completionReserve int64
	retry             retry.Config
	metrics           *metrics
}

func New(cfg Config) (*Gateway, error) {
	if cfg.Pool == nil {
		return nil, errors.New("endpoint pool is required")
	}
	if cfg.Providers == nil {
		return nil, errors.New("provider registry is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.CompletionReserve < 1 {
		cfg.CompletionReserve = 256
	}
	if cfg.Retry.MaxAttempts < 1 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.BaseDelay <= 0 {
		cfg.Retry.BaseDelay = 100 * time.Millisecond
	}
	return &Gateway{
		logger:            cfg.Logger,
		auth:              cfg.Auth,
		limiter:           cfg.Limiter,
		pool:              cfg.Pool,
		providers:         cfg.Providers,
		prefix:            cfg.Prefix,
		tokenizer:         cfg.Tokenizer,
		ready:             cfg.Ready,
		completionReserve: cfg.CompletionReserve,
		retry:             cfg.Retry,
		metrics:           newMetrics(),
	}, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/readyz":
		if g.ready != nil {
			if err := g.ready(r.Context()); err != nil {
				g.logger.Error("readiness check failed", "error", err)
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/metrics":
		g.metrics.write(w, g.pool)
		return
	}

	if r.URL.Path != "/v1/chat/completions" {
		writeAPIError(w, http.StatusNotFound, "not found", "invalid_request_error", "")
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "")
		return
	}

	started := time.Now()
	requestID := incomingRequestID(r)
	w.Header().Set(requestIDHeader, requestID)
	sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}

	user, err := g.authenticate(r)
	if err != nil {
		g.metrics.authRejected.Add(1)
		g.logRequest(requestID, "", "", "", sw.code, started, err)
		writeAuthError(sw, err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeAPIError(sw, http.StatusBadRequest, "read body", "invalid_request_error", "")
		return
	}
	req, err := types.ParseChatRequest(body)
	if err != nil {
		writeAPIError(sw, http.StatusBadRequest, err.Error(), "invalid_request_error", "")
		return
	}

	g.maybePrefixCache(sw, r, req)

	preDeducted := g.estimateTokens(req)
	if g.limiter != nil {
		ok, err := g.limiter.PreDeduct(r.Context(), user.ID, preDeducted)
		if err != nil || !ok {
			g.writeLimiterError(sw, err)
			g.logRequest(requestID, user.ID, req.Model, "", sw.code, started, err)
			return
		}
	}

	usage, ferr := g.forward(r.Context(), sw, req, user, requestID)
	actual := int64(0)
	if usage != nil {
		actual = usage.TotalTokens
		if actual == 0 {
			actual = usage.PromptTokens + usage.CompletionTokens
		}
		if actual > 0 {
			g.metrics.tokenUsage.Add(uint64(actual))
		}
	}
	if g.limiter != nil {
		if settleErr := g.limiter.SettleQuota(context.Background(), user.ID, preDeducted, actual); settleErr != nil {
			g.logger.Error("settle quota", "request_id", requestID, "user_id", user.ID, "error", settleErr)
		}
	}
	g.metrics.observe(time.Since(started), ferr != nil)
	g.logRequest(requestID, user.ID, req.Model, "", sw.code, started, ferr)
}

func (g *Gateway) authenticate(r *http.Request) (auth.User, error) {
	if g.auth == nil {
		return auth.User{ID: "anonymous", Status: "active"}, nil
	}
	return g.auth.Lookup(r.Context(), r.Header.Get("Authorization"))
}

func (g *Gateway) forward(ctx context.Context, w http.ResponseWriter, req *types.ChatRequest, user auth.User, requestID string) (*types.Usage, error) {
	exclude := map[string]struct{}{}
	var lastErr error
	attempts := g.retry.MaxAttempts

	for attempt := 0; attempt < attempts; attempt++ {
		ep, err := g.pool.Pick(req.Model, exclude)
		if err != nil {
			if lastErr != nil {
				writeAPIError(w, http.StatusBadGateway, lastErr.Error(), "api_error", "")
				return nil, lastErr
			}
			writeAPIError(w, http.StatusBadRequest, "unknown model or no healthy endpoint", "invalid_request_error", "")
			return nil, err
		}

		adapter, err := g.providers.For(ep)
		if err != nil {
			exclude[ep.ID] = struct{}{}
			lastErr = err
			continue
		}

		upstream := withUpstreamModel(req, ep)
		callStart := time.Now()
		var usage *types.Usage
		if req.Stream {
			usage, err = adapter.ChatStream(ctx, upstream, w)
		} else {
			var resp *types.ChatResponse
			resp, err = adapter.Chat(ctx, upstream)
			if err == nil {
				resp.Model = req.Model
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(types.EncodeChatCompletion(resp))
				u := resp.Usage
				usage = &u
			}
		}
		latency := time.Since(callStart)

		result := endpoint.Result{Latency: latency}
		if err == nil {
			result.Success = true
			g.pool.Report(ep.ID, result)
			g.logger.Info("provider call",
				"request_id", requestID,
				"user_id", user.ID,
				"model", req.Model,
				"provider", ep.Provider,
				"endpoint", ep.ID,
				"latency_ms", latency.Milliseconds(),
				"stream", req.Stream,
			)
			return usage, nil
		}

		lastErr = err
		action := retry.Classify(err)
		var pe *provider.Error
		if errors.As(err, &pe) {
			result.StatusCode = pe.StatusCode
			result.Retryable = pe.Retryable()
			g.metrics.providerError(ep.ID)
		} else {
			result.Retryable = action == retry.Retry || action == retry.RetryOther
			g.metrics.providerError(ep.ID)
		}
		g.pool.Report(ep.ID, result)
		g.logger.Warn("provider call failed",
			"request_id", requestID,
			"user_id", user.ID,
			"model", req.Model,
			"provider", ep.Provider,
			"endpoint", ep.ID,
			"error", err,
			"latency_ms", latency.Milliseconds(),
		)

		if action == retry.Fail || ctx.Err() != nil {
			writeProviderError(w, err)
			return usage, err
		}

		exclude[ep.ID] = struct{}{}
		delay := retry.Backoff(g.retry, attempt)
		if action == retry.RetryOther {
			if ra := retry.RetryAfter(err); ra > 0 && len(exclude) >= attempts {
				delay = ra
			} else {
				delay = 0
			}
		}
		if err := retry.Sleep(ctx, delay); err != nil {
			writeProviderError(w, lastErr)
			return usage, lastErr
		}
	}

	if lastErr != nil {
		writeProviderError(w, lastErr)
		return nil, lastErr
	}
	writeAPIError(w, http.StatusBadGateway, "all endpoints failed", "api_error", "")
	return nil, errors.New("all endpoints failed")
}

func (g *Gateway) maybePrefixCache(w http.ResponseWriter, r *http.Request, req *types.ChatRequest) {
	if g.prefix == nil {
		return
	}
	prompt := types.PromptText(req.Messages)
	if prompt == "" {
		return
	}
	if hit, matchedLen := g.prefix.MatchPrefix(prompt); hit {
		w.Header().Set(prefixCacheHeader, "true")
		r.Header.Set(prefixCacheHeader, "true")
		g.logger.Info("prefix cache hit", "matched_len", matchedLen, "prompt_len", len(prompt))
	}
	if sys := systemPrefix(prompt); sys != "" {
		g.prefix.Insert(sys)
	}
}

func (g *Gateway) estimateTokens(req *types.ChatRequest) int64 {
	prompt := types.PromptText(req.Messages)
	n := int64(0)
	if g.tokenizer != nil {
		n = g.tokenizer.Count(prompt)
	} else {
		n = int64((len(prompt) + 3) / 4)
	}
	if req.MaxTokens > 0 {
		n += int64(req.MaxTokens)
	} else {
		n += g.completionReserve
	}
	if n < 1 {
		return 1
	}
	return n
}

func (g *Gateway) writeLimiterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, limiter.ErrRateLimitExceeded):
		g.metrics.rateLimited.Add(1)
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "rate limited", "rate_limit_error", "rate_limit_exceeded")
	case errors.Is(err, limiter.ErrInsufficientBalance):
		g.metrics.quotaRejected.Add(1)
		writeAPIError(w, http.StatusPaymentRequired, "insufficient balance", "insufficient_quota", "insufficient_quota")
	default:
		g.logger.Error("prededuct failed", "error", err)
		writeAPIError(w, http.StatusServiceUnavailable, "limiter unavailable", "api_error", "")
	}
}

func (g *Gateway) logRequest(requestID, userID, model, ep string, status int, started time.Time, err error) {
	args := []any{
		"request_id", requestID,
		"user_id", userID,
		"model", model,
		"endpoint", ep,
		"status", status,
		"latency_ms", time.Since(started).Milliseconds(),
	}
	if err != nil {
		args = append(args, "error", err.Error())
		g.logger.Info("request", args...)
		return
	}
	g.logger.Info("request", args...)
}

func withUpstreamModel(req *types.ChatRequest, ep endpoint.Endpoint) *types.ChatRequest {
	cp := *req
	cp.Model = ep.UpstreamModel(req.Model)
	return &cp
}

func incomingRequestID(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get(requestIDHeader)); id != "" && len(id) <= 64 {
		return id
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func writeAPIError(w http.ResponseWriter, status int, message, typ, code string) {
	if sw, ok := w.(*statusWriter); ok && sw.wrote {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(types.EncodeAPIError(message, typ, code))
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrMissing):
		writeAPIError(w, http.StatusUnauthorized, "missing api key", "invalid_request_error", "invalid_api_key")
	case errors.Is(err, auth.ErrDisabled):
		writeAPIError(w, http.StatusUnauthorized, "api key disabled", "invalid_request_error", "invalid_api_key")
	case errors.Is(err, auth.ErrInvalid):
		writeAPIError(w, http.StatusUnauthorized, "invalid api key", "invalid_request_error", "invalid_api_key")
	default:
		writeAPIError(w, http.StatusServiceUnavailable, "auth unavailable", "api_error", "")
	}
}

func writeProviderError(w http.ResponseWriter, err error) {
	if sw, ok := w.(*statusWriter); ok && sw.wrote {
		return
	}
	status := http.StatusBadGateway
	msg := err.Error()
	var pe *provider.Error
	if errors.As(err, &pe) && pe.StatusCode > 0 {
		status = pe.StatusCode
		msg = pe.Message
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
		msg = "request timeout"
	}
	writeAPIError(w, status, msg, "api_error", "")
}

func systemPrefix(prompt string) string {
	const maxPrefix = 128
	runes := []rune(strings.TrimSpace(prompt))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) > maxPrefix {
		return string(runes[:maxPrefix])
	}
	return string(runes)
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.code = code
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type metrics struct {
	requests      atomic.Uint64
	errors        atomic.Uint64
	rateLimited   atomic.Uint64
	quotaRejected atomic.Uint64
	authRejected  atomic.Uint64
	latencyMS     atomic.Uint64
	tokenUsage    atomic.Uint64
	providerErrs  *atomicMap
}

func newMetrics() *metrics {
	return &metrics{providerErrs: newAtomicMap()}
}

func (m *metrics) observe(d time.Duration, failed bool) {
	m.requests.Add(1)
	m.latencyMS.Add(uint64(d.Milliseconds()))
	if failed {
		m.errors.Add(1)
	}
}

func (m *metrics) providerError(endpointID string) {
	m.providerErrs.add(endpointID, 1)
}

func (m *metrics) write(w http.ResponseWriter, pool *endpoint.Pool) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w,
		"gateway_requests_total %d\n"+
			"gateway_errors_total %d\n"+
			"gateway_rate_limited_total %d\n"+
			"gateway_quota_rejected_total %d\n"+
			"gateway_auth_rejected_total %d\n"+
			"gateway_request_latency_ms_sum %d\n"+
			"gateway_token_usage_total %d\n",
		m.requests.Load(),
		m.errors.Load(),
		m.rateLimited.Load(),
		m.quotaRejected.Load(),
		m.authRejected.Load(),
		m.latencyMS.Load(),
		m.tokenUsage.Load(),
	)
	for id, n := range m.providerErrs.snapshot() {
		_, _ = fmt.Fprintf(w, "gateway_provider_errors_total{endpoint=%q} %d\n", id, n)
	}
	if pool == nil {
		return
	}
	for _, st := range pool.Snapshot() {
		healthy := 0
		if st.Health == endpoint.Healthy {
			healthy = 1
		}
		_, _ = fmt.Fprintf(w, "gateway_endpoint_healthy{endpoint=%q} %d\n", st.Endpoint.ID, healthy)
		_, _ = fmt.Fprintf(w, "gateway_circuit_breaker_state{endpoint=%q} %d\n", st.Endpoint.ID, int(st.Breaker))
		_, _ = fmt.Fprintf(w, "gateway_endpoint_latency_ms{endpoint=%q} %d\n", st.Endpoint.ID, st.LatencyMS)
	}
}

type atomicMap struct {
	mu     sync.Mutex
	values map[string]uint64
}

func newAtomicMap() *atomicMap {
	return &atomicMap{values: make(map[string]uint64)}
}

func (a *atomicMap) add(key string, n uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.values[key] += n
}

func (a *atomicMap) snapshot() map[string]uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]uint64, len(a.values))
	for k, v := range a.values {
		out[k] = v
	}
	return out
}
