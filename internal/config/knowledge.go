package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

func loadKnowledgeConfig() (KnowledgeConfig, error) {
	enabled, err := parseBool("KNOWLEDGE_ENABLED", false)
	if err != nil {
		return KnowledgeConfig{}, err
	}
	c := KnowledgeConfig{Enabled: enabled}
	// Disabled means entirely optional, including credentials and endpoint validation.
	if !enabled {
		return c, nil
	}
	c.URL = getEnv("KNOWLEDGE_URL", "http://localhost:8090")
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("KNOWLEDGE_URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	c.Timeout, err = parseDuration("KNOWLEDGE_TIMEOUT", "5s")
	if err != nil {
		return c, err
	}
	if c.Timeout <= 0 || c.Timeout > 20*time.Second {
		return c, fmt.Errorf("KNOWLEDGE_TIMEOUT must be positive and at most 20s")
	}
	c.Limit, err = parseInt("KNOWLEDGE_LIMIT", 4)
	if err != nil {
		return c, err
	}
	if c.Limit < 1 || c.Limit > 20 {
		return c, fmt.Errorf("KNOWLEDGE_LIMIT must be between 1 and 20")
	}
	c.Tokens, err = parseAuthTokens(getEnv("KNOWLEDGE_SUBJECT_TOKENS", ""))
	if err != nil || len(c.Tokens) == 0 {
		return c, fmt.Errorf("KNOWLEDGE_SUBJECT_TOKENS requires unique subject=token entries")
	}
	for _, entry := range c.Tokens {
		if strings.ContainsAny(entry.Token, "\r\n\t ") {
			return c, fmt.Errorf("KNOWLEDGE_SUBJECT_TOKENS contains an invalid bearer credential")
		}
	}
	return c, nil
}
