package decisions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

func testConfig() config.DecisionsConfig {
	return config.DecisionsConfig{Model: "gpt-6-luna", Recipe: Recipe, APIKey: "synthetic-test-key", CacheSecret: strings.Repeat("s", 32), Timeout: time.Second, MaxInputBytes: 65536, SegmentExchanges: 32}
}
func response(recent, older float64) string {
	return fmt.Sprintf(`{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"recent_dependence","probability":%g},{"type":"predicate","name":"older_reference","probability":%g}],"usage":{"input_tokens":2000,"output_tokens":0,"total_tokens":2000,"input_tokens_details":{"cached_tokens":1000,"cache_write_tokens":500}}}`, recent, older)
}
func TestTypedResultsAndCosts(t *testing.T) {
	for _, tc := range []struct{ name, body, outcome, suggestion string }{
		{"standalone", response(.1, .1), "success", "standalone"},
		{"followup", response(.9, .1), "success", "recent"},
		{"older", response(.1, .9), "success", "older_reference"},
		{"uncertain", response(.5, .1), "uncertain", "recent"},
		{"refusal", strings.Replace(response(.1, .1), `"type":"predicate"`, `"type":"refusal"`, 1), "refusal", "recent"},
		{"out_of_range", response(1.1, .1), "invalid_response", "recent"},
		{"missing", strings.Replace(response(.1, .1), `"probability":0.1`, `"ignored":0.1`, 1), "invalid_response", "recent"},
		{"duplicate_name", strings.Replace(response(.1, .1), "older_reference", "recent_dependence", 1), "invalid_response", "recent"},
		{"model", strings.Replace(response(.1, .1), "gpt-6-luna", "other", 1), "invalid_response", "recent"},
		{"malformed", `{"answers":`, "invalid_response", "recent"},
		{"trailing", response(.1, .1) + `{}`, "invalid_response", "recent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parse([]byte(tc.body), "gpt-6-luna")
			if r.Outcome != tc.outcome || r.Suggestion() != tc.suggestion {
				t.Fatal(r)
			}
		})
	}
	r := parse([]byte(response(.1, .1)), "gpt-6-luna")
	c := Cost(r.Usage, &config.TokenRates{Input: 1, Cached: .1, Write: 2})
	if c == nil || *c != .0016 {
		t.Fatal("double counted cache tokens", c)
	}
	if Cost(r.Usage, nil) != nil {
		t.Fatal("unknown prices treated as free")
	}
	for _, bad := range []string{strings.Replace(response(.1, .1), `"input_tokens":2000`, `"input_tokens":-1`, 1), strings.Replace(response(.1, .1), `"cache_write_tokens":500`, `"cache_write_tokens":1500`, 1), strings.Replace(response(.1, .1), `"total_tokens":2000`, `"total_tokens":0`, 1), strings.Replace(response(.1, .1), `"cached_tokens":1000`, `"unknown":1000`, 1)} {
		if got := parse([]byte(bad), "gpt-6-luna"); got.Usage != nil {
			t.Fatal("invalid usage accepted")
		}
	}
}
func TestHTTPBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		body, outcome string
		delay         time.Duration
	}{
		{"valid", 200, response(.9, .1), "success", 0}, {"denied", 403, "private provider message", "access_denied", 0},
		{"unsupported", 404, "private provider message", "unsupported", 0}, {"rate", 429, "private", "rate_limited", 0},
		{"redirect", 302, "private", "upstream_error", 0}, {"large", 200, strings.Repeat("x", 16385), "invalid_response", 0},
		{"timeout", 200, response(.9, .1), "timeout", 60 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer synthetic-test-key" {
					t.Error("wrong request")
				}
				w.Header().Set("Location", "http://127.0.0.1:1/credential-leak")
				if tc.delay > 0 {
					time.Sleep(tc.delay)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cfg := testConfig()
			cfg.BaseURL = server.URL + "/v1"
			cfg.Timeout = 20 * time.Millisecond
			out := New(cfg).CheckAccess(context.Background())
			if out.Outcome != tc.outcome || out.Duration <= 0 {
				t.Fatal(out)
			}
		})
	}
}
func TestStablePrefixesAndPartitions(t *testing.T) {
	c := testConfig()
	scope := Scope{"owner", "session", 1, 1}
	evidence := Evidence{History: []Exchange{{1, "first", "reply"}, {2, "second", "another reply"}}, RecentSequences: []int64{1, 2}, Current: "follow-up"}
	first, err := Build(c, scope, evidence)
	if err != nil {
		t.Fatal(err)
	}
	evidence.History = append(evidence.History, Exchange{3, "third", "answer"})
	evidence.Current = "new question"
	evidence.RecentSequences = []int64{2, 3}
	next, err := Build(c, scope, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheKey != next.CacheKey || len(first.CacheKey) != 64 {
		t.Fatal("unstable or oversized key")
	}
	if !reflect.DeepEqual(first.Input[0].Content[:3], next.Input[0].Content[:3]) {
		t.Fatal("prefix changed as history grew")
	}
	for _, s := range []Scope{{"other", "session", 1, 1}, {"owner", "other", 1, 1}, {"owner", "session", 2, 1}, {"owner", "session", 1, 33}} {
		v, _ := Build(c, s, evidence)
		if v.CacheKey == first.CacheKey {
			t.Fatal("shared partition")
		}
	}
	for i := 4; i <= 40; i++ {
		evidence.History = append(evidence.History, Exchange{int64(i), "input", "reply"})
	}
	r, _ := Build(c, scope, evidence)
	marks := 0
	for _, p := range r.Input[0].Content {
		if p.Breakpoint != nil {
			marks++
		}
	}
	if marks != 4 || r.Input[0].Content[len(r.Input[0].Content)-1].Breakpoint != nil {
		t.Fatal("bad cache boundary")
	}
	b, _ := json.Marshal(first)
	if strings.Contains(string(b), "owner") || strings.Contains(string(b), "synthetic-test-key") {
		t.Fatal("identity/secret leaked")
	}
	c.MaxInputBytes = 100
	if _, err := Build(c, scope, evidence); err == nil {
		t.Fatal("unbounded input")
	}
	if Anchor(0, 32) != 1 || Anchor(31, 32) != 1 || Anchor(32, 32) != 33 {
		t.Fatal("segment rotation")
	}
}
