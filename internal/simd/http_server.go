package simd

import (
	"encoding/json"
	"net/http"
	"time"
)

type HTTPServer struct {
	mux *http.ServeMux
}

func NewHTTPServer() *HTTPServer {
	s := &HTTPServer{
		mux: http.NewServeMux(),
	}

	s.mux.HandleFunc("/healthz", s.handleHealthz)

	// Orchestrator plane placeholders (Milestone 1).
	s.mux.HandleFunc("/v1/runs", s.handleNotImplemented)
	s.mux.HandleFunc("/v1/runs/", s.handleNotImplemented)

	return s
}

func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

func (s *HTTPServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		http.Error(w, `{"error":"encode failed"}`, http.StatusInternalServerError)
	}
}

func (s *HTTPServer) handleNotImplemented(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error": "not implemented",
	}); err != nil {
		http.Error(w, `{"error":"encode failed"}`, http.StatusInternalServerError)
	}
}
