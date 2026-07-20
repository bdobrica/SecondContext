package api

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPResolverForwardedChains(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		forwarded string
		trusted   []string
		want      string
	}{
		{
			name:      "direct IPv4 ignores spoofed header",
			remote:    "203.0.113.10:4000",
			forwarded: "198.51.100.8",
			want:      "203.0.113.10",
		},
		{
			name:      "direct IPv6 ignores spoofed header",
			remote:    "[2001:db8::10]:4000",
			forwarded: "2001:db8::99",
			want:      "2001:db8::10",
		},
		{
			name:      "one trusted proxy",
			remote:    "10.0.0.4:4000",
			forwarded: "203.0.113.10",
			trusted:   []string{"10.0.0.0/8"},
			want:      "203.0.113.10",
		},
		{
			name:      "multiple trusted proxy hops",
			remote:    "10.0.0.4:4000",
			forwarded: "203.0.113.10, 192.0.2.8",
			trusted:   []string{"10.0.0.0/8", "192.0.2.0/24"},
			want:      "203.0.113.10",
		},
		{
			name:      "first untrusted hop is client boundary",
			remote:    "10.0.0.4:4000",
			forwarded: "198.51.100.3, 203.0.113.9, 192.0.2.8",
			trusted:   []string{"10.0.0.0/8", "192.0.2.0/24"},
			want:      "203.0.113.9",
		},
		{
			name:      "untrusted proxy cannot forward",
			remote:    "192.0.2.8:4000",
			forwarded: "203.0.113.10",
			trusted:   []string{"10.0.0.0/8"},
			want:      "192.0.2.8",
		},
		{
			name:      "malformed chain safely uses socket peer",
			remote:    "10.0.0.4:4000",
			forwarded: "203.0.113.10, malformed",
			trusted:   []string{"10.0.0.0/8"},
			want:      "10.0.0.4",
		},
		{
			name:      "IPv4-mapped peer matches IPv4 proxy CIDR",
			remote:    "[::ffff:10.0.0.4]:4000",
			forwarded: "2001:db8::10",
			trusted:   []string{"10.0.0.0/8"},
			want:      "2001:db8::10",
		},
		{
			name:      "trusted IPv6 proxy",
			remote:    "[2001:db8:1::4]:4000",
			forwarded: "2001:db8:2::10",
			trusted:   []string{"2001:db8:1::/48"},
			want:      "2001:db8:2::10",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefixes := make([]netip.Prefix, 0, len(test.trusted))
			for _, value := range test.trusted {
				prefixes = append(prefixes, netip.MustParsePrefix(value))
			}
			resolver := newClientIPResolver(prefixes)
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			request.Header.Set(forwardedForHeader, test.forwarded)

			if got := resolver.resolve(request).String(); got != test.want {
				t.Fatalf("resolve() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientIPMiddlewareSharesResolvedAddress(t *testing.T) {
	resolver := newClientIPResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	handler := resolver.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := resolvedClientIP(r); got != "203.0.113.10" {
			t.Fatalf("resolvedClientIP() = %q", got)
		}
		if got := clientRateLimitKey(r); got != "ip:203.0.113.10" {
			t.Fatalf("clientRateLimitKey() = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.4:4000"
	request.Header.Set(forwardedForHeader, "203.0.113.10")

	handler.ServeHTTP(httptest.NewRecorder(), request)
}
