package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/go-chi/chi/v5/middleware"
)

var (
	httpDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
	llmDurationBuckets  = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
)

type observability struct {
	service  string
	env      string
	started  time.Time
	inFlight atomic.Int64
	mu       sync.Mutex
	http     map[httpMetricKey]*httpMetricValue
	llm      map[llmMetricKey]*llmMetricValue
}

type httpMetricKey struct {
	Method string
	Route  string
	Status int
}

type llmMetricKey struct {
	Operation string
	Model     string
	Outcome   string
}

type httpMetricValue struct {
	Count         uint64
	SumSeconds    float64
	Buckets       []uint64
	ResponseBytes uint64
}

type llmMetricValue struct {
	Count        uint64
	SumSeconds   float64
	Buckets      []uint64
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
}

func newObservability(service, env string) *observability {
	return &observability{
		service: strings.TrimSpace(service),
		env:     strings.TrimSpace(env),
		started: time.Now().UTC(),
		http:    make(map[httpMetricKey]*httpMetricValue),
		llm:     make(map[llmMetricKey]*llmMetricValue),
	}
}

func (o *observability) addInflight(delta int64) {
	o.inFlight.Add(delta)
}

func (o *observability) observeHTTPRequest(method, route string, statusCode int, duration time.Duration, responseBytes int) {
	if o == nil {
		return
	}

	key := httpMetricKey{Method: strings.ToUpper(strings.TrimSpace(method)), Route: normalizePathLabel(route), Status: normalizeStatusCode(statusCode)}
	durationSeconds := duration.Seconds()

	o.mu.Lock()
	defer o.mu.Unlock()

	metric := o.http[key]
	if metric == nil {
		metric = &httpMetricValue{Buckets: make([]uint64, len(httpDurationBuckets))}
		o.http[key] = metric
	}
	metric.Count++
	metric.SumSeconds += durationSeconds
	if responseBytes > 0 {
		metric.ResponseBytes += uint64(responseBytes)
	}
	observeBuckets(metric.Buckets, httpDurationBuckets, durationSeconds)
}

func (o *observability) observeLLMRequest(operation, model, outcome string, duration time.Duration, usage llm.Usage) {
	if o == nil {
		return
	}

	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = "unknown"
	}
	key := llmMetricKey{Operation: strings.TrimSpace(operation), Model: resolvedModel, Outcome: strings.TrimSpace(outcome)}
	durationSeconds := duration.Seconds()

	o.mu.Lock()
	defer o.mu.Unlock()

	metric := o.llm[key]
	if metric == nil {
		metric = &llmMetricValue{Buckets: make([]uint64, len(llmDurationBuckets))}
		o.llm[key] = metric
	}
	metric.Count++
	metric.SumSeconds += durationSeconds
	metric.InputTokens += uint64(maxIntMetric(usage.InputTokens))
	metric.OutputTokens += uint64(maxIntMetric(usage.OutputTokens))
	metric.TotalTokens += uint64(maxIntMetric(usage.TotalTokens))
	observeBuckets(metric.Buckets, llmDurationBuckets, durationSeconds)
}

func observeBuckets(target []uint64, boundaries []float64, value float64) {
	for index, boundary := range boundaries {
		if value <= boundary {
			target[index]++
		}
	}
}

