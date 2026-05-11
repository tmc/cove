package coved

import (
	"embed"
	"encoding/json"
	"net/http"
)

//go:embed webui/index.html webui/app.js
var webUIFiles embed.FS

type UISnapshot struct {
	Status any
	Events []Event
}

func WebUIHandler(snapshot func() UISnapshot, metrics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveWebUIFile("webui/index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("/app.js", serveWebUIFile("webui/app.js", "text/javascript; charset=utf-8"))
	mux.Handle("/metrics", metrics)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		snap := snapshot()
		_ = json.NewEncoder(w).Encode(snap.Status)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		snap := snapshot()
		events := snap.Events
		if events == nil {
			events = []Event{}
		}
		_ = json.NewEncoder(w).Encode(events)
	})
	return mux
}

func serveWebUIFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := webUIFiles.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", contentType)
		_, _ = w.Write(data)
	}
}
