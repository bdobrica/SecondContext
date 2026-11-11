package config

import (
	"strings"
	"testing"
)

func TestDecisionsConfig(t *testing.T) {
	keys := []string{"DECISIONS_OBSERVE_ENABLED", "DECISIONS_BASE_URL", "DECISIONS_API_KEY", "DECISIONS_MODEL", "DECISIONS_RECIPE", "DECISIONS_CACHE_SECRET", "DECISIONS_TIMEOUT", "DECISIONS_MAX_INPUT_BYTES", "DECISIONS_SEGMENT_EXCHANGES", "DECISIONS_INPUT_USD_PER_MILLION", "DECISIONS_CACHED_USD_PER_MILLION", "DECISIONS_WRITE_USD_PER_MILLION", "HISTORY_GENERATION_INPUT_USD_PER_MILLION", "HISTORY_GENERATION_CACHED_USD_PER_MILLION", "HISTORY_GENERATION_OUTPUT_USD_PER_MILLION"}
	for _, k := range keys {
		t.Setenv(k, "")
	}
	c, err := LoadDecisionsConfig()
	if err != nil || c.Enabled || c.Rates != nil || c.Model != "gpt-6-luna" {
		t.Fatal("invalid defaults", err)
	}
	for _, tc := range []struct{ k, v string }{{"DECISIONS_OBSERVE_ENABLED", "true"}, {"DECISIONS_TIMEOUT", "6s"}, {"DECISIONS_TIMEOUT", "0s"}, {"DECISIONS_BASE_URL", "http://remote.invalid/v1"}, {"DECISIONS_BASE_URL", "https://user:pass@example.com/v1"}, {"DECISIONS_BASE_URL", "https://example.com/v1?q=x"}, {"DECISIONS_MODEL", "unknown"}, {"DECISIONS_RECIPE", "unknown"}, {"DECISIONS_SEGMENT_EXCHANGES", "65"}, {"DECISIONS_MAX_INPUT_BYTES", "262145"}, {"DECISIONS_INPUT_USD_PER_MILLION", "NaN"}, {"DECISIONS_INPUT_USD_PER_MILLION", "0.1"}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			t.Setenv(tc.k, tc.v)
			if _, err := LoadDecisionsConfig(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	t.Setenv("DECISIONS_OBSERVE_ENABLED", "true")
	t.Setenv("DECISIONS_API_KEY", "test-key")
	t.Setenv("DECISIONS_CACHE_SECRET", strings.Repeat("x", 32))
	t.Setenv("DECISIONS_INPUT_USD_PER_MILLION", "1")
	t.Setenv("DECISIONS_CACHED_USD_PER_MILLION", "0.1")
	t.Setenv("DECISIONS_WRITE_USD_PER_MILLION", "2")
	c, err = LoadDecisionsConfig()
	if err != nil || !c.Enabled || c.Rates == nil || c.Rates.Write != 2 {
		t.Fatal(err)
	}
}
