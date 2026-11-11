package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"time"
)

// Decisions supports independently enabled observation and adaptive selection. Rates are optional USD per million tokens;
// nil means unknown, never free. Secrets stay server-side and out of bindings.
type DecisionsConfig struct {
	Enabled, AdaptiveEnabled                    bool
	BaseURL, APIKey, Model, Recipe, CacheSecret string
	Timeout                                     time.Duration
	MaxInputBytes, SegmentExchanges             int
	Rates, GenerationRates                      *TokenRates
}
type TokenRates struct{ Input, Cached, Write, Output float64 }

func LoadDecisionsConfig() (DecisionsConfig, error) {
	c := DecisionsConfig{BaseURL: getEnv("DECISIONS_BASE_URL", "https://api.openai.com/v1"), APIKey: os.Getenv("DECISIONS_API_KEY"), Model: getEnv("DECISIONS_MODEL", "gpt-6-luna"), Recipe: getEnv("DECISIONS_RECIPE", "history-dependence-1"), CacheSecret: os.Getenv("DECISIONS_CACHE_SECRET")}
	var err error
	if c.Enabled, err = parseBool("DECISIONS_OBSERVE_ENABLED", false); err != nil {
		return c, err
	}
	if c.AdaptiveEnabled, err = parseBool("HISTORY_ADAPTIVE_ENABLED", false); err != nil {
		return c, err
	}
	if c.Timeout, err = parseDuration("DECISIONS_TIMEOUT", "2s"); err != nil {
		return c, err
	}
	if c.MaxInputBytes, err = parseInt("DECISIONS_MAX_INPUT_BYTES", 65536); err != nil {
		return c, err
	}
	if c.SegmentExchanges, err = parseInt("DECISIONS_SEGMENT_EXCHANGES", 32); err != nil {
		return c, err
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return c, fmt.Errorf("invalid DECISIONS_BASE_URL")
	}
	if c.Model != "gpt-6-luna" || c.Recipe != "history-dependence-1" || c.Timeout <= 0 || c.Timeout > 5*time.Second || c.MaxInputBytes < 1024 || c.MaxInputBytes > 262144 || c.SegmentExchanges < 1 || c.SegmentExchanges > 64 {
		return c, fmt.Errorf("invalid Decisions model, recipe or limits")
	}
	if (c.Enabled || c.AdaptiveEnabled) && (strings.TrimSpace(c.APIKey) == "" || len(c.CacheSecret) < 32) {
		return c, fmt.Errorf("Decisions requires DECISIONS_API_KEY and DECISIONS_CACHE_SECRET (at least 32 bytes)")
	}
	if c.Rates, err = loadTokenRates("DECISIONS", true); err != nil {
		return c, err
	}
	if c.GenerationRates, err = loadTokenRates("HISTORY_GENERATION", false); err != nil {
		return c, err
	}
	return c, nil
}
func loadTokenRates(prefix string, decisions bool) (*TokenRates, error) {
	keys := []string{"INPUT", "CACHED", "OUTPUT"}
	if decisions {
		keys = []string{"INPUT", "CACHED", "WRITE"}
	}
	values := make([]float64, 3)
	supplied := 0
	for i, key := range keys {
		name := prefix + "_" + key + "_USD_PER_MILLION"
		if os.Getenv(name) == "" {
			continue
		}
		supplied++
		v, err := parseFloat64(name, 0)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000000 {
			return nil, fmt.Errorf("invalid %s rate", prefix)
		}
		values[i] = v
	}
	if supplied == 0 {
		return nil, nil
	}
	if supplied != 3 {
		return nil, fmt.Errorf("all %s token rates must be supplied together", prefix)
	}
	r := &TokenRates{Input: values[0], Cached: values[1]}
	if decisions {
		r.Write = values[2]
	} else {
		r.Output = values[2]
	}
	return r, nil
}
