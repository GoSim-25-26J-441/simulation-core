package engine

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestNewEventQueue(t *testing.T) {
	eq := NewEventQueue()
	if eq == nil {
		t.Fatal("NewEventQueue returned nil")
	}
	if !eq.IsEmpty() {
		t.Error("New event queue should be empty")
	}
}

func TestEventQueueScheduleAndNext(t *testing.T) {
	eq := NewEventQueue()

	now := time.Now()
	event1 := &Event{
		ID:   "evt-1",
		Type: EventTypeRequestArrival,
		Time: now.Add(1 * time.Second),
	}
	event2 := &Event{
		ID:   "evt-2",
		Type: EventTypeRequestStart,
		Time: now.Add(2 * time.Second),
	}
	event3 := &Event{
		ID:   "evt-3",
		Type: EventTypeRequestComplete,
		Time: now.Add(500 * time.Millisecond),
	}

	eq.Schedule(event1)
	eq.Schedule(event2)
	eq.Schedule(event3)

	if eq.Size() != 3 {
		t.Errorf("Expected queue size 3, got %d", eq.Size())
	}

	// Should get events in time order
	next := eq.Next()
	if next.ID != "evt-3" {
		t.Errorf("Expected first event to be evt-3, got %s", next.ID)
	}

	next = eq.Next()
	if next.ID != "evt-1" {
		t.Errorf("Expected second event to be evt-1, got %s", next.ID)
	}

	next = eq.Next()
	if next.ID != "evt-2" {
		t.Errorf("Expected third event to be evt-2, got %s", next.ID)
	}

	if !eq.IsEmpty() {
		t.Error("Queue should be empty after removing all events")
	}
}

func TestEventQueuePriority(t *testing.T) {
	eq := NewEventQueue()

	now := time.Now()
	// Same time, different priorities
	event1 := &Event{
		ID:       "evt-1",
		Type:     EventTypeRequestArrival,
		Time:     now,
		Priority: 10,
	}
	event2 := &Event{
		ID:       "evt-2",
		Type:     EventTypeRequestStart,
		Time:     now,
		Priority: 5,
	}
	event3 := &Event{
		ID:       "evt-3",
		Type:     EventTypeRequestComplete,
		Time:     now,
		Priority: 1,
	}

	eq.Schedule(event1)
	eq.Schedule(event2)
	eq.Schedule(event3)

	// Should get events in priority order (lower priority value = higher priority)
	next := eq.Next()
	if next.ID != "evt-3" {
		t.Errorf("Expected first event to be evt-3 (priority 1), got %s", next.ID)
	}

	next = eq.Next()
	if next.ID != "evt-2" {
		t.Errorf("Expected second event to be evt-2 (priority 5), got %s", next.ID)
	}

	next = eq.Next()
	if next.ID != "evt-1" {
		t.Errorf("Expected third event to be evt-1 (priority 10), got %s", next.ID)
	}
}

func TestEventQueuePeek(t *testing.T) {
	eq := NewEventQueue()

	now := time.Now()
	event := &Event{
		ID:   "evt-1",
		Type: EventTypeRequestArrival,
		Time: now,
	}

	eq.Schedule(event)

	// Peek should return event without removing it
	peeked := eq.Peek()
	if peeked.ID != "evt-1" {
		t.Errorf("Expected peeked event to be evt-1, got %s", peeked.ID)
	}

	if eq.Size() != 1 {
		t.Error("Peek should not remove event from queue")
	}

	// Next should return the same event
	next := eq.Next()
	if next.ID != "evt-1" {
		t.Errorf("Expected next event to be evt-1, got %s", next.ID)
	}

	if !eq.IsEmpty() {
		t.Error("Queue should be empty after Next()")
	}

	// Peek on empty queue should return nil
	peeked = eq.Peek()
	if peeked != nil {
		t.Error("Peek on empty queue should return nil")
	}
}

func TestEventQueueClear(t *testing.T) {
	eq := NewEventQueue()

	now := time.Now()
	for i := 0; i < 10; i++ {
		event := &Event{
			ID:   string(rune(i)),
			Type: EventTypeRequestArrival,
			Time: now.Add(time.Duration(i) * time.Second),
		}
		eq.Schedule(event)
	}

	if eq.Size() != 10 {
		t.Errorf("Expected queue size 10, got %d", eq.Size())
	}

	eq.Clear()

	if !eq.IsEmpty() {
		t.Error("Queue should be empty after Clear()")
	}
	if eq.Size() != 0 {
		t.Errorf("Expected queue size 0 after clear, got %d", eq.Size())
	}
}

func TestEventQueueConcurrency(t *testing.T) {
	eq := NewEventQueue()

	now := time.Now()
	numEvents := 100

	// Schedule events concurrently
	done := make(chan bool)
	for i := 0; i < numEvents; i++ {
		go func(id int) {
			event := &Event{
				ID:   string(rune(id)),
				Type: EventTypeRequestArrival,
				Time: now.Add(time.Duration(id) * time.Millisecond),
			}
			eq.Schedule(event)
			done <- true
		}(i)
	}

	// Wait for all events to be scheduled
	for i := 0; i < numEvents; i++ {
		<-done
	}

	if eq.Size() != numEvents {
		t.Errorf("Expected queue size %d, got %d", numEvents, eq.Size())
	}

	// Remove events concurrently
	for i := 0; i < numEvents; i++ {
		go func() {
			eq.Next()
			done <- true
		}()
	}

	// Wait for all events to be removed
	for i := 0; i < numEvents; i++ {
		<-done
	}

	if !eq.IsEmpty() {
		t.Errorf("Queue should be empty, got size %d", eq.Size())
	}
}

func TestEventTypes(t *testing.T) {
	types := []EventType{
		EventTypeRequestArrival,
		EventTypeRequestStart,
		EventTypeRequestComplete,
		EventTypeDownstreamCall,
		EventTypeScaleUp,
		EventTypeScaleDown,
		EventTypeSimulationEnd,
	}

	for _, eventType := range types {
		if eventType == "" {
			t.Errorf("Event type should not be empty")
		}
	}
}

func TestEventWithRequest(t *testing.T) {
	req := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "test-service",
		Endpoint:    "/test",
	}

	event := &Event{
		ID:      "evt-1",
		Type:    EventTypeRequestStart,
		Time:    time.Now(),
		Request: req,
	}

	if event.Request.ID != "req-1" {
		t.Errorf("Expected request ID 'req-1', got '%s'", event.Request.ID)
	}
	if event.Request.ServiceName != "test-service" {
		t.Errorf("Expected service name 'test-service', got '%s'", event.Request.ServiceName)
	}
}

func TestEventWithData(t *testing.T) {
	data := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	event := &Event{
		ID:   "evt-1",
		Type: EventTypeScaleUp,
		Time: time.Now(),
		Data: data,
	}

	if event.Data["key1"] != "value1" {
		t.Errorf("Expected data key1 to be 'value1', got '%v'", event.Data["key1"])
	}
	if event.Data["key2"] != 42 {
		t.Errorf("Expected data key2 to be 42, got '%v'", event.Data["key2"])
	}
	if event.Data["key3"] != true {
		t.Errorf("Expected data key3 to be true, got '%v'", event.Data["key3"])
	}
}
