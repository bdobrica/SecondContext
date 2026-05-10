package db

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

var whitespacePattern = regexp.MustCompile(`\s+`)

func normalizeName(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return ""
	}

	return whitespacePattern.ReplaceAllString(trimmed, " ")
}

func normalizeJSON(value json.RawMessage) []byte {
	if len(bytes.TrimSpace(value)) == 0 {
		return []byte("{}")
	}

	return value
}

func scanJSON(value []byte) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("{}")
	}

	return json.RawMessage(value)
}
