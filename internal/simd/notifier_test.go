package simd

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestValidateCallbackURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errType error
	}{
		{
			name:    "valid external URL",
			url:     "https://example.com/callback",
			wantErr: false,
		},
		{
			name:    "valid localhost for development",
			url:     "http://localhost:8000/callback",
			wantErr: false,
		},
		{
			name:    "invalid scheme",
			url:     "ftp://example.com/callback",
			wantErr: true,
			errType: ErrInvalidURL,
		},
		{
			name:    "missing hostname",
			url:     "http:///callback",
			wantErr: true,
			errType: ErrInvalidURL,
		},
		{
			name:    "metadata endpoint - IP",
			url:     "http://169.254.169.254/metadata",
			wantErr: true,
			errType: ErrMetadataEndpoint,
		},
		{
			name:    "metadata endpoint - hostname",
			url:     "http://metadata.google.internal/metadata",
			wantErr: true,
			errType: ErrMetadataEndpoint,
		},
		{
			name:    "wildcard address",
			url:     "http://0.0.0.0:8000/callback",
			wantErr: true,
			errType: ErrInternalHost,
		},
		{
			name:    "direct loopback IP",
			url:     "http://127.0.0.1:8000/callback",
			wantErr: true,
			errType: ErrInternalHost,
		},
		{
			name:    "URL with run_id template",
			url:     "http://localhost:8000/callback/{run_id}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCallbackURL(tt.url, nil)
			if tt.wantErr {
				if err == nil {
					t.Errorf("validateCallbackURL() expected error but got nil")
					return
				}
				if tt.errType != nil {
					if !isErrorType(err, tt.errType) {
						t.Errorf("validateCallbackURL() error type = %v, want %v", err, tt.errType)
					}
				}
			} else {
				if err != nil {
					t.Errorf("validateCallbackURL() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestValidateCallbackURL_Whitelist(t *testing.T) {
	// Hostname in whitelist allows even loopback IP
	t.Run("hostname in whitelist allows 127.0.0.1", func(t *testing.T) {
		err := validateCallbackURL("http://127.0.0.1:8000/callback", []string{"127.0.0.1"})
		if err != nil {
			t.Errorf("validateCallbackURL with 127.0.0.1 in whitelist: expected nil, got %v", err)
		}
	})
	// Resolved IP in whitelist allows (e.g. hostname resolves to private IP)
	t.Run("resolved IP in whitelist allows private", func(t *testing.T) {
		// localhost resolves to 127.0.0.1; whitelist the IP
		err := validateCallbackURL("http://localhost:8000/callback", []string{"127.0.0.1"})
		if err != nil {
			t.Errorf("validateCallbackURL localhost with 127.0.0.1 in whitelist: expected nil, got %v", err)
		}
	})
	// Not in whitelist, private IP still rejected
	t.Run("private IP not in whitelist still rejected", func(t *testing.T) {
		err := validateCallbackURL("http://127.0.0.1:8000/callback", []string{"10.0.0.1"})
		if err == nil {
			t.Error("validateCallbackURL with 127.0.0.1 not in whitelist: expected error, got nil")
		}
		if err != nil && !isErrorType(err, ErrInternalHost) {
			t.Errorf("validateCallbackURL: expected ErrInternalHost, got %v", err)
		}
	})
	// Empty/nil whitelist unchanged behavior
	t.Run("nil whitelist rejects loopback IP", func(t *testing.T) {
		err := validateCallbackURL("http://127.0.0.1:8000/callback", nil)
		if err == nil {
			t.Error("validateCallbackURL with nil whitelist: expected error for 127.0.0.1, got nil")
		}
	})
}

func isErrorType(err error, target error) bool {
	if err == nil || target == nil {
		return err == target
	}
	// Check if errors are the same or if err wraps target
	if errors.Is(err, target) {
		return true
	}
	// Check error message contains target error
	errMsg := err.Error()
	targetMsg := target.Error()
	return errMsg == targetMsg || (len(errMsg) >= len(targetMsg) && errMsg[:len(targetMsg)] == targetMsg)
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"public IP", "8.8.8.8", false},
		{"RFC 1918 - 10.0.0.0/8", "10.0.0.1", true},
		{"RFC 1918 - 172.16.0.0/12", "172.16.0.1", true},
		{"RFC 1918 - 192.168.0.0/16", "192.168.1.1", true},
		{"link-local", "169.254.0.1", true},
		{"loopback", "127.0.0.1", true},
		{"IPv6 loopback", "::1", true},
		{"IPv6 unique local", "fc00::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP: %s", tt.ip)
			}
			got := isPrivateIP(ip)
			if got != tt.want {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestNotifierNotify_Success(t *testing.T) {
	// Create a test server that accepts POST requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var payload NotificationPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode payload: %v", err)
		}

		if payload.RunID != "test-run-123" {
			t.Errorf("expected RunID test-run-123, got %s", payload.RunID)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Convert IP-based URL to localhost hostname for SSRF validation
	// The server listens on 127.0.0.1 but we want to validate with localhost hostname
	serverURL, _ := url.Parse(server.URL)
	callbackURL := "http://localhost:" + serverURL.Port() + "/callback"

	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              "test-run-123",
			Status:          simulationv1.RunStatus_RUN_STATUS_COMPLETED,
			CreatedAtUnixMs: time.Now().UnixMilli(),
			EndedAtUnixMs:   time.Now().UnixMilli(),
		},
		Input: &simulationv1.RunInput{
			CallbackUrl: callbackURL,
		},
	}

	// Notify should return immediately (async)
	notifier.Notify(callbackURL, "test-secret", rec)

	// Wait a bit for the async notification to complete
	time.Sleep(200 * time.Millisecond)
}

func TestNotifierNotify_WithSecret(t *testing.T) {
	receivedSecretCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecretCh <- r.Header.Get("X-Simulation-Callback-Secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Convert IP-based URL to localhost hostname for SSRF validation
	serverURL, _ := url.Parse(server.URL)
	callbackURL := "http://localhost:" + serverURL.Port()

	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              "test-run-123",
			Status:          simulationv1.RunStatus_RUN_STATUS_COMPLETED,
			CreatedAtUnixMs: time.Now().UnixMilli(),
		},
		Input: &simulationv1.RunInput{
			CallbackUrl:    callbackURL + "/callback",
			CallbackSecret: "my-secret-123",
		},
	}

	notifier.Notify(callbackURL+"/callback", "my-secret-123", rec)

	select {
	case receivedSecret := <-receivedSecretCh:
		if receivedSecret != "my-secret-123" {
			t.Errorf("expected secret 'my-secret-123', got '%s'", receivedSecret)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestNotifierNotify_URLTemplateSubstitution(t *testing.T) {
	receivedPathCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPathCh <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Convert IP-based URL to localhost hostname for SSRF validation
	serverURL, _ := url.Parse(server.URL)
	callbackURL := "http://localhost:" + serverURL.Port()

	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              "run-abc-123",
			Status:          simulationv1.RunStatus_RUN_STATUS_COMPLETED,
			CreatedAtUnixMs: time.Now().UnixMilli(),
		},
		Input: &simulationv1.RunInput{
			CallbackUrl: callbackURL + "/callback/{run_id}",
		},
	}

	notifier.Notify(callbackURL+"/callback/{run_id}", "", rec)

	select {
	case receivedPath := <-receivedPathCh:
		if receivedPath != "/callback/run-abc-123" {
			t.Errorf("expected path '/callback/run-abc-123', got '%s'", receivedPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestNotifierNotify_OptimizationPayloadIncludesTopCandidates(t *testing.T) {
	payloadCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		payloadCh <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	callbackURL := "http://localhost:" + serverURL.Port() + "/callback"

	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              "opt-run-1",
			Status:          simulationv1.RunStatus_RUN_STATUS_COMPLETED,
			CreatedAtUnixMs: time.Now().UnixMilli(),
			EndedAtUnixMs:   time.Now().UnixMilli(),
			BestRunId:       "opt-cand-best",
			BestScore:       12.5,
			Iterations:      7,
			CandidateRunIds: []string{"opt-cand-best", "opt-cand-2", "opt-cand-3", "opt-cand-4", "opt-cand-5", "opt-cand-6"},
		},
		Input: &simulationv1.RunInput{
			CallbackUrl: callbackURL,
		},
	}

	notifier.Notify(callbackURL, "", rec)

	select {
	case body := <-payloadCh:
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal payload: %v", err)
		}
		if payload["best_run_id"] != "opt-cand-best" {
			t.Errorf("expected best_run_id opt-cand-best, got %v", payload["best_run_id"])
		}
		if payload["best_score"] != 12.5 {
			t.Errorf("expected best_score 12.5, got %v", payload["best_score"])
		}
		if payload["iterations"] != float64(7) {
			t.Errorf("expected iterations 7, got %v", payload["iterations"])
		}
		topCandidates, ok := payload["top_candidates"].([]interface{})
		if !ok || len(topCandidates) != 5 {
			t.Errorf("expected top_candidates with 5 elements (capped from 6), got %v", payload["top_candidates"])
		} else {
			expected := []string{"opt-cand-best", "opt-cand-2", "opt-cand-3", "opt-cand-4", "opt-cand-5"}
			for i, id := range expected {
				if topCandidates[i].(string) != id {
					t.Errorf("top_candidates[%d] = %v, want %s", i, topCandidates[i], id)
				}
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestNotifierNotify_OnlineOptimizationIncludesFinalConfig(t *testing.T) {
	payloadCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		payloadCh <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	callbackURL := "http://localhost:" + serverURL.Port() + "/callback"

	// Online run: best_run_id is self; optimization history has one step with settled config
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id:              "online-run-1",
			Status:          simulationv1.RunStatus_RUN_STATUS_COMPLETED,
			CreatedAtUnixMs: time.Now().UnixMilli(),
			EndedAtUnixMs:   time.Now().UnixMilli(),
			BestRunId:       "online-run-1",
			Iterations:      1,
			CandidateRunIds: []string{"online-run-1"},
		},
		Input: &simulationv1.RunInput{CallbackUrl: callbackURL},
		OptimizationHistory: []*simulationv1.OptimizationStep{
			{
				IterationIndex: 1,
				Reason:         "scaled up",
				CurrentConfig: &simulationv1.RunConfiguration{
					Services: []*simulationv1.ServiceConfigEntry{
						{ServiceId: "svc1", Replicas: 3, CpuCores: 2, MemoryMb: 512},
					},
				},
			},
		},
	}

	notifier := NewNotifier()
	notifier.Notify(callbackURL, "", rec)

	select {
	case body := <-payloadCh:
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal payload: %v", err)
		}
		if payload["best_run_id"] != "online-run-1" {
			t.Errorf("expected best_run_id online-run-1, got %v", payload["best_run_id"])
		}
		finalConfig, ok := payload["final_config"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected final_config object, got %T %v", payload["final_config"], payload["final_config"])
		}
		services, ok := finalConfig["services"].([]interface{})
		if !ok || len(services) != 1 {
			t.Fatalf("expected final_config.services with 1 entry, got %v", finalConfig["services"])
		}
		s0, ok := services[0].(map[string]interface{})
		if !ok {
			t.Fatalf("expected service as map")
		}
		if s0["service_id"] != "svc1" || s0["replicas"].(float64) != 3 {
			t.Errorf("expected service_id svc1 replicas 3, got %v", s0)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestNotifierNotify_EmptyURL(t *testing.T) {
	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id: "test-run",
		},
		Input: &simulationv1.RunInput{
			CallbackUrl: "",
		},
	}

	// Should not panic or send request
	notifier.Notify("", "", rec)
}

func TestNotifierNotify_InvalidURL(t *testing.T) {
	notifier := NewNotifier()
	rec := &RunRecord{
		Run: &simulationv1.Run{
			Id: "test-run",
		},
		Input: &simulationv1.RunInput{
			CallbackUrl: "http://127.0.0.1:8000/callback", // Direct IP, should be blocked
		},
	}

	// Should not send request due to validation
	notifier.Notify("http://127.0.0.1:8000/callback", "", rec)
	time.Sleep(50 * time.Millisecond)
}

func TestGetCallbackSecret(t *testing.T) {
	tests := []struct {
		name     string
		rec      *RunRecord
		expected string
	}{
		{
			name: "with secret",
			rec: &RunRecord{
				Input: &simulationv1.RunInput{
					CallbackSecret: "my-secret",
				},
			},
			expected: "my-secret",
		},
		{
			name: "without secret",
			rec: &RunRecord{
				Input: &simulationv1.RunInput{
					CallbackSecret: "",
				},
			},
			expected: "",
		},
		{
			name:     "nil record",
			rec:      nil,
			expected: "",
		},
		{
			name: "nil input",
			rec: &RunRecord{
				Input: nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getCallbackSecret(tt.rec)
			if got != tt.expected {
				t.Errorf("getCallbackSecret() = %q, want %q", got, tt.expected)
			}
		})
	}
}
