package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkoukk/tiktoken-go"

	"cloud-gateway-lab/pkg/limiter"
	"cloud-gateway-lab/pkg/prefixcache"
	"cloud-gateway-lab/pkg/scheduler"
)

type ctxKey int

const (
	ctxUserID ctxKey = iota
	ctxPreDeducted
	ctxPromptTokens
)

const prefixCacheHeader = "X-Prefix-Cache-Hit"

type Config struct {
	Upstream         *url.URL
	Limiter          *limiter.Limiter
	Scheduler        *scheduler.Scheduler
	Prefix           *prefixcache.PrefixIndexer
	Encoder          *tiktoken.Tiktoken
	Logger           *slog.Logger
	ShortTextTokens  int64
	CompletionReserve int64
}

type Proxy struct {
	upstream          *httputil.ReverseProxy
	limiter           *limiter.Limiter
	scheduler         *scheduler.Scheduler
	prefix            *prefixcache.PrefixIndexer
	encoder           *tiktoken.Tiktoken
	logger            *slog.Logger
	shortTextTokens   int64
	completionReserve int64
	errors            atomic.Uint64
}

func New(cfg Config) (*Proxy, error) {
	if cfg.Upstream == nil {
		return nil, errors.New("upstream URL is required")
	}
	if cfg.Limiter == nil {
		return nil, errors.New("limiter is required")
	}
	if cfg.Scheduler == nil {
		return nil, errors.New("scheduler is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ShortTextTokens < 1 {
		cfg.ShortTextTokens = 128
	}
	if cfg.CompletionReserve < 1 {
		cfg.CompletionReserve = 256
	}

	p := &Proxy{
		limiter:           cfg.Limiter,
		scheduler:         cfg.Scheduler,
		prefix:            cfg.Prefix,
		encoder:           cfg.Encoder,
		logger:            cfg.Logger,
		shortTextTokens:   cfg.ShortTextTokens,
		completionReserve: cfg.CompletionReserve,
	}

	reverse := httputil.NewSingleHostReverseProxy(cfg.Upstream)
	reverse.FlushInterval = -1
	reverse.Transport = &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		// First-token wait only; the body stream has no extra deadline.
		ResponseHeaderTimeout: 60 * time.Second,
	}
	originalDirector := reverse.Director
	reverse.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = cfg.Upstream.Host
	}
	reverse.ModifyResponse = p.modifyResponse
	reverse.ErrorHandler = p.errorHandler
	p.upstream = reverse
	return p, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	userID := clientKey(r)
	prompt := promptFromChatBody(body)
	promptTokens := p.countTokens(prompt)
	preDeducted := promptTokens + p.completionReserveFrom(body)

	if p.prefix != nil && prompt != "" {
		hit, matchedLen := p.prefix.MatchPrefix(prompt)
		if hit {
			r.Header.Set(prefixCacheHeader, "true")
			w.Header().Set(prefixCacheHeader, "true")
			p.logger.Info("prefix cache hit",
				"user", userID,
				"matched_len", matchedLen,
				"prompt_len", len(prompt),
			)
		}
		// Index this prompt so a follow-up with the same system prefix can hit.
		if sys := systemPrefix(prompt); sys != "" {
			p.prefix.Insert(sys)
		}
	}

	allowed, err := p.limiter.PreDeduct(r.Context(), userID, preDeducted)
	if err != nil || !allowed {
		p.writeDeductError(w, err)
		return
	}

	ctx := context.WithValue(r.Context(), ctxUserID, userID)
	ctx = context.WithValue(ctx, ctxPreDeducted, preDeducted)
	ctx = context.WithValue(ctx, ctxPromptTokens, promptTokens)
	r = r.WithContext(ctx)

	priority := scheduler.PriorityLow
	if isHighPriority(r, promptTokens, p.shortTextTokens) {
		priority = scheduler.PriorityHigh
	}

	err = p.scheduler.Submit(ctx, func(jobCtx context.Context) {
		p.upstream.ServeHTTP(w, r.WithContext(jobCtx))
	}, priority)
	if err != nil {
		// The request never reached upstream; refund the whole pre-deduct.
		if settleErr := p.limiter.SettleQuota(context.Background(), userID, preDeducted, 0); settleErr != nil {
			p.logger.Error("refund after queue failure", "user", userID, "error", settleErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "request canceled", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, "scheduler unavailable", http.StatusServiceUnavailable)
	}
}

