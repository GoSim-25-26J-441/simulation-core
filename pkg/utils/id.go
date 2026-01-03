package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var (
	// Counter for sequential IDs
	idCounter uint64
)

// GenerateID generates a unique ID
func GenerateID() string {
	// Increment counter atomically
	count := atomic.AddUint64(&idCounter, 1)

	// Combine timestamp with counter for uniqueness
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("%x-%x", timestamp, count)
}

// GenerateTraceID generates a trace ID (16 bytes hex-encoded)
func GenerateTraceID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID
		return GenerateID()
	}
	return hex.EncodeToString(b)
}

// GenerateRequestID generates a request ID (8 bytes hex-encoded)
func GenerateRequestID() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID
		return GenerateID()
	}
	return hex.EncodeToString(b)
}

// GenerateRunID generates a run ID with a timestamp prefix
func GenerateRunID() string {
	timestamp := time.Now().Format("20060102-150405")
	b := make([]byte, 4)
	_, err := rand.Read(b)
	if err != nil {
		count := atomic.AddUint64(&idCounter, 1)
		return fmt.Sprintf("run-%s-%x", timestamp, count)
	}
	return fmt.Sprintf("run-%s-%s", timestamp, hex.EncodeToString(b))
}

// GenerateServiceInstanceID generates a service instance ID
func GenerateServiceInstanceID(serviceName string, index int) string {
	return fmt.Sprintf("%s-%d", serviceName, index)
}
