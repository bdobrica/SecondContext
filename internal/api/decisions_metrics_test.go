package api

import (
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/decisions"
)

func TestDecisionMetricsNoDoubleCountingOrIdentifiers(t *testing.T) {
	o := newObservability("test", "test")
	p := .1
	u := &decisions.Usage{Input: 2000, Total: 2000}
	u.Details.Cached = 1000
	u.Details.Write = 500
	gen := decisions.GenerationUsage([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":500}}}`))
	if gen == nil {
		t.Fatal("missing generation usage")
	}
	c := config.DecisionsConfig{Rates: &config.TokenRates{Input: 1, Cached: .1, Write: 2}, GenerationRates: &config.TokenRates{Input: 2, Cached: 1, Output: 10}}
	o.observeHistory(historyObservation{Decision: decisions.Result{Recent: &p, Older: &p, Outcome: "success", Usage: u, Duration: time.Second}, Exchanges: 12, Generation: gen, GenerationAttempted: true, GenerationDuration: time.Second}, c)
	wire := o.renderPrometheus()
	for _, s := range []string{`cache="warm"`, `length="long"`, `suggestion="standalone"`, `0.004100000`, `input_tokens_total`, `cache_write_tokens_total`, `priced_requests_total`} {
		if !strings.Contains(wire, s) {
			t.Fatal("missing metric", s, wire)
		}
	}
	if decisions.GenerationUsage([]byte(`{"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100}}`)) != nil {
		t.Fatal("missing cache usage priced")
	}
	if decisions.GenerationUsage([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1,"prompt_tokens_details":{"cached_tokens":2}}}`)) != nil {
		t.Fatal("invalid cache usage priced")
	}
	o.observeHistory(historyObservation{Decision: decisions.Result{Outcome: "timeout"}}, c)
	if !strings.Contains(o.renderPrometheus(), `outcome="timeout",suggestion="recent"`) {
		t.Fatal("missing fallback")
	}
}
