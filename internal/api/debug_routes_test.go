package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bdobrica/SecondContext/internal/config"
)

func TestDebugRoutesNotExposedOutsideDev(t *testing.T) {
	server := NewServerWithClient(
		config.Config{App: config.AppConfig{Name: "salience-graph", Env: "production"}},
		slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)),
		nil,
		&fakeLLMClient{},
	)

	testCases := []struct {
		name   string
		method string
		path   string
	}{
		{name: "context", method: http.MethodGet, path: "/debug/context?input=test"},
		{name: "beliefs", method: http.MethodGet, path: "/debug/beliefs?user_external_id=test-user"},
		{name: "person inspect", method: http.MethodGet, path: "/debug/person/test-person"},
		{name: "person update", method: http.MethodPut, path: "/debug/person/test-person"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(testCase.method, testCase.path, nil)

			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("expected status %d, got %d body=%s", http.StatusNotFound, recorder.Code, recorder.Body.String())
			}
		})
	}
}
