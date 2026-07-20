package config

import (
	"os"
	"strings"
	"testing"
)

func TestParseBoolConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		value    *string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{name: "unset uses false fallback", fallback: false, want: false},
		{name: "unset uses true fallback", fallback: true, want: true},
		{name: "empty uses fallback", value: stringPointer("  "), fallback: true, want: true},
		{name: "one", value: stringPointer("1"), want: true},
		{name: "lowercase t", value: stringPointer("t"), want: true},
		{name: "uppercase T", value: stringPointer("T"), want: true},
		{name: "uppercase true", value: stringPointer("TRUE"), want: true},
		{name: "lowercase true", value: stringPointer("true"), want: true},
		{name: "title true", value: stringPointer("True"), want: true},
		{name: "zero", value: stringPointer("0"), want: false},
		{name: "lowercase f", value: stringPointer("f"), want: false},
		{name: "uppercase F", value: stringPointer("F"), want: false},
		{name: "uppercase false", value: stringPointer("FALSE"), want: false},
		{name: "lowercase false", value: stringPointer("false"), want: false},
		{name: "title false", value: stringPointer("False"), want: false},
		{name: "invalid", value: stringPointer("treu"), wantErr: true},
	}

	const key = "SECOND_CONTEXT_TEST_BOOL"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setOptionalEnv(t, key, test.value)
			got, err := parseBool(key, test.fallback)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseBool() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("parseBool() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLoadRejectsInvalidBooleanConfiguration(t *testing.T) {
	for _, key := range []string{"AUTH_ENABLED", "HTTP_METRICS_ENABLED", "POSTGRES_ENABLED"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("AUTH_ENABLED", "false")
			t.Setenv("HTTP_METRICS_ENABLED", "true")
			t.Setenv("POSTGRES_ENABLED", "false")
			t.Setenv(key, "not-a-boolean")

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted invalid %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error %q does not identify %s", err, key)
			}
		})
	}
}

func TestParseAuthTokensConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		want      []AuthTokenConfig
		wantError bool
	}{
		{name: "unset", value: "", want: nil},
		{name: "blank", value: "   ", want: nil},
		{name: "bare token", value: "secret-token", wantError: true},
		{name: "blank subject", value: "=secret-token", wantError: true},
		{name: "blank token", value: "tenant-a=", wantError: true},
		{name: "empty entry", value: "tenant-a=token-a,", wantError: true},
		{name: "duplicate normalized subject", value: "tenant-a=token-a, tenant-a =token-b", wantError: true},
		{name: "duplicate normalized token", value: "tenant-a=token-a,tenant-b= token-a ", wantError: true},
		{
			name:  "valid multi-user",
			value: "tenant-a=token-a, tenant-b=token=b",
			want: []AuthTokenConfig{
				{Subject: "tenant-a", Token: "token-a"},
				{Subject: "tenant-b", Token: "token=b"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAuthTokens(test.value)
			if (err != nil) != test.wantError {
				t.Fatalf("parseAuthTokens() error = %v, wantError %v", err, test.wantError)
			}
			if err != nil {
				if strings.Contains(err.Error(), "secret-token") ||
					strings.Contains(err.Error(), "token-a") ||
					strings.Contains(err.Error(), "token-b") {
					t.Fatalf("configuration error disclosed a token value: %v", err)
				}
				return
			}
			if len(got) != len(test.want) {
				t.Fatalf("parseAuthTokens() = %#v, want %#v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("parseAuthTokens()[%d] = %#v, want %#v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func setOptionalEnv(t *testing.T, key string, value *string) {
	t.Helper()

	original, existed := os.LookupEnv(key)
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, original)
			return
		}
		_ = os.Unsetenv(key)
	})

	if value == nil {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		return
	}
	if err := os.Setenv(key, *value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}
