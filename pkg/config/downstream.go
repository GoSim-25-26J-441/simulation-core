package config

import "strings"

// IsAsync reports whether this edge should be excluded from synchronous call-graph cycle checks.
// Default (empty mode) is synchronous REST-style behavior.
func (d DownstreamCall) IsAsync() bool {
	m := strings.ToLower(strings.TrimSpace(d.Mode))
	return m == "async" || m == "event"
}

// IsRetryable reports whether downstream retries are allowed for this edge (default true).
func (d DownstreamCall) IsRetryable() bool {
	if d.Retryable == nil {
		return true
	}
	return *d.Retryable
}
