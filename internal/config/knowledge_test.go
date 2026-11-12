package config

import (
	"strings"
	"testing"
	"time"
)

func TestKnowledgeConfigOptionalAndValidated(t *testing.T) {
	t.Setenv("KNOWLEDGE_SHARED_TOKEN", "")
	t.Setenv("KNOWLEDGE_ENABLED", "false")
	t.Setenv("KNOWLEDGE_URL", ":invalid")
	t.Setenv("KNOWLEDGE_SUBJECT_TOKENS", "broken")
	c, err := loadKnowledgeConfig()
	if err != nil || c.Enabled {
		t.Fatalf("optional config: %v", err)
	}
	t.Setenv("KNOWLEDGE_ENABLED", "true")
	t.Setenv("KNOWLEDGE_URL", "http://knowledge:8090")
	t.Setenv("KNOWLEDGE_SUBJECT_TOKENS", "alice=scoped-secret")
	t.Setenv("KNOWLEDGE_TIMEOUT", "")
	t.Setenv("KNOWLEDGE_LIMIT", "")
	c, err = loadKnowledgeConfig()
	if err != nil || c.Timeout != 5*time.Second || c.Limit != 4 {
		t.Fatalf("defaults: %v %v", c, err)
	}
	for _, test := range []struct{ key, value string }{
		{"KNOWLEDGE_ENABLED", "perhaps"}, {"KNOWLEDGE_URL", "file:///tmp/secret"}, {"KNOWLEDGE_URL", "http://user:secret@example.com"},
		{"KNOWLEDGE_URL", "http://example.com?a=b"}, {"KNOWLEDGE_TIMEOUT", "0s"}, {"KNOWLEDGE_TIMEOUT", "21s"}, {"KNOWLEDGE_LIMIT", "21"},
		{"KNOWLEDGE_SUBJECT_TOKENS", ""}, {"KNOWLEDGE_SUBJECT_TOKENS", "alice=scoped-secret,alice=other"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			_, err := loadKnowledgeConfig()
			if err == nil || strings.Contains(err.Error(), "scoped-secret") {
				t.Fatalf("invalid config: %v", err)
			}
		})
	}
}

func TestSharedKnowledgeConfiguration(t *testing.T) {
	t.Setenv("KNOWLEDGE_ENABLED", "true")
	t.Setenv("KNOWLEDGE_URL", "http://knowledge:8090")
	t.Setenv("KNOWLEDGE_TIMEOUT", "5s")
	t.Setenv("KNOWLEDGE_LIMIT", "4")
	t.Setenv("KNOWLEDGE_SUBJECT_TOKENS", "")
	t.Setenv("KNOWLEDGE_SHARED_TOKEN", "shared-secret")
	c, err := loadKnowledgeConfig()
	if err != nil || c.SharedToken != "shared-secret" || len(c.Tokens) != 0 {
		t.Fatalf("shared-only: %+v", err)
	}
	t.Setenv("KNOWLEDGE_SUBJECT_TOKENS", "alice=private-secret")
	if _, err = loadKnowledgeConfig(); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"private-secret", "bad\nsecret", "bad secret"} {
		t.Setenv("KNOWLEDGE_SHARED_TOKEN", token)
		if _, err = loadKnowledgeConfig(); err == nil || strings.Contains(err.Error(), token) {
			t.Fatal("unsafe shared credential configuration", err)
		}
	}
}
