package engine

import (
	"container/heap"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// EventType represents the type of simulation event
type EventType string

const (
	// EventTypeRequestArrival represents a new request arriving
	EventTypeRequestArrival EventType = "request_arrival"

	// EventTypeRequestStart represents a request starting processing
	EventTypeRequestStart EventType = "request_start"

	// EventTypeRequestComplete represents a request completing processing
	EventTypeRequestComplete EventType = "request_complete"

	// EventTypeDownstreamCall represents a call to a downstream service
	EventTypeDownstreamCall EventType = "downstream_call"

	// EventTypeScaleUp represents a service scaling up
	EventTypeScaleUp EventType = "scale_up"

	// EventTypeScaleDown represents a service scaling down
	EventTypeScaleDown EventType = "scale_down"

	// EventTypeSimulationEnd represents the end of the simulation
	EventTypeSimulationEnd EventType = "simulation_end"

	// EventTypeDrainSweep runs periodic replica drain processing independent of request traffic.
	EventTypeDrainSweep EventType = "drain_sweep"

	// EventTypeDownstreamTimeout fires when a downstream call exceeds timeout_ms (DES deadline).
	EventTypeDownstreamTimeout EventType = "downstream_timeout"

	// EventTypeDownstreamRetry schedules a replacement downstream attempt after simulated backoff (retry policy).
	EventTypeDownstreamRetry EventType = "downstream_retry"

	// EventTypeDownstreamCallerOverheadStart allocates caller CPU for downstream serialization / client overhead.
	EventTypeDownstreamCallerOverheadStart EventType = "downstream_caller_overhead_start"

	// EventTypeDownstreamCallerOverheadEnd releases caller CPU and schedules the downstream spawn (or retry spawn).
	EventTypeDownstreamCallerOverheadEnd EventType = "downstream_caller_overhead_end"

	// Queue / broker DES events (kind: queue services and downstream kind: queue).
	EventTypeQueueEnqueue        EventType = "queue_enqueue"
	EventTypeQueueDequeue        EventType = "queue_dequeue"
	EventTypeQueueAckTimeout     EventType = "queue_ack_timeout"
	EventTypeQueueRedelivery     EventType = "queue_redelivery"
	EventTypeQueueDLQ            EventType = "queue_dlq"
	EventTypeAsyncParentFinalize EventType = "async_parent_finalize"

	// Topic / pub-sub DES events (kind: topic services and downstream kind: topic).
	EventTypeTopicPublish    EventType = "topic_publish"
	EventTypeTopicDequeue    EventType = "topic_dequeue"
	EventTypeTopicAckTimeout EventType = "topic_ack_timeout"
	EventTypeTopicDLQ        EventType = "topic_dlq"
	// EventTypeTopicRetentionExpire removes queued messages past retention at DES time (per shard/partition/group).
	EventTypeTopicRetentionExpire EventType = "topic_retention_expire"
)

// Event represents a discrete event in the simulation
type Event struct {
	ID        string                 `json:"id"`
	Type      EventType              `json:"type"`
	Time      time.Time              `json:"time"`
	Priority  int                    `json:"priority"` // Lower values = higher priority
	Sequence  int64                  `json:"sequence,omitempty"`
	Request   *models.Request        `json:"request,omitempty"`
	ServiceID string                 `json:"service_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// EventQueue is a priority queue of events ordered by time
type EventQueue struct {
	events []*Event
	mu     sync.RWMutex
}

// NewEventQueue creates a new event queue
func NewEventQueue() *EventQueue {
	eq := &EventQueue{
		events: make([]*Event, 0),
	}
	heap.Init(eq)
	return eq
}

// Len returns the number of events in the queue
func (eq *EventQueue) Len() int {
	return len(eq.events)
}

// Less compares two events by time, priority, then schedule order (Sequence).
func (eq *EventQueue) Less(i, j int) bool {
	a, b := eq.events[i], eq.events[j]
	if a.Time.Before(b.Time) {
		return true
	}
	if a.Time.After(b.Time) {
		return false
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	return a.Sequence < b.Sequence
}

// Swap swaps two events in the queue
func (eq *EventQueue) Swap(i, j int) {
	eq.events[i], eq.events[j] = eq.events[j], eq.events[i]
}

// Push adds an event to the queue
func (eq *EventQueue) Push(x interface{}) {
	eq.events = append(eq.events, x.(*Event))
}

// Pop removes and returns the next event from the queue
func (eq *EventQueue) Pop() interface{} {
	old := eq.events
	n := len(old)
	event := old[n-1]
	old[n-1] = nil // avoid memory leak
	eq.events = old[0 : n-1]
	return event
}

// Schedule adds an event to the queue (thread-safe)
func (eq *EventQueue) Schedule(event *Event) {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	heap.Push(eq, event)
}

// Next removes and returns the next event (thread-safe)
func (eq *EventQueue) Next() *Event {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	if eq.Len() == 0 {
		return nil
	}
	return heap.Pop(eq).(*Event)
}

// Peek returns the next event without removing it (thread-safe)
func (eq *EventQueue) Peek() *Event {
	eq.mu.RLock()
	defer eq.mu.RUnlock()
	if eq.Len() == 0 {
		return nil
	}
	return eq.events[0]
}

// Clear removes all events from the queue (thread-safe)
func (eq *EventQueue) Clear() {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	eq.events = make([]*Event, 0)
	heap.Init(eq)
}

// Size returns the current queue size (thread-safe)
func (eq *EventQueue) Size() int {
	eq.mu.RLock()
	defer eq.mu.RUnlock()
	return eq.Len()
}

// IsEmpty returns true if the queue is empty (thread-safe)
func (eq *EventQueue) IsEmpty() bool {
	return eq.Size() == 0
}
