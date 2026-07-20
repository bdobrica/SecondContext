package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONBodyBoundaries(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		err := decodeTestJSONBody(t, "", false)
		assertRequestBodyError(t, err, http.StatusBadRequest, "empty_body")
	})

	t.Run("trailing value", func(t *testing.T) {
		err := decodeTestJSONBody(t, `{"value":"ok"} {"value":"extra"}`, false)
		assertRequestBodyError(t, err, http.StatusBadRequest, "trailing_json")
	})

	t.Run("malformed", func(t *testing.T) {
		err := decodeTestJSONBody(t, `{"value":`, false)
		assertRequestBodyError(t, err, http.StatusBadRequest, "invalid_json")
	})

	t.Run("unknown allowed for compatibility", func(t *testing.T) {
		if err := decodeTestJSONBody(t, `{"value":"ok","future_field":true}`, false); err != nil {
			t.Fatalf("decode compatible body: %v", err)
		}
	})

	t.Run("unknown rejected for internal API", func(t *testing.T) {
		err := decodeTestJSONBody(t, `{"value":"ok","unknown":true}`, true)
		assertRequestBodyError(t, err, http.StatusBadRequest, "invalid_json")
	})

	t.Run("exact maximum", func(t *testing.T) {
		body := `{"value":"` + strings.Repeat("a", maxJSONRequestBodyBytes-len(`{"value":""}`)) + `"}`
		if len(body) != maxJSONRequestBodyBytes {
			t.Fatalf("test body length = %d", len(body))
		}
		if err := decodeTestJSONBody(t, body, false); err != nil {
			t.Fatalf("decode exact maximum: %v", err)
		}
	})

	t.Run("one byte over maximum", func(t *testing.T) {
		body := `{"value":"` + strings.Repeat("a", maxJSONRequestBodyBytes-len(`{"value":""}`)+1) + `"}`
		err := decodeTestJSONBody(t, body, false)
		assertRequestBodyError(t, err, http.StatusRequestEntityTooLarge, "request_too_large")
	})
}

func TestResponseRequestFieldLimits(t *testing.T) {
	t.Run("exact input maximum", func(t *testing.T) {
		if err := validateResponseRequestSize(createResponseRequest{Input: bytes.Repeat([]byte("x"), maxResponseInputBytes)}); err != nil {
			t.Fatalf("validate exact input maximum: %v", err)
		}
	})

	t.Run("input one over maximum", func(t *testing.T) {
		err := validateResponseRequestSize(createResponseRequest{Input: bytes.Repeat([]byte("x"), maxResponseInputBytes+1)})
		assertRequestBodyError(t, err, http.StatusBadRequest, "input_too_large")
	})

	t.Run("metadata over maximum", func(t *testing.T) {
		err := validateResponseRequestSize(createResponseRequest{Metadata: map[string]any{"value": strings.Repeat("x", maxRequestMetadataBytes)}})
		assertRequestBodyError(t, err, http.StatusBadRequest, "metadata_too_large")
	})

	t.Run("exact metadata maximum", func(t *testing.T) {
		const envelopeBytes = len(`{"value":""}`)
		metadata := map[string]any{"value": strings.Repeat("x", maxRequestMetadataBytes-envelopeBytes)}
		if err := validateResponseRequestSize(createResponseRequest{Metadata: metadata}); err != nil {
			t.Fatalf("validate exact metadata maximum: %v", err)
		}
	})
}

func TestAPIResultLimits(t *testing.T) {
	server := &Server{}
	tests := []struct {
		name    string
		request *http.Request
		handler http.HandlerFunc
	}{
		{
			name:    "memory list zero",
			request: httptest.NewRequest(http.MethodGet, "/memory?limit=0", nil),
			handler: server.handleListMemories,
		},
		{
			name:    "belief list excessive",
			request: httptest.NewRequest(http.MethodGet, "/debug/beliefs?limit=101", nil),
			handler: server.handleListDebugBeliefs,
		},
		{
			name:    "search zero",
			request: httptest.NewRequest(http.MethodPost, "/memory/search", strings.NewReader(`{"query":"test","limit":0}`)),
			handler: server.handleMemorySearch,
		},
		{
			name:    "search excessive",
			request: httptest.NewRequest(http.MethodPost, "/memory/search", strings.NewReader(`{"query":"test","limit":51}`)),
			handler: server.handleMemorySearch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler.ServeHTTP(recorder, test.request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var response apiErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error.Code != "invalid_limit" {
				t.Fatalf("code = %q, want invalid_limit", response.Error.Code)
			}
		})
	}
}

func decodeTestJSONBody(t *testing.T, body string, strict bool) error {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	var destination struct {
		Value string `json:"value"`
	}
	return decodeJSONBody(recorder, request, &destination, strict)
}

func assertRequestBodyError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var bodyError *requestBodyError
	if !errors.As(err, &bodyError) {
		t.Fatalf("error = %v, want requestBodyError", err)
	}
	if bodyError.status != status || bodyError.code != code {
		t.Fatalf("error = status %d code %q, want status %d code %q", bodyError.status, bodyError.code, status, code)
	}
}