func maxIntMetric(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func normalizeStatusCode(statusCode int) int {
	if statusCode <= 0 {
		return http.StatusOK
	}
	return statusCode
}

func (o *observability) renderPrometheus() string {
	if o == nil {
		return ""
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	var builder strings.Builder
	writeMetricHelp(&builder, "second_context_http_requests_in_flight", "Current number of in-flight HTTP requests.", "gauge")
	fmt.Fprintf(&builder, "second_context_http_requests_in_flight %d\n", o.inFlight.Load())
	writeMetricHelp(&builder, "second_context_http_requests_total", "Total number of completed HTTP requests.", "counter")
	writeMetricHelp(&builder, "second_context_http_response_size_bytes_total", "Total response bytes written by completed HTTP requests.", "counter")
	writeMetricHelp(&builder, "second_context_http_request_duration_seconds", "HTTP request duration in seconds.", "histogram")
	writeMetricHelp(&builder, "second_context_llm_requests_total", "Total number of upstream LLM requests.", "counter")
	writeMetricHelp(&builder, "second_context_llm_request_duration_seconds", "Upstream LLM request duration in seconds.", "histogram")
	writeMetricHelp(&builder, "second_context_llm_input_tokens_total", "Total upstream LLM input tokens.", "counter")
	writeMetricHelp(&builder, "second_context_llm_output_tokens_total", "Total upstream LLM output tokens.", "counter")
	writeMetricHelp(&builder, "second_context_llm_total_tokens_total", "Total upstream LLM tokens.", "counter")
	writeMetricHelp(&builder, "second_context_build_info", "Build and runtime labels for the running API process.", "gauge")
	fmt.Fprintf(&builder, "second_context_build_info%s 1\n", formatPrometheusLabels(map[string]string{"service": firstNonEmpty(o.service, "second-context"), "environment": firstNonEmpty(o.env, "unknown")}))

	httpKeys := make([]httpMetricKey, 0, len(o.http))
	for key := range o.http {
		httpKeys = append(httpKeys, key)
	}
	sort.Slice(httpKeys, func(left, right int) bool {
		if httpKeys[left].Route != httpKeys[right].Route {
			return httpKeys[left].Route < httpKeys[right].Route
		}
		if httpKeys[left].Method != httpKeys[right].Method {
			return httpKeys[left].Method < httpKeys[right].Method
		}
		return httpKeys[left].Status < httpKeys[right].Status
	})
	for _, key := range httpKeys {
		metric := o.http[key]
		labels := map[string]string{"method": key.Method, "route": key.Route, "status": strconv.Itoa(key.Status)}
		fmt.Fprintf(&builder, "second_context_http_requests_total%s %d\n", formatPrometheusLabels(labels), metric.Count)
		fmt.Fprintf(&builder, "second_context_http_response_size_bytes_total%s %d\n", formatPrometheusLabels(labels), metric.ResponseBytes)
		writeHistogram(&builder, "second_context_http_request_duration_seconds", labels, metric.Count, metric.SumSeconds, httpDurationBuckets, metric.Buckets)
	}

	llmKeys := make([]llmMetricKey, 0, len(o.llm))
	for key := range o.llm {
		llmKeys = append(llmKeys, key)
	}
	sort.Slice(llmKeys, func(left, right int) bool {
		if llmKeys[left].Operation != llmKeys[right].Operation {
			return llmKeys[left].Operation < llmKeys[right].Operation
		}
		if llmKeys[left].Model != llmKeys[right].Model {
			return llmKeys[left].Model < llmKeys[right].Model
		}
		return llmKeys[left].Outcome < llmKeys[right].Outcome
	})
	for _, key := range llmKeys {
		metric := o.llm[key]
		labels := map[string]string{"operation": key.Operation, "model": key.Model, "outcome": key.Outcome}
		fmt.Fprintf(&builder, "second_context_llm_requests_total%s %d\n", formatPrometheusLabels(labels), metric.Count)
		fmt.Fprintf(&builder, "second_context_llm_input_tokens_total%s %d\n", formatPrometheusLabels(labels), metric.InputTokens)
		fmt.Fprintf(&builder, "second_context_llm_output_tokens_total%s %d\n", formatPrometheusLabels(labels), metric.OutputTokens)
		fmt.Fprintf(&builder, "second_context_llm_total_tokens_total%s %d\n", formatPrometheusLabels(labels), metric.TotalTokens)
		writeHistogram(&builder, "second_context_llm_request_duration_seconds", labels, metric.Count, metric.SumSeconds, llmDurationBuckets, metric.Buckets)
	}

	return builder.String()
}

func writeMetricHelp(builder *strings.Builder, name, help, metricType string) {
	fmt.Fprintf(builder, "# HELP %s %s\n", name, help)
	fmt.Fprintf(builder, "# TYPE %s %s\n", name, metricType)
}

func writeHistogram(builder *strings.Builder, name string, labels map[string]string, count uint64, sum float64, boundaries []float64, buckets []uint64) {
	for index, boundary := range boundaries {
		labelsWithBucket := copyLabels(labels)
		labelsWithBucket["le"] = strconv.FormatFloat(boundary, 'f', -1, 64)
		fmt.Fprintf(builder, "%s%s %d\n", name, formatPrometheusLabels(labelsWithBucket), buckets[index])
	}
	labelsWithInf := copyLabels(labels)
	labelsWithInf["le"] = "+Inf"
	fmt.Fprintf(builder, "%s%s %d\n", name, formatPrometheusLabels(labelsWithInf), count)
	fmt.Fprintf(builder, "%s_sum%s %.6f\n", name, formatPrometheusLabels(labels), sum)
	fmt.Fprintf(builder, "%s_count%s %d\n", name, formatPrometheusLabels(labels), count)
}

func copyLabels(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func formatPrometheusLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapePrometheusLabel(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapePrometheusLabel(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\\"`, "\n", `\\n`).Replace(value)
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if s.obs == nil {
		_, _ = w.Write([]byte(""))
		return
	}
	_, _ = w.Write([]byte(s.obs.renderPrometheus()))
}

func (s *Server) observeRejectedRequest(r *http.Request, statusCode int, duration time.Duration, errorCode string) {
	route := requestRouteLabel(r)
	if s.obs != nil {
		s.obs.observeHTTPRequest(r.Method, route, statusCode, duration, 0)
	}
	s.emitRequestLog(r, route, statusCode, 0, duration, "http request rejected", "error_code", errorCode)
}

func (s *Server) emitRequestLog(r *http.Request, route string, statusCode int, responseBytes int, duration time.Duration, message string, extra ...any) {
	statusCode = normalizeStatusCode(statusCode)
	args := []any{
		"request_id", middleware.GetReqID(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"route", route,
		"status", statusCode,
		"bytes", responseBytes,
		"duration_ms", duration.Milliseconds(),
		"remote_ip", clientIP(r),
		"user_agent", strings.TrimSpace(r.UserAgent()),
	}
	if subject := authenticatedSubject(r.Context()); subject != "" {
		args = append(args, "auth_subject", subject)
	}
	if r.ContentLength >= 0 {
		args = append(args, "content_length", r.ContentLength)
	}
	args = append(args, extra...)

	switch {
	case statusCode >= http.StatusInternalServerError:
		s.logger.Error(message, args...)
	case statusCode >= http.StatusBadRequest:
		s.logger.Warn(message, args...)
	default:
		s.logger.Info(message, args...)
	}
}

type observedLLMClient struct {
	next   llm.Client
	logger *slog.Logger
	obs    *observability
}

func newObservedLLMClient(next llm.Client, logger *slog.Logger, obs *observability) llm.Client {
	if next == nil {
		return nil
	}
	return &observedLLMClient{next: next, logger: logger, obs: obs}
}

func (c *observedLLMClient) Generate(ctx context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	startedAt := time.Now()
	response, err := c.next.Generate(ctx, request)
	model := firstNonEmpty(response.Model, request.Model)
	c.record(ctx, "generate", model, time.Since(startedAt), response.Usage, err)
	return response, err
}

func (c *observedLLMClient) Embed(ctx context.Context, request llm.EmbedRequest) (llm.EmbedResponse, error) {
	startedAt := time.Now()
	response, err := c.next.Embed(ctx, request)
	model := firstNonEmpty(request.Model)
	c.record(ctx, "embed", model, time.Since(startedAt), response.Usage, err)
	return response, err
}

func (c *observedLLMClient) record(ctx context.Context, operation, model string, duration time.Duration, usage llm.Usage, err error) {
	outcome := "success"
	args := []any{
		"request_id", middleware.GetReqID(ctx),
		"operation", operation,
		"model", firstNonEmpty(model, "unknown"),
		"duration_ms", duration.Milliseconds(),
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"total_tokens", usage.TotalTokens,
	}
	if err != nil {
		outcome = "error"
		args = append(args, "error", err)
	}
	if c.obs != nil {
		c.obs.observeLLMRequest(operation, model, outcome, duration, usage)
	}
	args = append(args, "outcome", outcome)
	if err != nil {
		c.logger.Warn("upstream llm request", args...)
		return
	}
	c.logger.Info("upstream llm request", args...)
}
