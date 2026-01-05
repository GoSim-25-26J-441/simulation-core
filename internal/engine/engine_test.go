package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestNewEngine(t *testing.T) {
	engine := NewEngine("test-run")
	if engine == nil {
		t.Fatal("NewEngine returned nil")
	}

	if engine.eventQueue == nil {
		t.Error("Event queue should not be nil")
	}
	if engine.runManager == nil {
		t.Error("Run manager should not be nil")
	}
	if engine.simTime == nil {
		t.Error("Sim time should not be nil")
	}
}

func TestEngineScheduleEvent(t *testing.T) {
	engine := NewEngine("test-run")

	event := &Event{
		Type: EventTypeRequestArrival,
		Time: time.Now(),
	}

	engine.ScheduleEvent(event)

	if engine.eventQueue.Size() != 1 {
		t.Errorf("Expected 1 event in queue, got %d", engine.eventQueue.Size())
	}
	if atomic.LoadInt64(&engine.eventCounter) != 1 {
		t.Errorf("Expected event counter to be 1, got %d", atomic.LoadInt64(&engine.eventCounter))
	}

	// Event ID should be auto-generated
	if event.ID == "" {
		t.Error("Event ID should be auto-generated")
	}
}

func TestEngineScheduleAt(t *testing.T) {
	engine := NewEngine("test-run")

	futureTime := time.Now().Add(5 * time.Second)
	req := &models.Request{ID: "req-1"}

	engine.ScheduleAt(EventTypeRequestStart, futureTime, req, "service-1", nil)

	if engine.eventQueue.Size() != 1 {
		t.Errorf("Expected 1 event in queue, got %d", engine.eventQueue.Size())
	}

	event := engine.eventQueue.Peek()
	if event.Type != EventTypeRequestStart {
		t.Errorf("Expected event type RequestStart, got %s", event.Type)
	}
	if event.Request.ID != "req-1" {
		t.Errorf("Expected request ID 'req-1', got '%s'", event.Request.ID)
	}
	if event.ServiceID != "service-1" {
		t.Errorf("Expected service ID 'service-1', got '%s'", event.ServiceID)
	}
}

func TestEngineScheduleAfter(t *testing.T) {
	engine := NewEngine("test-run")

	delay := 100 * time.Millisecond
	data := map[string]interface{}{"key": "value"}

	beforeTime := engine.GetSimTime()
	engine.ScheduleAfter(EventTypeRequestComplete, delay, nil, "service-2", data)

	event := engine.eventQueue.Peek()
	expectedTime := beforeTime.Add(delay)

	// Allow for small time differences due to processing
	timeDiff := event.Time.Sub(expectedTime)
	if timeDiff > time.Millisecond || timeDiff < -time.Millisecond {
		t.Errorf("Event time doesn't match expected. Expected around %v, got %v", expectedTime, event.Time)
	}

	if event.Data["key"] != "value" {
		t.Errorf("Expected data key to be 'value', got '%v'", event.Data["key"])
	}
}

func TestEngineRegisterHandler(t *testing.T) {
	engine := NewEngine("test-run")

	handlerCalled := false
	handler := func(e *Engine, event *Event) error {
		handlerCalled = true
		return nil
	}

	engine.RegisterHandler(EventTypeRequestArrival, handler)

	if len(engine.handlers) != 1 {
		t.Errorf("Expected 1 handler registered, got %d", len(engine.handlers))
	}

	// Verify handler can be called
	event := &Event{Type: EventTypeRequestArrival, Time: time.Now()}
	_ = engine.handlers[EventTypeRequestArrival](engine, event)

	if !handlerCalled {
		t.Error("Handler should have been called")
	}
}

func TestEngineRun(t *testing.T) {
	engine := NewEngine("test-run")

	eventsProcessed := 0
	handler := func(e *Engine, event *Event) error {
		eventsProcessed++
		return nil
	}

	engine.RegisterHandler(EventTypeRequestArrival, handler)
	engine.RegisterHandler(EventTypeRequestStart, handler)
	engine.RegisterHandler(EventTypeRequestComplete, handler)

	// Schedule some events
	now := engine.GetSimTime()
	engine.ScheduleAt(EventTypeRequestArrival, now.Add(10*time.Millisecond), nil, "", nil)
	engine.ScheduleAt(EventTypeRequestStart, now.Add(20*time.Millisecond), nil, "", nil)
	engine.ScheduleAt(EventTypeRequestComplete, now.Add(30*time.Millisecond), nil, "", nil)

	// Run simulation for 100ms
	err := engine.Run(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine.Run() returned error: %v", err)
	}

	if eventsProcessed != 3 {
		t.Errorf("Expected 3 events to be processed, got %d", eventsProcessed)
	}

	run := engine.GetRunManager().GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Errorf("Expected run status completed, got %s", run.Status)
	}
}

