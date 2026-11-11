package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
)

type Usage struct {
	Input   int `json:"input_tokens"`
	Output  int `json:"output_tokens"`
	Total   int `json:"total_tokens"`
	Details struct {
		Cached int `json:"cached_tokens"`
		Write  int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}
type Result struct {
	Recent, Older *float64
	Outcome       string
	Usage         *Usage
	Duration      time.Duration
}

// Outcome labels are bounded constants. Never log upstream payloads/errors.
func (r Result) Suggestion() string {
	if r.Outcome != "success" || r.Recent == nil || r.Older == nil {
		return "recent"
	}
	if *r.Older >= 0.8 {
		return "older_reference"
	}
	if *r.Recent <= 0.2 && *r.Older <= 0.2 {
		return "standalone"
	}
	return "recent"
}
func Cost(u *Usage, r *config.TokenRates) *float64 {
	if u == nil || r == nil {
		return nil
	}
	v := (float64(u.Input-u.Details.Cached-u.Details.Write)*r.Input + float64(u.Details.Cached)*r.Cached + float64(u.Details.Write)*r.Write + float64(u.Output)*r.Output) / 1e6
	return &v
}

type Client struct {
	cfg  config.DecisionsConfig
	http *http.Client
}

func New(c config.DecisionsConfig) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{c, &http.Client{Transport: transport, Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Decide(ctx context.Context, r Request) (out Result) {
	started := time.Now()
	defer func() { out.Duration = time.Since(started) }()
	out.Outcome = "invalid_request"
	b, err := json.Marshal(r)
	if err != nil || len(b) > c.cfg.MaxInputBytes || c.cfg.APIKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.cfg.BaseURL, "/")+"/decisions", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		out.Outcome = "transport_error"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.Outcome = "timeout"
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out.Outcome = "upstream_error"
		switch resp.StatusCode {
		case 401, 403:
			out.Outcome = "access_denied"
		case 404, 400, 422:
			out.Outcome = "unsupported"
		case 429:
			out.Outcome = "rate_limited"
		}
		return
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(payload) > 16384 {
		out.Outcome = "invalid_response"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.Outcome = "timeout"
		}
		return
	}
	return parse(payload, c.cfg.Model)
}
func parse(b []byte, model string) Result {
	out := Result{Outcome: "invalid_response"}
	var wire struct {
		Model   string `json:"model"`
		Answers []struct {
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			Probability *float64 `json:"probability"`
		} `json:"answers"`
		Usage *struct {
			Input   *int `json:"input_tokens"`
			Output  *int `json:"output_tokens"`
			Total   *int `json:"total_tokens"`
			Details *struct {
				Cached *int `json:"cached_tokens"`
				Write  *int `json:"cache_write_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(b, &wire) != nil || wire.Model != model {
		return out
	}
	// Usage remains measurable for valid refusals/uncertainty. Missing or
	// contradictory usage is unknown and never fabricated as zero cost.
	u := wire.Usage
	if u != nil && u.Input != nil && u.Output != nil && u.Total != nil && u.Details != nil && u.Details.Cached != nil && u.Details.Write != nil {
		v := &Usage{Input: *u.Input, Output: *u.Output, Total: *u.Total}
		v.Details.Cached = *u.Details.Cached
		v.Details.Write = *u.Details.Write
		if v.Input >= 0 && v.Input <= 10000000 && v.Output >= 0 && v.Output <= 10000000 && v.Total == v.Input+v.Output && v.Details.Cached >= 0 && v.Details.Write >= 0 && v.Details.Cached+v.Details.Write <= v.Input {
			out.Usage = v
		}
	}
	if len(wire.Answers) != 2 {
		return out
	}
	names := []string{RecentQuestion, OlderQuestion}
	values := make([]*float64, 2)
	refused := false
	for i, a := range wire.Answers {
		if a.Name != names[i] {
			return out
		}
		if a.Type == "refusal" {
			refused = true
			continue
		}
		if a.Type != "predicate" || a.Probability == nil || math.IsNaN(*a.Probability) || math.IsInf(*a.Probability, 0) || *a.Probability < 0 || *a.Probability > 1 {
			return out
		}
		values[i] = a.Probability
	}
	if refused {
		out.Outcome = "refusal"
		return out
	}
	out.Recent, out.Older = values[0], values[1]
	out.Outcome = "success"
	if (*out.Recent > 0.2 && *out.Recent < 0.8) || (*out.Older > 0.2 && *out.Older < 0.8) {
		out.Outcome = "uncertain"
	}
	return out
}

// CheckAccess exercises the exact model/recipe without any customer content.
// Health and model discovery are not proof that this endpoint is usable.
func (c *Client) CheckAccess(ctx context.Context) Result {
	r, err := Build(c.cfg, Scope{Subject: "synthetic-access", Session: "probe"}, Evidence{Current: "What is two plus two?"})
	if err != nil {
		return Result{Outcome: "input_limit"}
	}
	return c.Decide(ctx, r)
}
