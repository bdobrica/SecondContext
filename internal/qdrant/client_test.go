package qdrant

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQdrantClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", MaxResponseBodyBytes+1)))
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	err := client.do(context.Background(), http.MethodGet, "/", nil, nil)
	var limitError *ResponseTooLargeError
	if !errors.As(err, &limitError) {
		t.Fatalf("client error = %#v, want ResponseTooLargeError", err)
	}
}

func TestQdrantClientDoesNotEchoErrorBody(t *testing.T) {
	const secret = "qdrant-secret-diagnostic"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status":{"error":"`+secret+`"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, httpClient: server.Client()}
	err := client.do(context.Background(), http.MethodGet, "/", nil, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("client error exposed upstream body: %v", err)
	}
}

func TestQdrantResponseBodyLimit(t *testing.T) {
	exact := bytes.Repeat([]byte("x"), MaxResponseBodyBytes)
	payload, err := readLimitedResponse(bytes.NewReader(exact), MaxResponseBodyBytes)
	if err != nil {
		t.Fatalf("read exact maximum: %v", err)
	}
	if len(payload) != MaxResponseBodyBytes {
		t.Fatalf("payload length = %d", len(payload))
	}

	_, err = readLimitedResponse(bytes.NewReader(append(exact, 'x')), MaxResponseBodyBytes)
	var limitError *ResponseTooLargeError
	if !errors.As(err, &limitError) {
		t.Fatalf("one-over error = %#v, want ResponseTooLargeError", err)
	}
}

func TestQdrantSearchLimit(t *testing.T) {
	client := &Client{}
	if _, err := client.SearchDense(context.Background(), "test", nil, MaxSearchLimit+1, nil); err == nil {
		t.Fatal("SearchDense accepted excessive limit")
	}
	if _, err := client.SearchSparse(context.Background(), "test", SparseVector{}, MaxSearchLimit+1, nil); err == nil {
		t.Fatal("SearchSparse accepted excessive limit")
	}
	if _, err := client.SearchHybrid(context.Background(), "test", nil, SparseVector{}, MaxSearchLimit+1, MaxSearchPrefetch, nil); err == nil {
		t.Fatal("SearchHybrid accepted excessive result limit")
	}
	if _, err := client.SearchHybrid(context.Background(), "test", nil, SparseVector{}, MaxSearchLimit, MaxSearchPrefetch+1, nil); err == nil {
		t.Fatal("SearchHybrid accepted excessive prefetch limit")
	}
}
