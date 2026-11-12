package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"unicode/utf8"
)

type NoteInput struct {
	Purpose, Name, Format, Text, Filename, MeetingDate string
	File                                               []byte
}

type NoteResult struct {
	Purpose  string `json:"purpose"`
	Text     string `json:"text,omitempty"`
	Format   string `json:"format,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Status   string `json:"status,omitempty"`
}

// NoteIngester is optional: search-only providers remain valid.
type NoteIngester interface {
	IngestNote(context.Context, string, NoteInput, string) (NoteResult, error)
}

func (p *HTTPProvider) IngestNote(ctx context.Context, subject string, input NoteInput, key string) (NoteResult, error) {
	fail := func(code string) (NoteResult, error) { return NoteResult{}, &Error{Code: code} }
	if p.tokens[subject] == "" {
		return fail("not_configured")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for key, value := range map[string]string{"purpose": input.Purpose, "name": input.Name, "format": input.Format, "meeting_date": input.MeetingDate} {
		if err := w.WriteField(key, value); err != nil {
			return fail("invalid_request")
		}
	}
	if input.Filename != "" {
		part, err := w.CreateFormFile("file", input.Filename)
		if err != nil {
			return fail("invalid_request")
		}
		_, _ = part.Write(input.File)
	} else {
		_ = w.WriteField("text", input.Text)
	}
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(p.endpoint, "/search")+"/notes", &body)
	if err != nil {
		return fail("unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+p.tokens[subject])
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Idempotency-Key", key)
	resp, err := p.client.Do(req)
	if err != nil {
		return fail("unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 400 || resp.StatusCode == 413 || resp.StatusCode == 422 {
			return fail("invalid_note")
		}
		return fail("unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	var result NoteResult
	if err != nil || len(raw) > MaxResponseBytes || !utf8.Valid(raw) || json.Unmarshal(raw, &result) != nil || result.Purpose != input.Purpose {
		return fail("invalid_response")
	}
	if input.Purpose == "personal" {
		if strings.TrimSpace(result.Text) == "" || len(result.Text) > 65536 || strings.ContainsRune(result.Text, 0) {
			return fail("invalid_response")
		}
	} else if !(Filters{SourceIDs: []string{result.SourceID}}).Valid() || result.SourceID == "" {
		return fail("invalid_response")
	}
	return result, nil
}
