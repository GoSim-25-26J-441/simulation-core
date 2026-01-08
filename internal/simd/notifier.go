package simd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

	// Validate callback URL to prevent SSRF attacks
	if err := validateCallbackURL(finalURL); err != nil {
		logger.Error("invalid callback URL, blocking request",
			"run_id", rec.Run.Id,
			"callback_url", finalURL,
			"error", err)
		return
	}

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

var (
	// ErrInvalidURL is returned when the callback URL format is invalid
	ErrInvalidURL = fmt.Errorf("invalid URL format")
	// ErrInternalHost is returned when the callback URL points to an internal/private host
	ErrInternalHost = fmt.Errorf("callback URL cannot target internal/private networks")
	// ErrMetadataEndpoint is returned when the callback URL targets metadata endpoints
	ErrMetadataEndpoint = fmt.Errorf("callback URL cannot target metadata endpoints")
)

// validateCallbackURL validates the callback URL to prevent SSRF attacks
func validateCallbackURL(callbackURL string) error {
	// Parse URL
	parsedURL, err := url.Parse(callbackURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	// Only allow http and https schemes
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%w: only http and https schemes are allowed, got %s", ErrInvalidURL, parsedURL.Scheme)
	}

	host := parsedURL.Hostname()
	if host == "" {
		return fmt.Errorf("%w: missing hostname", ErrInvalidURL)
	}

	// Block metadata endpoints (AWS, GCP, Azure, etc.)
	metadataHosts := []string{
		"169.254.169.254", // AWS, GCP, Azure metadata
		"metadata.google.internal",
		"metadata", // Common metadata hostname
	}
	hostLower := strings.ToLower(host)
	for _, blocked := range metadataHosts {
		if hostLower == blocked || strings.HasPrefix(hostLower, blocked+".") {
			return fmt.Errorf("%w: metadata endpoint blocked", ErrMetadataEndpoint)
		}
	}

	// Block wildcard addresses
	if hostLower == "0.0.0.0" || hostLower == "::" {
		return fmt.Errorf("%w: wildcard addresses are not allowed", ErrInternalHost)
	}

	// Block direct IP access to loopback (127.0.0.1, ::1)
	// Allow localhost hostname as it may be valid in containerized environments
	if hostLower == "127.0.0.1" || hostLower == "::1" {
		return fmt.Errorf("%w: direct loopback IP addresses are not allowed (use localhost hostname for development)", ErrInternalHost)
	}

	// Resolve hostname to check if it's a private IP
	ips, err := net.LookupIP(host)
	if err != nil {
		// If DNS resolution fails, we can't verify - be conservative and block
		// In production, you might want to allow configured domains
		return fmt.Errorf("%w: failed to resolve hostname: %v", ErrInternalHost, err)
	}

	// Check all resolved IPs for private/internal ranges
	// Allow loopback IPs (for localhost in development/containerized environments)
	// Block all other private IP ranges (RFC 1918, link-local, etc.)
	for _, ip := range ips {
		if isPrivateIP(ip) && !ip.IsLoopback() {
			return fmt.Errorf("%w: hostname resolves to private IP: %s", ErrInternalHost, ip.String())
		}
	}

	return nil
}

// isPrivateIP checks if an IP address is in a private/internal range
func isPrivateIP(ip net.IP) bool {
	// Handle IPv4 and IPv6
	if ip4 := ip.To4(); ip4 != nil {
		// Check RFC 1918 private ranges
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168) ||
			// Check link-local
			(ip4[0] == 169 && ip4[1] == 254) ||
			// Check loopback
			ip4[0] == 127
	}

	// Check IPv6 private ranges
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Check IPv6 unique local addresses (fc00::/7)
	if len(ip) == net.IPv6len {
		return ip[0] == 0xfc || ip[0] == 0xfd
	}

	return false
}