func (p *Proxy) modifyResponse(resp *http.Response) error {
	userID, _ := resp.Request.Context().Value(ctxUserID).(string)
	preDeducted, _ := resp.Request.Context().Value(ctxPreDeducted).(int64)
	promptTokens, _ := resp.Request.Context().Value(ctxPromptTokens).(int64)
	if userID == "" {
		return nil
	}

	pr, pw := io.Pipe()
	original := resp.Body
	resp.Body = pr

	go func() {
		actual := p.teeAndCount(original, pw, promptTokens)
		if err := p.limiter.SettleQuota(context.Background(), userID, preDeducted, actual); err != nil {
			p.logger.Error("settle quota", "user", userID, "pre", preDeducted, "actual", actual, "error", err)
			return
		}
		p.logger.Info("quota settled",
			"user", userID,
			"pre_deducted", preDeducted,
			"actual", actual,
			"refunded", preDeducted-actual,
		)
	}()
	return nil
}

// teeAndCount copies upstream bytes into the pipe (so the client sees them
// immediately) while parsing SSE rows for generated text / usage.
func (p *Proxy) teeAndCount(src io.ReadCloser, dst *io.PipeWriter, promptTokens int64) int64 {
	defer src.Close()
	defer dst.Close()

	reader := bufio.NewReaderSize(src, 32*1024)
	var generated strings.Builder
	var usage streamUsage

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if dst != nil {
				if _, werr := dst.Write(line); werr != nil {
					// Client hung up; keep draining so we can still settle.
					dst = nil
				}
			}
			parseSSELine(line, &generated, &usage)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				p.logger.Error("read upstream stream", "error", err)
			}
			break
		}
	}

	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	completion := int64(0)
	if usage.CompletionTokens > 0 {
		completion = usage.CompletionTokens
	} else {
		completion = p.countTokens(generated.String())
	}
	prompt := promptTokens
	if usage.PromptTokens > 0 {
		prompt = usage.PromptTokens
	}
	return prompt + completion
}

func (p *Proxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	p.errors.Add(1)
	p.logger.Error("upstream request failed", "path", r.URL.Path, "error", err)
	userID, _ := r.Context().Value(ctxUserID).(string)
	preDeducted, _ := r.Context().Value(ctxPreDeducted).(int64)
	if userID != "" && preDeducted > 0 {
		if settleErr := p.limiter.SettleQuota(context.Background(), userID, preDeducted, 0); settleErr != nil {
			p.logger.Error("refund after upstream error", "user", userID, "error", settleErr)
		}
	}
	http.Error(w, "bad gateway", http.StatusBadGateway)
}

func (p *Proxy) writeDeductError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, limiter.ErrRateLimitExceeded):
		w.Header().Set("Retry-After", "1")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	case errors.Is(err, limiter.ErrInsufficientBalance):
		http.Error(w, "insufficient balance", http.StatusPaymentRequired)
	default:
		p.logger.Error("prededuct failed", "error", err)
		http.Error(w, "limiter unavailable", http.StatusServiceUnavailable)
	}
}

func (p *Proxy) countTokens(text string) int64 {
	if text == "" {
		return 0
	}
	if p.encoder != nil {
		return int64(len(p.encoder.Encode(text, nil, nil)))
	}
	// Fallback: ~4 chars per token when tiktoken is unavailable.
	n := int64((len(text) + 3) / 4)
	if n < 1 {
		return 1
	}
	return n
}

func (p *Proxy) completionReserveFrom(body []byte) int64 {
	var req struct {
		MaxTokens int64 `json:"max_tokens"`
	}
	if json.Unmarshal(body, &req) == nil && req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return p.completionReserve
}

func isHighPriority(r *http.Request, promptTokens, shortTextTokens int64) bool {
	tier := strings.ToLower(strings.TrimSpace(r.Header.Get("X-User-Tier")))
	priority := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Priority")))
	if tier == "vip" || priority == "high" {
		return true
	}
	return promptTokens > 0 && promptTokens <= shortTextTokens
}

func clientKey(r *http.Request) string {
	if id := strings.TrimSpace(r.Header.Get("X-User-ID")); id != "" {
		return id
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
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
