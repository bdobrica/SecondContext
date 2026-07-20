package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		if s.obs != nil {
			s.obs.addInflight(1)
			defer s.obs.addInflight(-1)
		}
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(wrapped, r)

		statusCode := wrapped.Status()
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		route := requestRouteLabel(r)
		duration := time.Since(startedAt)
		if s.obs != nil {
			s.obs.observeHTTPRequest(r.Method, route, statusCode, duration, wrapped.BytesWritten())
		}

		s.emitRequestLog(r, route, statusCode, wrapped.BytesWritten(), duration, "http request")
	})
}

func requestRouteLabel(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := strings.TrimSpace(routeContext.RoutePattern()); pattern != "" {
			return pattern
		}
	}

	return normalizePathLabel(r.URL.Path)
}

func normalizePathLabel(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	parts := strings.Split(trimmed, "/")
	for index, part := range parts {
		if part == "" {
			continue
		}
		if looksLikeIdentifier(part) {
			parts[index] = ":id"
		}
	}
	result := strings.Join(parts, "/")
	if !strings.HasPrefix(result, "/") {
		return "/" + result
	}
	return result
}

func looksLikeIdentifier(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 32 {
		return true
	}
	if strings.Count(trimmed, "-") >= 2 && len(trimmed) >= 16 {
		return true
	}
	if len(trimmed) < 8 {
		return false
	}
	hasIdentifierHint := false
	for _, runeValue := range trimmed {
		if (runeValue >= 'a' && runeValue <= 'z') || (runeValue >= 'A' && runeValue <= 'Z') || (runeValue >= '0' && runeValue <= '9') || runeValue == '-' || runeValue == '_' {
			if (runeValue >= '0' && runeValue <= '9') || runeValue == '-' || runeValue == '_' {
				hasIdentifierHint = true
			}
			continue
		}
		return false
	}
	return hasIdentifierHint
}

func clientIP(r *http.Request) string {
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remoteAddr
}
