package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPServerHealthz(t *testing.T) {
	srv := NewHTTPServer()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
	if body["timestamp"] == "" {
		t.Fatalf("expected timestamp to be set")
	}
}

func TestHTTPServerNotImplemented(t *testing.T) {
	srv := NewHTTPServer()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected status 501, got %d", rr.Code)
	}
}