func TestEngineRunWithNoHandlers(t *testing.T) {
	engine := NewEngine("test-run")

	// Schedule events without registering handlers
	now := engine.GetSimTime()
	engine.ScheduleAt(EventTypeRequestArrival, now.Add(10*time.Millisecond), nil, "", nil)

	// Should not fail even without handlers
	err := engine.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine.Run() returned error: %v", err)
	}

	run := engine.GetRunManager().GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Errorf("Expected run status completed, got %s", run.Status)
	}
}

func TestEngineRunEmptyQueue(t *testing.T) {
	engine := NewEngine("test-run")

	// Run with no events scheduled
	err := engine.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine.Run() returned error: %v", err)
	}

	run := engine.GetRunManager().GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Errorf("Expected run status completed, got %s", run.Status)
	}
}

func TestEngineStop(t *testing.T) {
	engine := NewEngine("test-run")

	// Schedule some events
	now := engine.GetSimTime()
	for i := 0; i < 10; i++ {
		engine.ScheduleAt(EventTypeRequestArrival, now.Add(time.Duration(i)*time.Millisecond), nil, "", nil)
	}

	if engine.eventQueue.Size() != 10 {
		t.Errorf("Expected 10 events in queue, got %d", engine.eventQueue.Size())
	}

	engine.Stop()

	// Queue should be cleared
	if !engine.eventQueue.IsEmpty() {
		t.Errorf("Event queue should be empty after Stop(), got size %d", engine.eventQueue.Size())
	}

	// Context should be cancelled
	select {
	case <-engine.GetRunManager().Context().Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled after Stop()")
	}
}

func TestEngineGetStats(t *testing.T) {
	engine := NewEngine("test-run")
	engine.runManager.Start()

	// Add some requests to the run manager
	req1 := &models.Request{ID: "req-1", Status: models.RequestStatusCompleted}
	req2 := &models.Request{ID: "req-2", Status: models.RequestStatusFailed}
	engine.runManager.AddRequest(req1)
	engine.runManager.AddRequest(req2)

	// Schedule some events
	now := engine.GetSimTime()
	engine.ScheduleAt(EventTypeRequestArrival, now.Add(10*time.Millisecond), nil, "", nil)

	stats := engine.GetStats()
	if stats["total_requests"] != 2 {
		t.Errorf("Expected 2 total requests in stats, got %v", stats["total_requests"])
	}
	if stats["events_in_queue"] != 1 {
		t.Errorf("Expected 1 event in queue, got %v", stats["events_in_queue"])
	}
	if stats["sim_time"] == "" {
		t.Error("Expected sim_time to be set in stats")
	}
}

func TestEngineSimTimeAdvancement(t *testing.T) {
	engine := NewEngine("test-run")

	startTime := engine.GetSimTime()

	handler := func(e *Engine, event *Event) error {
		// Check that sim time has advanced to event time
		if !e.GetSimTime().Equal(event.Time) {
			t.Errorf("Sim time should be %v, got %v", event.Time, e.GetSimTime())
		}
		return nil
	}

	engine.RegisterHandler(EventTypeRequestArrival, handler)

	// Schedule event in the future
	futureTime := startTime.Add(1 * time.Second)
	engine.ScheduleAt(EventTypeRequestArrival, futureTime, nil, "", nil)

	// Run simulation
	err := engine.Run(2 * time.Second)
	if err != nil {
		t.Fatalf("Engine.Run() returned error: %v", err)
	}

	// Sim time should have advanced to at least the event time
	finalTime := engine.GetSimTime()
	if finalTime.Before(futureTime) {
		t.Errorf("Sim time should have advanced to at least %v, got %v", futureTime, finalTime)
	}
}

func TestEngineHandlerError(t *testing.T) {
	engine := NewEngine("test-run")

	errorHandler := func(e *Engine, event *Event) error {
		return &testError{msg: "handler error"}
	}

	engine.RegisterHandler(EventTypeRequestArrival, errorHandler)

	// Schedule event
	now := engine.GetSimTime()
	engine.ScheduleAt(EventTypeRequestArrival, now.Add(10*time.Millisecond), nil, "", nil)

	// Run should not fail even if handler returns error
	err := engine.Run(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("Engine.Run() should not return error from handler: %v", err)
	}

	// Run should still complete successfully
	run := engine.GetRunManager().GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Errorf("Expected run status completed despite handler error, got %s", run.Status)
	}
}
