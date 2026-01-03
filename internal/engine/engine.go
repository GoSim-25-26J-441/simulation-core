package engine

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// Engine is the discrete-event simulation engine
type Engine struct {
	eventQueue   *EventQueue
	runManager   *RunManager
	simTime      *utils.SimTime
	handlers     map[EventType]EventHandler
	logger       *slog.Logger
	eventCounter int64
}

// EventHandler is a function that handles a specific event type
type EventHandler func(*Engine, *Event) error

// NewEngine creates a new simulation engine
func NewEngine(runID string) *Engine {
	return &Engine{
		eventQueue: NewEventQueue(),
		runManager: NewRunManager(runID),
		simTime:    utils.NewSimTime(time.Now()),
		handlers:   make(map[EventType]EventHandler),
		logger:     logger.Default,
	}
}

// SetLogger sets the engine's logger
func (e *Engine) SetLogger(l *slog.Logger) {
	e.logger = l
}

// RegisterHandler registers an event handler
func (e *Engine) RegisterHandler(eventType EventType, handler EventHandler) {
	e.handlers[eventType] = handler
}

// ScheduleEvent schedules an event
func (e *Engine) ScheduleEvent(event *Event) {
	e.eventCounter++
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d", e.eventCounter)
	}
	e.eventQueue.Schedule(event)

	e.logger.Debug("Event scheduled",
		"event_id", event.ID,
		"type", event.Type,
		"time", event.Time,
		"queue_size", e.eventQueue.Size())
}

// ScheduleAt schedules an event at a specific simulation time
func (e *Engine) ScheduleAt(eventType EventType, simTime time.Time, request *models.Request, serviceID string, data map[string]interface{}) {
	event := &Event{
		Type:      eventType,
		Time:      simTime,
		Priority:  0,
		Request:   request,
		ServiceID: serviceID,
		Data:      data,
	}
	e.ScheduleEvent(event)
}

// ScheduleAfter schedules an event after a duration from current simulation time
func (e *Engine) ScheduleAfter(eventType EventType, delay time.Duration, request *models.Request, serviceID string, data map[string]interface{}) {
	simTime := e.simTime.Now().Add(delay)
	e.ScheduleAt(eventType, simTime, request, serviceID, data)
}

// Run executes the simulation
func (e *Engine) Run(duration time.Duration) error {
	e.logger.Info("Starting simulation",
		"run_id", e.runManager.run.ID,
		"duration", duration)

	e.runManager.Start()
	defer func() {
		if e.runManager.run.Status == models.RunStatusRunning {
			e.runManager.Complete()
		}
	}()

	startTime := e.simTime.Now()
	endTime := startTime.Add(duration)

	// Schedule simulation end event
	e.ScheduleAt(EventTypeSimulationEnd, endTime, nil, "", nil)

	// Event loop
	for !e.eventQueue.IsEmpty() {
		// Check if context is cancelled
		select {
		case <-e.runManager.Context().Done():
			e.logger.Info("Simulation cancelled")
			return fmt.Errorf("simulation cancelled")
		default:
		}

		// Get next event
		event := e.eventQueue.Next()
		if event == nil {
			break
		}

		// Advance simulation time
		e.simTime.Set(event.Time)

		// Log event processing
		e.logger.Debug("Processing event",
			"event_id", event.ID,
			"type", event.Type,
			"sim_time", event.Time,
			"queue_size", e.eventQueue.Size())

		// Handle simulation end
		if event.Type == EventTypeSimulationEnd {
			e.logger.Info("Simulation ended",
				"sim_time", event.Time,
				"events_processed", e.eventCounter)
			break
		}

		// Find and execute handler
		handler, ok := e.handlers[event.Type]
		if !ok {
			e.logger.Warn("No handler for event type",
				"event_type", event.Type,
				"event_id", event.ID)
			continue
		}

		// Execute handler
		if err := handler(e, event); err != nil {
			e.logger.Error("Event handler error",
				"event_id", event.ID,
				"type", event.Type,
				"error", err)
			// Continue processing other events
		}
	}

	e.logger.Info("Simulation completed",
		"run_id", e.runManager.run.ID,
		"duration", e.simTime.Since(startTime),
		"events_processed", e.eventCounter)

	return nil
}

// GetSimTime returns the current simulation time
func (e *Engine) GetSimTime() time.Time {
	return e.simTime.Now()
}

// GetRunManager returns the run manager
func (e *Engine) GetRunManager() *RunManager {
	return e.runManager
}

// GetEventQueue returns the event queue
func (e *Engine) GetEventQueue() *EventQueue {
	return e.eventQueue
}

// Stop stops the simulation
func (e *Engine) Stop() {
	e.runManager.Cancel()
	e.eventQueue.Clear()
	e.logger.Info("Simulation stopped")
}

// GetStats returns current simulation statistics
func (e *Engine) GetStats() map[string]interface{} {
	stats := e.runManager.GetStats()
	stats["sim_time"] = e.simTime.Now().Format(time.RFC3339)
	stats["events_in_queue"] = e.eventQueue.Size()
	stats["events_processed"] = e.eventCounter
	return stats
}

// PrintStats prints simulation statistics
func (e *Engine) PrintStats() {
	stats := e.GetStats()
	e.logger.Info("Simulation statistics",
		"status", stats["status"],
		"elapsed", stats["elapsed"],
		"total_requests", stats["total_requests"],
		"completed_requests", stats["completed_requests"],
		"failed_requests", stats["failed_requests"],
		"current_p50_ms", stats["current_p50_ms"],
		"current_p95_ms", stats["current_p95_ms"],
		"throughput_rps", stats["throughput_rps"])
}
