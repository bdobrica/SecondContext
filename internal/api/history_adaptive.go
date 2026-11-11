package api

import (
	"encoding/json"
	"github.com/bdobrica/SecondContext/internal/decisions"
)

func (s *Server) adaptiveConfigHash(baseline []byte) []byte {
	c := s.cfg.Decisions
	// Flags and credentials can rotate; interpretation-changing settings cannot.
	b, _ := json.Marshal(map[string]any{"baseline": baseline, "selection": decisions.AdaptiveRecipe,
		"threshold": decisions.StandaloneThreshold, "model": c.Model, "recipe": c.Recipe,
		"base_url": c.BaseURL, "input_limit": c.MaxInputBytes, "segment": c.SegmentExchanges, "timeout": c.Timeout})
	h, _ := historyHash(b)
	return h
}
