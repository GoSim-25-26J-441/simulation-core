package policy

import (
	"testing"
	"time"
)

func TestNewRateLimitingPolicy(t *testing.T) {
	policy := NewRateLimitingPolicy(true, 10)
	if policy == nil {
		t.Fatalf("expected policy to be created")
	}
	if !policy.Enabled() {
		t.Fatalf("expected policy to be enabled")
	}
	if policy.Name() != "rate_limiting" {
		t.Fatalf("expected name to be 'rate_limiting', got %s", policy.Name())
	}
}

func TestRateLimitingPolicyAllowRequest(t *testing.T) {
	policy := NewRateLimitingPolicy(true, 2) // 2 requests per second
	now := time.Now()

	// First request should be allowed
	if !policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected first request to be allowed")
	}

	// Second request should be allowed
	if !policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected second request to be allowed")
	}

	// Third request should be rejected (rate limit exceeded)
	if policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected third request to be rejected")
	}

	// After 1 second, tokens should refill
	afterSecond := now.Add(1 * time.Second)
	if !policy.AllowRequest("svc1", "/endpoint1", afterSecond) {
		t.Fatalf("expected request after 1 second to be allowed")
	}
}

func TestRateLimitingPolicyDifferentKeys(t *testing.T) {
	policy := NewRateLimitingPolicy(true, 1) // 1 request per second
	now := time.Now()

	// Different services should have separate buckets
	if !policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected request for svc1 to be allowed")
	}
	if !policy.AllowRequest("svc2", "/endpoint1", now) {
		t.Fatalf("expected request for svc2 to be allowed")
	}

	// Different endpoints should have separate buckets
	if !policy.AllowRequest("svc1", "/endpoint2", now) {
		t.Fatalf("expected request for different endpoint to be allowed")
	}
}

func TestRateLimitingPolicyGetRemainingQuota(t *testing.T) {
	policy := NewRateLimitingPolicy(true, 5) // 5 requests per second
	now := time.Now()

	// Initially should have full quota
	quota := policy.GetRemainingQuota("svc1", "/endpoint1", now)
	if quota != 5 {
		t.Fatalf("expected remaining quota 5, got %d", quota)
	}

	// After consuming 2 requests
	policy.AllowRequest("svc1", "/endpoint1", now)
	policy.AllowRequest("svc1", "/endpoint1", now)
	quota = policy.GetRemainingQuota("svc1", "/endpoint1", now)
	if quota != 3 {
		t.Fatalf("expected remaining quota 3, got %d", quota)
	}

	// After consuming all quota
	policy.AllowRequest("svc1", "/endpoint1", now)
	policy.AllowRequest("svc1", "/endpoint1", now)
	policy.AllowRequest("svc1", "/endpoint1", now)
	quota = policy.GetRemainingQuota("svc1", "/endpoint1", now)
	if quota != 0 {
		t.Fatalf("expected remaining quota 0, got %d", quota)
	}
}

func TestRateLimitingPolicyTokenRefill(t *testing.T) {
	policy := NewRateLimitingPolicy(true, 2) // 2 requests per second
	now := time.Now()

	// Consume all tokens
	policy.AllowRequest("svc1", "/endpoint1", now)
	policy.AllowRequest("svc1", "/endpoint1", now)

	// Check quota immediately after consuming tokens
	quota := policy.GetRemainingQuota("svc1", "/endpoint1", now)
	if quota != 0 {
		t.Fatalf("expected quota 0 after consuming all tokens, got %d", quota)
	}

	// After 1 second, should have full quota refilled
	futureTime := now.Add(1 * time.Second)
	quota = policy.GetRemainingQuota("svc1", "/endpoint1", futureTime)
	if quota != 2 {
		t.Fatalf("expected remaining quota 2 after 1 second, got %d", quota)
	}
}

func TestRateLimitingPolicyWhenDisabled(t *testing.T) {
	policy := NewRateLimitingPolicy(false, 1)
	now := time.Now()

	// All requests should be allowed when disabled
	for i := 0; i < 10; i++ {
		if !policy.AllowRequest("svc1", "/endpoint1", now) {
			t.Fatalf("expected request %d to be allowed when disabled", i)
		}
	}

	// Quota should return -1 (unlimited)
	quota := policy.GetRemainingQuota("svc1", "/endpoint1", now)
	if quota != -1 {
		t.Fatalf("expected quota -1 (unlimited) when disabled, got %d", quota)
	}
}
