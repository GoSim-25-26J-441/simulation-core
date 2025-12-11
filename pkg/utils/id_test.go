package utils

import (
	"strings"
	"sync"
	"testing"
)

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	id2 := GenerateID()

	if id1 == "" {
		t.Error("GenerateID returned empty string")
	}

	if id1 == id2 {
		t.Error("GenerateID should return unique IDs")
	}

	// Should contain a hyphen (timestamp-counter format)
	if !strings.Contains(id1, "-") {
		t.Errorf("GenerateID should contain hyphen: %s", id1)
	}
}

func TestGenerateTraceID(t *testing.T) {
	id1 := GenerateTraceID()
	id2 := GenerateTraceID()

	if id1 == "" {
		t.Error("GenerateTraceID returned empty string")
	}

	if id1 == id2 {
		t.Error("GenerateTraceID should return unique IDs")
	}

	// 16 bytes hex-encoded = 32 characters
	if len(id1) != 32 {
		t.Errorf("GenerateTraceID should return 32 character hex string, got %d: %s", len(id1), id1)
	}
}

func TestGenerateRequestID(t *testing.T) {
	id1 := GenerateRequestID()
	id2 := GenerateRequestID()

	if id1 == "" {
		t.Error("GenerateRequestID returned empty string")
	}

	if id1 == id2 {
		t.Error("GenerateRequestID should return unique IDs")
	}

	// 8 bytes hex-encoded = 16 characters
	if len(id1) != 16 {
		t.Errorf("GenerateRequestID should return 16 character hex string, got %d: %s", len(id1), id1)
	}
}

func TestGenerateRunID(t *testing.T) {
	id1 := GenerateRunID()
	id2 := GenerateRunID()

	if id1 == "" {
		t.Error("GenerateRunID returned empty string")
	}

	if id1 == id2 {
		t.Error("GenerateRunID should return unique IDs")
	}

	// Should start with "run-"
	if !strings.HasPrefix(id1, "run-") {
		t.Errorf("GenerateRunID should start with 'run-': %s", id1)
	}

	// Should contain timestamp in format YYYYMMDD-HHMMSS
	parts := strings.Split(id1, "-")
	if len(parts) < 3 {
		t.Errorf("GenerateRunID should have at least 3 parts: %s", id1)
	}
}

func TestGenerateServiceInstanceID(t *testing.T) {
	id := GenerateServiceInstanceID("test-service", 5)

	expected := "test-service-5"
	if id != expected {
		t.Errorf("Expected '%s', got '%s'", expected, id)
	}

	id2 := GenerateServiceInstanceID("api", 0)
	expected2 := "api-0"
	if id2 != expected2 {
		t.Errorf("Expected '%s', got '%s'", expected2, id2)
	}
}

func TestIDUniqueness(t *testing.T) {
	numIDs := 1000
	ids := make(map[string]bool)

	for i := 0; i < numIDs; i++ {
		id := GenerateID()
		if ids[id] {
			t.Errorf("Duplicate ID generated: %s", id)
		}
		ids[id] = true
	}

	if len(ids) != numIDs {
		t.Errorf("Expected %d unique IDs, got %d", numIDs, len(ids))
	}
}

func TestIDConcurrency(t *testing.T) {
	numGoroutines := 100
	idsPerGoroutine := 100

	idChan := make(chan string, numGoroutines*idsPerGoroutine)
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerGoroutine; j++ {
				idChan <- GenerateID()
			}
		}()
	}

	wg.Wait()
	close(idChan)

	// Check uniqueness
	ids := make(map[string]bool)
	for id := range idChan {
		if ids[id] {
			t.Errorf("Duplicate ID generated in concurrent test: %s", id)
		}
		ids[id] = true
	}

	expectedCount := numGoroutines * idsPerGoroutine
	if len(ids) != expectedCount {
		t.Errorf("Expected %d unique IDs, got %d", expectedCount, len(ids))
	}
}

func TestTraceIDConcurrency(t *testing.T) {
	numGoroutines := 50
	idsPerGoroutine := 50

	idChan := make(chan string, numGoroutines*idsPerGoroutine)
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < idsPerGoroutine; j++ {
				idChan <- GenerateTraceID()
			}
		}()
	}

	wg.Wait()
	close(idChan)

	// Check uniqueness
	ids := make(map[string]bool)
	for id := range idChan {
		if ids[id] {
			t.Errorf("Duplicate trace ID generated in concurrent test: %s", id)
		}
		ids[id] = true
	}

	expectedCount := numGoroutines * idsPerGoroutine
	if len(ids) != expectedCount {
		t.Errorf("Expected %d unique trace IDs, got %d", expectedCount, len(ids))
	}
}
