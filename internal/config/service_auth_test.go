package config

import (
	"strings"
	"testing"
)

func TestServiceTokenConfiguration(t *testing.T) {
	for _, tc := range []struct {
		service, user string
		enabled       bool
		bad           bool
	}{
		{"oria:=service-secret", "", true, false},
		{"oria:=service-secret", "alice=user-secret", true, false},
		{"oria:=service-secret", "", false, true},
		{"=service-secret", "", true, true},
		{"oria=service-secret", "", true, true},
		{"oria:x:=service-secret", "", true, true},
		{"oria:=service-secret,other:=service-secret", "", true, true},
		{"oria:=service-secret", "alice=service-secret", true, true},
		{"oria:=service-secret", "oria:someone=user-secret", true, true},
	} {
		t.Run(tc.service+tc.user, func(t *testing.T) {
			t.Setenv("AUTH_ENABLED", map[bool]string{true: "true", false: "false"}[tc.enabled])
			t.Setenv("AUTH_SERVICE_TOKENS", tc.service)
			t.Setenv("AUTH_BEARER_TOKENS", tc.user)
			cfg, err := Load()
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v bad=%v", err, tc.bad)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("secret leaked")
			}
			if err == nil && len(cfg.Auth.ServiceTokens) != 1 {
				t.Fatal("service token missing")
			}
		})
	}
}
