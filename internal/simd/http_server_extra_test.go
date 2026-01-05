package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestHTTPServerHandleRunsMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Test PUT /v1/runs (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/runs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test PUT /v1/runs/{id} (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/runs/"+rec.Run.Id, nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDStopMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test GET /v1/runs/{id}:stop (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+":stop", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDMetricsMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test POST /v1/runs/{id}/metrics (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDEmptyPath(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Test /v1/runs/ (empty run ID)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}

func TestHTTPServerWriteError(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	srv.writeError(rr, http.StatusInternalServerError, "test error")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "test error" {
		t.Fatalf("expected error 'test error', got %s", body["error"])
	}
}

func TestHTTPServerWriteJSON(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	testData := map[string]string{"key": "value"}
	srv.writeJSON(rr, http.StatusOK, testData)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["key"] != "value" {
		t.Fatalf("expected key 'value', got %s", body["key"])
	}
}
