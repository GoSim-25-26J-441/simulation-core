package engine

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// Engine is the discrete-event simulation engine
type Engine struct {
	eventQueue    *EventQueue
	runManager    *RunManager
	simTime       *utils.SimTime
	handlers      map[EventType]EventHandler
	logger        *slog.Logger
	eventCounter  int64
	realTimeStart time.Time // Real-time start for throttling
	realTimeMode  bool      // If true, throttle simulation to run in real-time
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

// SetRealTimeMode enables or disables real-time throttling
// When enabled, the simulation will throttle to run in real-time,
// making it suitable for real-time dashboards and monitoring
func (e *Engine) SetRealTimeMode(enabled bool) {
	e.realTimeMode = enabled
}

// RegisterHandler registers an event handler
func (e *Engine) RegisterHandler(eventType EventType, handler EventHandler) {
	e.handlers[eventType] = handler
}

// ScheduleEvent schedules an event
func (e *Engine) ScheduleEvent(event *Event) {
	counter := atomic.AddInt64(&e.eventCounter, 1)
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d", counter)
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
		"duration", duration,
		"real_time_mode", e.realTimeMode)

	e.runManager.Start()
	defer func() {
		if e.runManager.run.Status == models.RunStatusRunning {
			e.runManager.Complete()
		}
	}()

	startTime := e.simTime.Now()
	endTime := startTime.Add(duration)
	e.realTimeStart = time.Now() // Track real-time start for throttling

	// Schedule simulation end event
	e.ScheduleAt(EventTypeSimulationEnd, endTime, nil, "", nil)
	e.logger.Info("Simulation end event scheduled",
		"start_time", startTime,
		"end_time", endTime,
		"duration", duration)

	// Event loop - continue until simulation end event is processed
	for {
		// Check if context is cancelled
		select {
		case <-e.runManager.Context().Done():
			e.logger.Info("Simulation cancelled")
			return fmt.Errorf("simulation cancelled")
		default:
		}

		// Get next event
		event := e.eventQueue.Next()

		// If queue is empty, check if we should end the simulation
		if event == nil {
			currentSimTime := e.simTime.Now()
			if currentSimTime.Before(endTime) {
				// Queue is empty but we haven't reached end time yet
				// Check if simulation end event exists in queue (peek without removing)
				nextEvent := e.eventQueue.Peek()
				if nextEvent != nil && nextEvent.Type == EventTypeSimulationEnd {
					// Simulation end event exists - wait for it to be processed naturally
					// This shouldn't happen if queue is empty, but check anyway
					time.Sleep(50 * time.Millisecond)
					continue
				}
				// Wait briefly to allow event generation loop to add more events
				time.Sleep(100 * time.Millisecond)
				// Re-check queue
				if e.eventQueue.IsEmpty() {
					// Still empty - check if we should advance time or wait more
					timeUntilEnd := endTime.Sub(currentSimTime)
					if timeUntilEnd < 200*time.Millisecond {
						// Very close to end time - advance simulation time to end and exit
						e.simTime.Set(endTime)
						e.logger.Info("Simulation ended - advanced to end time (queue empty)",
							"sim_time", endTime,
							"events_processed", atomic.LoadInt64(&e.eventCounter))
						break
					}
					// Still far from end time - continue waiting
					continue
				}
				// Queue has events now, continue to process them
				continue
			} else {
				// We've reached or passed end time, simulation should end
				e.logger.Info("Simulation ended - reached end time",
					"sim_time", currentSimTime,
					"end_time", endTime,
					"events_processed", atomic.LoadInt64(&e.eventCounter))
				break
			}
		}

		// Advance simulation time to event time
		// This is the key: simulation time only advances when processing events
		previousSimTime := e.simTime.Now()

		// For simulation end event, ensure we don't process it before we should
		if event.Type == EventTypeSimulationEnd {
			// If event time is before endTime, something is wrong - reschedule it
			if event.Time.Before(endTime) {
				e.logger.Warn("Simulation end event scheduled at wrong time, rescheduling",
					"event_time", event.Time,
					"end_time", endTime)
				e.ScheduleAt(EventTypeSimulationEnd, endTime, nil, "", nil)
				continue
			}
			// If current simulation time is before event time, we shouldn't process it yet
			// But in discrete-event simulation, we process events in order, so this should be fine
			// However, if simulation time hasn't advanced enough, we might need to wait
			if previousSimTime.Before(event.Time.Add(-100 * time.Millisecond)) {
				// Simulation time is significantly before event time - this might indicate
				// that events aren't being generated properly. Log a warning but continue.
				e.logger.Debug("Processing simulation end event - simulation time will advance",
					"current_sim_time", previousSimTime,
					"event_time", event.Time)
			}
		}

		// Advance simulation time to event time
		simTimeAdvanced := event.Time.Sub(previousSimTime)
		e.simTime.Set(event.Time)
		currentSimTime := e.simTime.Now()

		// Real-time throttling: if enabled, wait in real-time to match simulation time advancement
		if e.realTimeMode && simTimeAdvanced > 0 {
			elapsedRealTime := time.Since(e.realTimeStart)
			elapsedSimTime := currentSimTime.Sub(startTime)

			// If we're ahead of real-time, wait to catch up
			// We want: elapsedRealTime >= elapsedSimTime (real time should match or exceed sim time)
			if elapsedRealTime < elapsedSimTime {
				waitTime := elapsedSimTime - elapsedRealTime
				// Cap wait time to avoid excessive delays (max 1 second per event)
				if waitTime > time.Second {
					waitTime = time.Second
				}
				if waitTime > 0 {
					time.Sleep(waitTime)
				}
			}
		}

		// Log event processing
		e.logger.Debug("Processing event",
			"event_id", event.ID,
			"type", event.Type,
			"previous_sim_time", previousSimTime,
			"new_sim_time", currentSimTime,
			"event_time", event.Time,
			"sim_time_advanced", currentSimTime.Sub(previousSimTime),
			"queue_size", e.eventQueue.Size())

		// Handle simulation end
		if event.Type == EventTypeSimulationEnd {
			// Verify the event time matches endTime (allowing for small floating point differences)
			timeDiff := event.Time.Sub(endTime)
			if timeDiff < -time.Millisecond || timeDiff > time.Millisecond {
				e.logger.Warn("Simulation end event time doesn't match expected end time",
					"event_time", event.Time,
					"end_time", endTime,
					"diff", timeDiff)
			}
			e.logger.Info("Simulation ended",
				"sim_time", event.Time,
				"end_time", endTime,
				"sim_duration", event.Time.Sub(startTime),
				"events_processed", atomic.LoadInt64(&e.eventCounter))
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
		"events_processed", atomic.LoadInt64(&e.eventCounter))

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
	stats["events_processed"] = atomic.LoadInt64(&e.eventCounter)
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
