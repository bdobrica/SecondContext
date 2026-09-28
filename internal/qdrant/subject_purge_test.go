package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteSubjectRequiresCompletedAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		ok     bool
	}{
		{200, `{"result":{"status":"completed"}}`, true},
		{200, `{"result":{"status":"acknowledged"}}`, false},
		{200, `{}`, false}, {404, `{"status":{"error":"Collection test not found"}}`, true},
		{404, `not found`, false}, {503, `{"status":{"error":"Collection test not found"}}`, false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("wait") != "true" {
				t.Error("missing wait")
			}
			var body struct {
				Filter struct {
					Must []struct {
						Key   string
						Match struct{ Value string }
					}
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Filter.Must) != 1 || body.Filter.Must[0].Key != "user_id" || body.Filter.Must[0].Match.Value != "internal-user" {
				t.Error("wrong scope")
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{baseURL: server.URL, httpClient: server.Client()}
		err := c.DeleteSubject(context.Background(), "test", "internal-user")
		server.Close()
		if (err == nil) != tc.ok {
			t.Fatalf("status %d: %v", tc.status, err)
		}
	}
}

func TestUpsertsCompleteBeforeSubjectFenceRelease(t *testing.T) {
	for _, status := range []string{"completed", "acknowledged"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("wait") != "true" {
				t.Error("upsert must wait")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": status}})
		}))
		c := &Client{baseURL: server.URL, httpClient: server.Client(), denseVector: "dense"}
		err := c.UpsertPoint(context.Background(), "test", Point{ID: "id", DenseVector: []float64{1}})
		server.Close()
		if (err == nil) != (status == "completed") {
			t.Fatalf("status %s: %v", status, err)
		}
	}
}

func TestEnsureCollectionClassifiesSanitizedConflict(t *testing.T) {
	for _, status := range []int{409, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"status":{"error":"Collection test already exists!"}}`))
		}))
		c := &Client{baseURL: server.URL, httpClient: server.Client()}
		err := c.EnsureCollection(context.Background(), "test", 3)
		server.Close()
		if (err == nil) != (status == 409) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
