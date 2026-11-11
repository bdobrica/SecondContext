package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/decisions"
)

type decisionMetricKey struct{ Outcome, Suggestion, Length, Cache string }
type decisionMetric struct {
	DecisionPriced, GenerationPriced                                                         uint64
	DecisionUSD, GenerationUSD                                                               float64
	Count, UsageKnown, GenerationAttempts, GenerationUsageKnown, Priced                      uint64
	Input, Cached, Write, Output, Total, GenerationInput, GenerationCached, GenerationOutput uint64
	DecisionSeconds, GenerationSeconds, CombinedUSD                                          float64
}

func (o *observability) observeHistory(v historyObservation, c config.DecisionsConfig) {
	if o == nil {
		return
	}
	key := decisionMetricKey{v.Decision.Outcome, v.Decision.Suggestion(), "short", "unknown"}
	if v.Exchanges > 8 {
		key.Length = "long"
	}
	if v.Decision.Usage != nil {
		key.Cache = "cold"
		if v.Decision.Usage.Details.Cached > 0 {
			key.Cache = "warm"
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.decisions == nil {
		o.decisions = map[decisionMetricKey]*decisionMetric{}
	}
	m := o.decisions[key]
	if m == nil {
		m = &decisionMetric{}
		o.decisions[key] = m
	}
	m.Count++
	m.DecisionSeconds += v.Decision.Duration.Seconds()
	m.GenerationSeconds += v.GenerationDuration.Seconds()
	if u := v.Decision.Usage; u != nil {
		m.UsageKnown++
		m.Input += uint64(u.Input)
		m.Cached += uint64(u.Details.Cached)
		m.Write += uint64(u.Details.Write)
		m.Output += uint64(u.Output)
		m.Total += uint64(u.Total)
	}
	if v.GenerationAttempted {
		m.GenerationAttempts++
	}
	if u := v.Generation; u != nil {
		m.GenerationUsageKnown++
		m.GenerationInput += uint64(u.Input)
		m.GenerationCached += uint64(u.Details.Cached)
		m.GenerationOutput += uint64(u.Output)
	}
	dc, gc := decisions.Cost(v.Decision.Usage, c.Rates), decisions.Cost(v.Generation, c.GenerationRates)
	if dc != nil {
		m.DecisionPriced++
		m.DecisionUSD += *dc
	}
	if gc != nil {
		m.GenerationPriced++
		m.GenerationUSD += *gc
	}
	if dc != nil && gc != nil {
		m.Priced++
		m.CombinedUSD += *dc + *gc
	}
}

// Called under the observability lock. Labels contain only bounded enums; no
// text, IDs, cache keys, provider errors or credentials enter telemetry.
func (o *observability) renderDecisions(b *strings.Builder) {
	keys := make([]decisionMetricKey, 0, len(o.decisions))
	for k := range o.decisions {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	for _, k := range keys {
		m := o.decisions[k]
		labels := formatPrometheusLabels(map[string]string{"outcome": k.Outcome, "suggestion": k.Suggestion, "length": k.Length, "cache": k.Cache})
		counts := map[string]uint64{"requests": m.Count, "usage_known": m.UsageKnown, "input_tokens": m.Input, "cached_tokens": m.Cached, "cache_write_tokens": m.Write, "output_tokens": m.Output, "total_tokens": m.Total, "generation_attempts": m.GenerationAttempts, "generation_usage_known": m.GenerationUsageKnown, "generation_input_tokens": m.GenerationInput, "generation_cached_tokens": m.GenerationCached, "generation_output_tokens": m.GenerationOutput, "priced_requests": m.Priced, "decision_priced_requests": m.DecisionPriced, "generation_priced_requests": m.GenerationPriced}
		names := make([]string, 0, len(counts))
		for n := range counts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(b, "second_context_decisions_%s_total%s %d\n", n, labels, counts[n])
		}
		fmt.Fprintf(b, "second_context_decisions_duration_seconds_total%s %.6f\n", labels, m.DecisionSeconds)
		fmt.Fprintf(b, "second_context_decisions_generation_duration_seconds_total%s %.6f\n", labels, m.GenerationSeconds)
		fmt.Fprintf(b, "second_context_decisions_estimated_usd_total%s %.9f\n", labels, m.DecisionUSD)
		fmt.Fprintf(b, "second_context_decisions_generation_estimated_usd_total%s %.9f\n", labels, m.GenerationUSD)
		fmt.Fprintf(b, "second_context_decisions_combined_estimated_usd_total%s %.9f\n", labels, m.CombinedUSD)
	}
}
