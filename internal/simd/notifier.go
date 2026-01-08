package simd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

// NotificationPayload represents the JSON payload sent to the callback URL
type NotificationPayload struct {
	RunID           string                   `json:"run_id"`
	Status          simulationv1.RunStatus   `json:"status"`
	StatusString    string                   `json:"status_string"`
	CreatedAtUnixMs int64                    `json:"created_at_unix_ms"`
	StartedAtUnixMs int64                    `json:"started_at_unix_ms,omitempty"`
	EndedAtUnixMs   int64                    `json:"ended_at_unix_ms,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Metrics         *simulationv1.RunMetrics `json:"metrics,omitempty"`
	Timestamp       int64                    `json:"timestamp"` // When notification was sent
}

// Notifier handles backend notifications for simulation completion
type Notifier struct {
	httpClient *http.Client
	maxRetries int
	baseDelay  time.Duration
}

// NewNotifier creates a new notification service
func NewNotifier() *Notifier {
	return &Notifier{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		maxRetries: 3,
		baseDelay:  1 * time.Second,
	}
}

// Notify sends a notification to the callback URL asynchronously
// This method returns immediately and performs the notification in a goroutine
func (n *Notifier) Notify(callbackURL string, callbackSecret string, runRecord *RunRecord) {
	if callbackURL == "" {
		return
	}

	// Clone the run record to avoid race conditions
	rec := runRecord
	if rec == nil || rec.Run == nil {
		logger.Warn("cannot notify: invalid run record", "callback_url", callbackURL)
		return
	}

	// Replace {run_id} template in callback URL if present
	finalURL := strings.ReplaceAll(callbackURL, "{run_id}", rec.Run.Id)

	// Build notification payload
	payload := NotificationPayload{
		RunID:           rec.Run.Id,
		Status:          rec.Run.Status,
		StatusString:    rec.Run.Status.String(),
		CreatedAtUnixMs: rec.Run.CreatedAtUnixMs,
		StartedAtUnixMs: rec.Run.StartedAtUnixMs,
		EndedAtUnixMs:   rec.Run.EndedAtUnixMs,
		Error:           rec.Run.Error,
		Metrics:         rec.Metrics,
		Timestamp:       time.Now().UTC().UnixMilli(),
	}

	// Send notification asynchronously
	go n.sendNotification(finalURL, callbackSecret, payload)
}

// sendNotification performs the actual HTTP POST with retry logic
func (n *Notifier) sendNotification(callbackURL string, callbackSecret string, payload NotificationPayload) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		logger.Error("failed to marshal notification payload",
			"callback_url", callbackURL,
			"run_id", payload.RunID,
			"error", err)
		return
	}

	var lastErr error
	for attempt := 0; attempt <= n.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: delay = baseDelay * 2^(attempt-1)
			delay := n.baseDelay * time.Duration(1<<uint(attempt-1))
			logger.Debug("retrying notification",
				"callback_url", callbackURL,
				"run_id", payload.RunID,
				"attempt", attempt,
				"delay", delay)
			time.Sleep(delay)
		}

		req, err := http.NewRequest("POST", callbackURL, bytes.NewReader(payloadJSON))
		if err != nil {
			lastErr = fmt.Errorf("failed to create request: %w", err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "simulation-core/1.0")

		// Add callback secret header if provided
		if callbackSecret != "" {
			req.Header.Set("X-Simulation-Callback-Secret", callbackSecret)
		}

		resp, err := n.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			logger.Warn("notification attempt failed",
				"callback_url", callbackURL,
				"run_id", payload.RunID,
				"attempt", attempt+1,
				"error", err)
			continue
		}

		// Read response body for error details
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		responseBody := string(bodyBytes)
		if len(responseBody) > 200 {
			responseBody = responseBody[:200] + "..."
		}

		// Success if status code is 2xx
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			logger.Info("notification sent successfully",
				"run_id", payload.RunID,
				"status", payload.StatusString,
				"status_code", resp.StatusCode)
			return
		}

		lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		logger.Warn("notification returned non-2xx status",
			"callback_url", callbackURL,
			"run_id", payload.RunID,
			"status_code", resp.StatusCode,
			"response_body", responseBody,
			"attempt", attempt+1)
	}

	// All retries exhausted
	logger.Error("failed to send notification after retries",
		"callback_url", callbackURL,
		"run_id", payload.RunID,
		"status", payload.StatusString,
		"max_retries", n.maxRetries,
		"last_error", lastErr)
}
