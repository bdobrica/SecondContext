package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const forwardedForHeader = "X-Forwarded-For"

type clientIPContextKey struct{}

type clientIPResolver struct {
	trustedProxies []netip.Prefix
}

func newClientIPResolver(trustedProxies []netip.Prefix) *clientIPResolver {
	return &clientIPResolver{trustedProxies: append([]netip.Prefix(nil), trustedProxies...)}
}

func (resolver *clientIPResolver) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address := resolver.resolve(r)
		ctx := context.WithValue(r.Context(), clientIPContextKey{}, address)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (resolver *clientIPResolver) resolve(r *http.Request) netip.Addr {
	peer, ok := parseSocketAddress(r.RemoteAddr)
	if !ok || !resolver.isTrusted(peer) {
		return peer
	}

	header := strings.TrimSpace(r.Header.Get(forwardedForHeader))
	if header == "" {
		return peer
	}

	parts := strings.Split(header, ",")
	for index := len(parts) - 1; index >= 0; index-- {
		if !resolver.isTrusted(peer) {
			break
		}
		hop, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
		if err != nil || hop.Zone() != "" {
			return socketPeerAddress(r)
		}
		peer = hop.Unmap()
	}
	return peer
}

func (resolver *clientIPResolver) isTrusted(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	for _, prefix := range resolver.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func socketPeerAddress(r *http.Request) netip.Addr {
	address, _ := parseSocketAddress(r.RemoteAddr)
	return address
}

func parseSocketAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		value = host
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func resolvedClientIP(r *http.Request) string {
	if address, ok := r.Context().Value(clientIPContextKey{}).(netip.Addr); ok && address.IsValid() {
		return address.String()
	}
	address, ok := parseSocketAddress(r.RemoteAddr)
	if !ok {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return address.String()
}
