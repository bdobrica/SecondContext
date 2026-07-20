package llm

import (
	"bytes"
	"errors"
	"testing"
)

func TestOpenAIResponseBodyLimit(t *testing.T) {
	exact := bytes.Repeat([]byte("x"), MaxOpenAIResponseBodyBytes)
	payload, err := readLimitedResponse(bytes.NewReader(exact))
	if err != nil {
		t.Fatalf("read exact maximum: %v", err)
	}
	if len(payload) != MaxOpenAIResponseBodyBytes {
		t.Fatalf("payload length = %d", len(payload))
	}

	_, err = readLimitedResponse(bytes.NewReader(append(exact, 'x')))
	var clientError *Error
	if !errors.As(err, &clientError) || clientError.Code != "response_too_large" {
		t.Fatalf("one-over error = %#v, want response_too_large", err)
	}
}
