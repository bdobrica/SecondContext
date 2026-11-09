package config

import "fmt"

// Enabled gates new managed generations and local captures. Finalize/abandon and
// erasure remain available for already reserved turns when it is disabled.
type HistoryConfig struct {
	Enabled              bool
	RecentExchanges      int
	PromptInputTokens    int
	ReservedOutputTokens int
}

func loadHistoryConfig() (HistoryConfig, error) {
	enabled, err := parseBool("HISTORY_ENABLED", false)
	if err != nil {
		return HistoryConfig{}, err
	}
	recent, err := parseInt("HISTORY_RECENT_EXCHANGES", 8)
	if err != nil {
		return HistoryConfig{}, err
	}
	input, err := parseInt("HISTORY_PROMPT_INPUT_TOKENS", 32768)
	if err != nil {
		return HistoryConfig{}, err
	}
	output, err := parseInt("HISTORY_RESERVED_OUTPUT_TOKENS", 4096)
	if err != nil {
		return HistoryConfig{}, err
	}
	if recent < 1 || recent > 64 || input < 1 || input > 1000000 || output < 1 || output > 1000000 {
		return HistoryConfig{}, fmt.Errorf("invalid history limits")
	}
	return HistoryConfig{enabled, recent, input, output}, nil
}
