package models

import (
	"sync"
	"testing"
	"time"
)

func TestTraceAddRequest(t *testing.T) {
	trace := &Trace{
		ID:            "trace-123",
		RootRequestID: "req-1",
		StartTime:     time.Now(),
	}

	req1 := &Request{ID: "req-1", ServiceName: "service-a"}
	req2 := &Request{ID: "req-2", ServiceName: "service-b"}

	trace.AddRequest(req1)
	trace.AddRequest(req2)

	requests := trace.GetRequests()
	if len(requests) != 2 {
		t.Errorf("Expected 2 requests, got %d", len(requests))
	}
	if requests[0].ID != "req-1" {
		t.Errorf("Expected first request ID 'req-1', got '%s'", requests[0].ID)
	}
	if requests[1].ID != "req-2" {
		t.Errorf("Expected second request ID 'req-2', got '%s'", requests[1].ID)
	}
}

func TestTraceConcurrency(t *testing.T) {
	trace := &Trace{
		ID:        "trace-concurrent",
		StartTime: time.Now(),
	}

	var wg sync.WaitGroup
	numGoroutines := 100

	// Add requests concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &Request{
				ID:          string(rune(id)),
				ServiceName: "service",
			}
			trace.AddRequest(req)
		}(i)
	}

	wg.Wait()

	requests := trace.GetRequests()
	if len(requests) != numGoroutines {
		t.Errorf("Expected %d requests, got %d", numGoroutines, len(requests))
	}
}

func TestServiceInstanceActiveRequests(t *testing.T) {
	instance := &ServiceInstance{
		ID:          "instance-1",
		ServiceName: "test-service",
	}

	// Test increment
	instance.IncrementActiveRequests()
	if instance.GetActiveRequests() != 1 {
		t.Errorf("Expected 1 active request, got %d", instance.GetActiveRequests())
	}

	instance.IncrementActiveRequests()
	if instance.GetActiveRequests() != 2 {
		t.Errorf("Expected 2 active requests, got %d", instance.GetActiveRequests())
	}

	// Test decrement
	instance.DecrementActiveRequests()
	if instance.GetActiveRequests() != 1 {
		t.Errorf("Expected 1 active request, got %d", instance.GetActiveRequests())
	}

	instance.DecrementActiveRequests()
	if instance.GetActiveRequests() != 0 {
		t.Errorf("Expected 0 active requests, got %d", instance.GetActiveRequests())
	}

	// Test decrement doesn't go below 0
	instance.DecrementActiveRequests()
	if instance.GetActiveRequests() != 0 {
		t.Errorf("Expected 0 active requests (shouldn't go below 0), got %d", instance.GetActiveRequests())
	}
}

func TestServiceInstanceQueue(t *testing.T) {
	instance := &ServiceInstance{
		ID:          "instance-1",
		ServiceName: "test-service",
	}

	// Test enqueue
	instance.EnqueueRequest("req-1")
	instance.EnqueueRequest("req-2")
	instance.EnqueueRequest("req-3")

	if instance.QueueLength() != 3 {
		t.Errorf("Expected queue length 3, got %d", instance.QueueLength())
	}

	// Test dequeue
	reqID, ok := instance.DequeueRequest()
	if !ok {
		t.Error("Expected to dequeue successfully")
	}
	if reqID != "req-1" {
		t.Errorf("Expected to dequeue 'req-1', got '%s'", reqID)
	}
	if instance.QueueLength() != 2 {
		t.Errorf("Expected queue length 2, got %d", instance.QueueLength())
	}

	// Dequeue remaining
	instance.DequeueRequest()
	instance.DequeueRequest()

	if instance.QueueLength() != 0 {
		t.Errorf("Expected queue length 0, got %d", instance.QueueLength())
	}

	// Test dequeue from empty queue
	_, ok = instance.DequeueRequest()
	if ok {
		t.Error("Expected dequeue to fail on empty queue")
	}
}

func TestServiceInstanceConcurrency(t *testing.T) {
	instance := &ServiceInstance{
		ID:          "instance-concurrent",
		ServiceName: "test-service",
	}

	var wg sync.WaitGroup
	numGoroutines := 100

	// Increment concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance.IncrementActiveRequests()
		}()
	}

	wg.Wait()

	if instance.GetActiveRequests() != numGoroutines {
		t.Errorf("Expected %d active requests, got %d", numGoroutines, instance.GetActiveRequests())
	}

	// Decrement concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instance.DecrementActiveRequests()
		}()
	}

	wg.Wait()

	if instance.GetActiveRequests() != 0 {
		t.Errorf("Expected 0 active requests, got %d", instance.GetActiveRequests())
	}
}

func TestHostServiceManagement(t *testing.T) {
	host := &Host{
		ID:       "host-1",
		CPUCores: 8,
		MemoryGB: 16,
	}

	// Test add service
	host.AddService("svc-1")
	host.AddService("svc-2")
	host.AddService("svc-3")

	services := host.GetServices()
	if len(services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(services))
	}

	// Test remove service
	host.RemoveService("svc-2")
	services = host.GetServices()
	if len(services) != 2 {
		t.Errorf("Expected 2 services after removal, got %d", len(services))
	}

	// Verify svc-2 was removed
	for _, svc := range services {
		if svc == "svc-2" {
			t.Error("Expected svc-2 to be removed")
		}
	}

	// Test remove non-existent service (should not crash)
	host.RemoveService("svc-999")
	services = host.GetServices()
	if len(services) != 2 {
		t.Errorf("Expected 2 services, got %d", len(services))
	}
}

func TestHostConcurrency(t *testing.T) {
	host := &Host{
		ID:       "host-concurrent",
		CPUCores: 8,
	}

	var wg sync.WaitGroup
	numGoroutines := 100

	// Add services concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			host.AddService(string(rune(id)))
		}(i)
	}

	wg.Wait()

	services := host.GetServices()
	if len(services) != numGoroutines {
		t.Errorf("Expected %d services, got %d", numGoroutines, len(services))
	}
}

func TestRunStatus(t *testing.T) {
	run := &Run{
		ID:        "run-1",
		Status:    RunStatusPending,
		StartTime: time.Now(),
	}

	if run.Status != RunStatusPending {
		t.Errorf("Expected status pending, got %s", run.Status)
	}

	run.Status = RunStatusRunning
	if run.Status != RunStatusRunning {
		t.Errorf("Expected status running, got %s", run.Status)
	}

	run.Status = RunStatusCompleted
	if run.Status != RunStatusCompleted {
		t.Errorf("Expected status completed, got %s", run.Status)
	}
}

func TestRequestStatus(t *testing.T) {
	req := &Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "test-service",
		Status:      RequestStatusPending,
		ArrivalTime: time.Now(),
	}

	if req.Status != RequestStatusPending {
		t.Errorf("Expected status pending, got %s", req.Status)
	}

	req.Status = RequestStatusProcessing
	if req.Status != RequestStatusProcessing {
		t.Errorf("Expected status processing, got %s", req.Status)
	}

	req.Status = RequestStatusCompleted
	if req.Status != RequestStatusCompleted {
		t.Errorf("Expected status completed, got %s", req.Status)
	}
}
