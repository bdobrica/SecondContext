package api

import (
	"embed"
	"net/http"
)

//go:embed inspector.html inspector.js
var inspectorAssets embed.FS

func (s *Server) handleLearningInspector(w http.ResponseWriter, r *http.Request) {
	name, contentType := "inspector.html", "text/html; charset=utf-8"
	if r.URL.Path == "/memory/inspector.js" {
		name = "inspector.js"
		contentType = "text/javascript; charset=utf-8"
	}
	content, _ := inspectorAssets.ReadFile(name)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	_, _ = w.Write(content)
}
