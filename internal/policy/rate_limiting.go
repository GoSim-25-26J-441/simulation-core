package policy

import (
	"sync"
	"time"
)

// rateLimitingPolicy implements RateLimitingPolicy using token bucket algorithm
type rateLimitingPolicy struct {
	enabled bool
	// rateLimitPerSecond is the maximum requests per second per service/endpoint
	rateLimitPerSecond int
	// buckets tracks token buckets per service/endpoint
	buckets map[string]*tokenBucket
	mu      sync.RWMutex
}

// tokenBucket implements a simple token bucket for rate limiting
type tokenBucket struct {
	capacity   int       // Maximum tokens
	tokens     int       // Current tokens
	refillRate int       // Tokens per second
	lastRefill time.Time // Last time tokens were refilled
	mu         sync.Mutex
}

// NewRateLimitingPolicy creates a new rate limiting policy
func NewRateLimitingPolicy(enabled bool, rateLimitPerSecond int) RateLimitingPolicy {
	return &rateLimitingPolicy{
		enabled:            enabled,
		rateLimitPerSecond: rateLimitPerSecond,
		buckets:            make(map[string]*tokenBucket),
	}
}

func (p *rateLimitingPolicy) Enabled() bool {
	return p.enabled
}

func (p *rateLimitingPolicy) Name() string {
	return "rate_limiting"
}

func (p *rateLimitingPolicy) AllowRequest(serviceID, endpointPath string, requestTime time.Time) bool {
	if !p.enabled {
		return true
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	bucket, exists := p.buckets[key]
	p.mu.RUnlock()

	if !exists {
		p.mu.Lock()
		// Double-check after acquiring write lock
		if bucket, exists = p.buckets[key]; !exists {
			bucket = &tokenBucket{
				capacity:   p.rateLimitPerSecond,
				tokens:     p.rateLimitPerSecond,
				refillRate: p.rateLimitPerSecond,
				lastRefill: requestTime,
			}
			p.buckets[key] = bucket
		}
		p.mu.Unlock()
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill tokens based on time elapsed
	elapsed := requestTime.Sub(bucket.lastRefill)
	tokensToAdd := int(elapsed.Seconds() * float64(bucket.refillRate))
	if tokensToAdd > 0 {
		bucket.tokens += tokensToAdd
		if bucket.tokens > bucket.capacity {
			bucket.tokens = bucket.capacity
		}
		bucket.lastRefill = requestTime
	}

	// Check if we have tokens available
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

func (p *rateLimitingPolicy) GetRemainingQuota(serviceID, endpointPath string) int {
	if !p.enabled {
		return -1 // Unlimited
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	bucket, exists := p.buckets[key]
	p.mu.RUnlock()

	if !exists {
		return p.rateLimitPerSecond
	}

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill tokens
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefill)
	tokensToAdd := int(elapsed.Seconds() * float64(bucket.refillRate))
	if tokensToAdd > 0 {
		bucket.tokens += tokensToAdd
		if bucket.tokens > bucket.capacity {
			bucket.tokens = bucket.capacity
		}
		bucket.lastRefill = now
	}

	return bucket.tokens
}
