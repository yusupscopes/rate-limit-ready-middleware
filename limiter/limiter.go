package limiter

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// FailureMode controls how the limiter behaves when Redis operations fail.
// In FailOpen mode, requests are allowed to proceed when Redis is unavailable.
// In FailClosed mode, requests are rejected when Redis is unavailable.
type FailureMode int

const (
	FailOpen FailureMode = iota
	FailClosed
)

// headerConfig holds configuration for rate-limit response headers.
type headerConfig struct {
	enabled         bool
	limitHeader     string
	remainingHeader string
}

// Option is a functional option for configuring a Limiter.
type Option func(*Limiter)

// Logger is the minimal logging interface used by the limiter.
type Logger interface {
	Printf(format string, v ...any)
}

// Limiter implements a sliding-window log rate limiter backed by Redis.
// It is safe for concurrent use by multiple goroutines.
type Limiter struct {
	client *redis.Client
	limit  int64
	window time.Duration
	// Now is the time source used by the limiter. It defaults to time.Now and
	// can be overridden in tests.
	Now         func() time.Time
	keyFunc     func(*http.Request) string
	headers     headerConfig
	failureMode FailureMode
	logger      Logger
	onAllowed   func(key string, r *http.Request)
	onLimited   func(key string, r *http.Request)
	onError     func(err error)
}

// New constructs a Limiter with the given Redis client, maximum number of
// requests allowed within the window, and optional configuration options.
//
// By default it:
//   - derives keys from the request's remote address (with conservative
//     X-Forwarded-For handling),
//   - emits X-RateLimit-Limit and X-RateLimit-Remaining headers, and
//   - fails open when Redis is unavailable.
func New(client *redis.Client, limit int64, window time.Duration, opts ...Option) *Limiter {
	rl := &Limiter{
		client:  client,
		limit:   limit,
		window:  window,
		Now:     time.Now,
		keyFunc: defaultKeyFunc,
		headers: headerConfig{
			enabled:         true,
			limitHeader:     "X-RateLimit-Limit",
			remainingHeader: "X-RateLimit-Remaining",
		},
		failureMode: FailOpen,
		logger:      log.Default(),
	}

	for _, opt := range opts {
		opt(rl)
	}

	return rl
}

// WithKeyFunc configures a custom function that derives the rate-limit key
// from the incoming HTTP request. If nil is provided, the default key
// function is preserved.
func WithKeyFunc(f func(*http.Request) string) Option {
	return func(l *Limiter) {
		if f != nil {
			l.keyFunc = f
		}
	}
}

// WithHeaders configures whether rate-limit headers are written to responses
// and allows overriding the header names. If a header name is empty, the
// default name is retained.
func WithHeaders(enabled bool, limitHeader, remainingHeader string) Option {
	return func(l *Limiter) {
		l.headers.enabled = enabled
		if limitHeader != "" {
			l.headers.limitHeader = limitHeader
		}
		if remainingHeader != "" {
			l.headers.remainingHeader = remainingHeader
		}
	}
}

// WithFailureMode configures how the limiter behaves when Redis operations
// fail (for example, when the backend is unavailable).
func WithFailureMode(mode FailureMode) Option {
	return func(l *Limiter) {
		l.failureMode = mode
	}
}

// WithLogger configures a custom logger implementation used for internal
// diagnostics (for example, Redis errors). If nil is provided, logging is
// disabled.
func WithLogger(logger Logger) Option {
	return func(l *Limiter) {
		if logger == nil {
			l.logger = noopLogger{}
			return
		}
		l.logger = logger
	}
}

// WithOnAllowed registers a callback that is invoked when a request is
// successfully allowed by the limiter.
func WithOnAllowed(fn func(key string, r *http.Request)) Option {
	return func(l *Limiter) {
		l.onAllowed = fn
	}
}

// WithOnLimited registers a callback that is invoked when a request is
// rejected due to exceeding the configured limit.
func WithOnLimited(fn func(key string, r *http.Request)) Option {
	return func(l *Limiter) {
		l.onLimited = fn
	}
}

// WithOnError registers a callback that is invoked when a Redis error
// occurs while evaluating the rate limit.
func WithOnError(fn func(err error)) Option {
	return func(l *Limiter) {
		l.onError = fn
	}
}

// defaultKeyFunc returns a key derived from the request. It prefers the
// left-most X-Forwarded-For value when present (assuming a trusted proxy
// in front of the application), otherwise it falls back to the remote IP.
func defaultKeyFunc(r *http.Request) string {
	hostPort := r.RemoteAddr
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		hostPort = ip
	}

	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return hostPort
	}

	parts := strings.Split(xff, ",")
	if len(parts) == 0 {
		return hostPort
	}

	return strings.TrimSpace(parts[0])
}

type noopLogger struct{}

func (noopLogger) Printf(string, ...any) {}

func (rl *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		key := fmt.Sprintf("ratelimit:%s", rl.keyFunc(r))

		now := rl.Now()
		nowNano := now.UnixNano()
		windowStart := now.Add(-rl.window).UnixNano()

		member := strconv.FormatInt(nowNano, 10)
		windowStartStr := strconv.FormatInt(windowStart, 10)

		pipe := rl.client.TxPipeline()
		pipe.ZRemRangeByScore(ctx, key, "-inf", windowStartStr)
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowNano), Member: member})
		countCmd := pipe.ZCard(ctx, key)
		pipe.Expire(ctx, key, rl.window)

		_, err := pipe.Exec(ctx)
		if err != nil {
			rl.logger.Printf("Rate limiter Redis error: %v", err)
			if rl.onError != nil {
				rl.onError(err)
			}
			if rl.failureMode == FailClosed {
				http.Error(w, "service unavailable due to rate limiting backend error", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		requestsThisWindow := countCmd.Val()
		remaining := rl.limit - requestsThisWindow
		if remaining < 0 {
			remaining = 0
		}

		if rl.headers.enabled {
			w.Header().Set(rl.headers.limitHeader, strconv.FormatInt(rl.limit, 10))
			w.Header().Set(rl.headers.remainingHeader, strconv.FormatInt(remaining, 10))
		}

		if requestsThisWindow > rl.limit {
			if rl.onLimited != nil {
				rl.onLimited(key, r)
			}
			http.Error(w, "429 Too Many Requests - Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		if rl.onAllowed != nil {
			rl.onAllowed(key, r)
		}

		next.ServeHTTP(w, r)
	})
}
