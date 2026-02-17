package simd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

const (
	callbackTimeout = 10 * time.Second
)

// validateCallbackURL checks the URL for SSRF safety (blocks private IPs, metadata endpoints).
func validateCallbackURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return fmt.Errorf("callback URL must use http or https scheme")
	}
	if strings.Contains(strings.ToLower(rawURL), "metadata.google.internal") ||
		strings.Contains(strings.ToLower(rawURL), "169.254.169.254") {
		return fmt.Errorf("callback URL must not target metadata endpoints")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("callback URL must have a valid host")
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("callback URL must not target private IP: %s", ip)
		}
	}
	return nil
}

// resolveCallbackURL replaces {run_id} placeholder in the URL.
func resolveCallbackURL(template string, runID string) string {
	return strings.ReplaceAll(template, "{run_id}", runID)
}

// notifyCallback sends a POST to the callback URL with run status.
// It is best-effort: runs asynchronously and logs errors.
func notifyCallback(callbackURL, callbackSecret string, run *simulationv1.Run) {
	if callbackURL == "" {
		return
	}
	url := resolveCallbackURL(callbackURL, run.Id)
	if err := validateCallbackURL(url); err != nil {
		logger.Warn("callback URL validation failed", "url", url, "error", err)
		return
	}

	payload := map[string]any{
		"run_id":      run.Id,
		"status":      run.Status.String(),
		"error":       run.Error,
		"best_run_id": run.BestRunId,
		"best_score":  run.BestScore,
		"iterations":  run.Iterations,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Error("failed to marshal callback payload", "run_id", run.Id, "error", err)
		return
	}

	go func() {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			logger.Error("failed to create callback request", "run_id", run.Id, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if callbackSecret != "" {
			req.Header.Set("X-Simulation-Callback-Secret", callbackSecret)
		}

		client := &http.Client{Timeout: callbackTimeout}

		resp, err := client.Do(req)
		if err != nil {
			logger.Error("callback request failed", "run_id", run.Id, "url", url, "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			logger.Warn("callback returned non-2xx", "run_id", run.Id, "url", url, "status", resp.StatusCode)
		}
	}()
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "::1/128", "fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil && block.Contains(ip) {
			return true
		}
	}
	return false
}
