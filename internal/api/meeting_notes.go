package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/knowledge"
)

// Explicit import is separate from automatic chat capture: reference material
// never silently becomes personal memory, and file bytes never enter a prompt.
func (s *Server) handleMeetingNote(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if authenticatedSubject(r.Context()) == "" {
		s.historyError(w, r, 401, "authentication_required")
		return
	}
	if s.dbPool == nil {
		s.historyError(w, r, 503, "learning_unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.historyError(w, r, 400, "invalid_note_upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	for key, values := range r.MultipartForm.Value {
		if len(values) != 1 || (key != "purpose" && key != "name" && key != "format" && key != "text" && key != "meeting_date") {
			s.historyError(w, r, 400, "invalid_note_field")
			return
		}
	}
	for key, files := range r.MultipartForm.File {
		if key != "file" || len(files) != 1 {
			s.historyError(w, r, 400, "invalid_note_file")
			return
		}
	}
	input := knowledge.NoteInput{Purpose: r.PostFormValue("purpose"), Name: strings.TrimSpace(r.PostFormValue("name")), Format: r.PostFormValue("format"), Text: r.PostFormValue("text")}
	date := r.PostFormValue("meeting_date")
	input.MeetingDate = date
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			s.historyError(w, r, 400, "invalid_meeting_date")
			return
		}
	}
	if input.Format == "" {
		input.Format = "text"
	}
	if (input.Purpose != "personal" && input.Purpose != "reference") || input.Name == "" || len(input.Name) > 500 {
		s.historyError(w, r, 400, "invalid_note_choice")
		return
	}
	if files := r.MultipartForm.File["file"]; len(files) > 0 {
		if _, ok := r.MultipartForm.Value["text"]; ok {
			s.historyError(w, r, 400, "ambiguous_note_input")
			return
		}
		file, err := files[0].Open()
		if err != nil {
			s.historyError(w, r, 400, "invalid_note_file")
			return
		}
		defer file.Close()
		input.File, err = io.ReadAll(io.LimitReader(file, 8<<20+1))
		input.Filename = files[0].Filename
		if err != nil || len(input.File) > 8<<20 || input.Filename == "" {
			s.historyError(w, r, 400, "invalid_note_file")
			return
		}
	} else if strings.TrimSpace(input.Text) == "" || len(input.Text) > 65536 {
		s.historyError(w, r, 400, "invalid_note_text")
		return
	}
	user, _, err := s.actorUser(r.Context())
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if input.Purpose == "personal" {
		var enabled bool
		err = s.dbPool.QueryRow(r.Context(), `SELECT COALESCE((SELECT enabled FROM learning_settings WHERE user_id=$1),false)`, user.ID).Scan(&enabled)
		if err != nil {
			s.writeMemoryError(w, r, err)
			return
		}
		if !enabled {
			s.historyError(w, r, 409, "learning_paused")
			return
		}
	}
	provider, ok := s.knowledge.(knowledge.NoteIngester)
	if !ok {
		s.historyError(w, r, 503, "note_parser_not_configured")
		return
	}
	raw, _ := json.Marshal(input)
	digest := sha256.Sum256(raw)
	result, err := provider.IngestNote(r.Context(), authenticatedSubject(r.Context()), input, "note:"+hex.EncodeToString(digest[:]))
	if err != nil {
		s.historyError(w, r, 502, "note_"+knowledge.ErrorCode(err))
		return
	}
	if input.Purpose == "reference" {
		writeJSON(w, 202, result)
		return
	}
	// The normalized content/date identify the import, independent of filename or
	// title. A repeated import cannot recreate individually forgotten observations.
	digest = sha256.Sum256([]byte(date + "\x00" + result.Text))
	meta, _ := json.Marshal(map[string]string{"title": input.Name, "filename": input.Filename, "format": result.Format, "meeting_date": date})
	var id, status string
	err = s.dbPool.QueryRow(r.Context(), `INSERT INTO learning_captures(user_id,source_key,source_text,status,source_kind,source_metadata)
 VALUES($1,$2,$3,'pending','meeting_note',$4) ON CONFLICT(user_id,source_key) DO UPDATE SET source_key=EXCLUDED.source_key
 RETURNING id::text,status`, user.ID, "note:"+hex.EncodeToString(digest[:]), result.Text, meta).Scan(&id, &status)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	writeJSON(w, 202, map[string]any{"purpose": "personal", "capture_id": id, "status": status})
}
