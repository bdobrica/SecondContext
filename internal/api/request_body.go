package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const (
	maxJSONRequestBodyBytes = 1 << 20
	maxResponseInputBytes   = 256 << 10
	maxRequestMetadataBytes = 64 << 10
	maxListResults          = 100
	maxSearchResults        = 50
)

type requestBodyError struct {
	status  int
	message string
	code    string
}

func (e *requestBodyError) Error() string {
	return e.message
}

func (s *Server) decodeJSONRequest(w http.ResponseWriter, r *http.Request, destination any, disallowUnknownFields bool) bool {
	err := decodeJSONBody(w, r, destination, disallowUnknownFields)
	if err == nil {
		return true
	}

	var bodyError *requestBodyError
	if !errors.As(err, &bodyError) {
		bodyError = &requestBodyError{status: http.StatusBadRequest, message: "invalid request body", code: "invalid_json"}
	}
	s.writeAPIError(w, r, bodyError.status, bodyError.message, "invalid_request_error", bodyError.code, "")
	return false
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any, disallowUnknownFields bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(destination); err != nil {
		return classifyJSONBodyError(err)
	}

	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return &requestBodyError{status: http.StatusBadRequest, message: "request body must contain exactly one JSON value", code: "trailing_json"}
	}
	return classifyJSONBodyError(err)
}

func classifyJSONBodyError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &requestBodyError{status: http.StatusRequestEntityTooLarge, message: "request body exceeds 1 MiB limit", code: "request_too_large"}
	}
	if errors.Is(err, io.EOF) {
		return &requestBodyError{status: http.StatusBadRequest, message: "request body is required", code: "empty_body"}
	}
	return &requestBodyError{status: http.StatusBadRequest, message: "invalid request body", code: "invalid_json"}
}

func validateResponseRequestSize(request createResponseRequest) *requestBodyError {
	if len(request.Input) > maxResponseInputBytes {
		return &requestBodyError{status: http.StatusBadRequest, message: "input exceeds 256 KiB limit", code: "input_too_large"}
	}
	if request.Metadata != nil {
		encoded, err := json.Marshal(request.Metadata)
		if err != nil || len(encoded) > maxRequestMetadataBytes {
			return &requestBodyError{status: http.StatusBadRequest, message: "metadata exceeds 64 KiB limit", code: "metadata_too_large"}
		}
	}
	return nil
}
