package config

import "testing"

func TestHistoryConfig(t *testing.T) {
	for _, key := range []string{"HISTORY_ENABLED", "HISTORY_RECENT_EXCHANGES", "HISTORY_PROMPT_INPUT_TOKENS", "HISTORY_RESERVED_OUTPUT_TOKENS"} {
		t.Setenv(key, "")
	}
	c, e := loadHistoryConfig()
	if e != nil || c.Enabled || c.RecentExchanges != 8 || c.PromptInputTokens != 32768 || c.ReservedOutputTokens != 4096 {
		t.Fatal(c, e)
	}
	for _, tc := range []struct{ key, value string }{{"HISTORY_ENABLED", "invalid"}, {"HISTORY_RECENT_EXCHANGES", "0"}, {"HISTORY_RECENT_EXCHANGES", "65"}, {"HISTORY_PROMPT_INPUT_TOKENS", "0"}, {"HISTORY_RESERVED_OUTPUT_TOKENS", "1000001"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := loadHistoryConfig(); err == nil {
				t.Fatal("accepted invalid history config")
			}
		})
	}
}
