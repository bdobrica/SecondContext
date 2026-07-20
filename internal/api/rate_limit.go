package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	rateLimitClientTTL         = 10 * time.Minute
	rateLimitCleanupInterval   = 5 * time.Minute
	rateLimitErrorType         = "rate_limit_error"
	rateLimitExceededErrorCode = "rate_limit_exceeded"
)

type requestRateLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	clients     map[string]*rateLimitClient
	lastCleanup time.Time
}

type rateLimitClient struct {
	requests []time.Time
	lastSeen time.Time
}

func newRequestRateLimiter(limit int, window time.Duration) *requestRateLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}

	return &requestRateLimiter{
		limit:   limit,
		window:  window,
		clients: make(map[string]*rateLimitClient),
	}
}

func (rl *requestRateLimiter) middleware(server *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			if shouldSkipRateLimit(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			allowed, retryAfter := rl.allow(clientRateLimitKey(r), time.Now())
			if !allowed {
				w.Header().Set("Retry-After", strconv.FormatInt(int64(retryAfter.Round(time.Second)/time.Second), 10))
				server.observeRejectedRequest(r, http.StatusTooManyRequests, time.Since(startedAt), rateLimitExceededErrorCode)
				server.writeAPIError(
					w,
					r,
					http.StatusTooManyRequests,
					fmt.Sprintf("request rate limit exceeded; retry in %s", retryAfter.Round(time.Second)),
					rateLimitErrorType,
					rateLimitExceededErrorCode,
					"",
				)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (rl *requestRateLimiter) allow(clientKey string, now time.Time) (bool, time.Duration) {
	if strings.TrimSpace(clientKey) == "" {
		clientKey = "unknown"
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.cleanupStaleClients(now)

	client := rl.clients[clientKey]
	if client == nil {
		client = &rateLimitClient{}
		rl.clients[clientKey] = client
	}

	windowStart := now.Add(-rl.window)
	pruned := client.requests[:0]
	for _, requestTime := range client.requests {
		if !requestTime.Before(windowStart) {
			pruned = append(pruned, requestTime)
		}
	}
	client.requests = pruned
	client.lastSeen = now

	if len(client.requests) >= rl.limit {
		retryAfter := client.requests[0].Add(rl.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return false, retryAfter
	}

	client.requests = append(client.requests, now)
	return true, 0
}

func (rl *requestRateLimiter) cleanupStaleClients(now time.Time) {
	if !rl.lastCleanup.IsZero() && now.Sub(rl.lastCleanup) < rateLimitCleanupInterval {
		return
	}

	for clientKey, client := range rl.clients {
		if now.Sub(client.lastSeen) > rateLimitClientTTL {
			delete(rl.clients, clientKey)
		}
	}

	rl.lastCleanup = now
}

func shouldSkipRateLimit(path string) bool {
	return path == "/healthz"
}

func clientRateLimitKey(r *http.Request) string {
	if subject := authenticatedSubject(r.Context()); subject != "" {
		return "subject:" + subject
	}

	address := resolvedClientIP(r)
	if address == "" {
		return "unknown"
	}
	return "ip:" + address
}
