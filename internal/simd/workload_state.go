package simd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// WorkloadPatternState tracks the state of a workload pattern during simulation
type WorkloadPatternState struct {
	Pattern       config.WorkloadPattern
	ServiceID     string
	EndpointPath  string
	LastEventTime time.Time
	NextEventTime time.Time
	Active        bool
	mu            sync.RWMutex
}

// WorkloadState manages workload patterns for a simulation run with continuous event generation
type WorkloadState struct {
	runID     string
	patterns  map[string]*WorkloadPatternState // key: "from:to"
	generator *utils.RandSource
	engine    *engine.Engine
	ctx       context.Context
	cancel    context.CancelFunc
	endTime   time.Time
	mu        sync.RWMutex
}

// NewWorkloadState creates a new workload state manager
func NewWorkloadState(runID string, eng *engine.Engine, endTime time.Time) *WorkloadState {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkloadState{
		runID:     runID,
		patterns:  make(map[string]*WorkloadPatternState),
		generator: utils.NewRandSource(time.Now().UnixNano()),
		engine:    eng,
		ctx:       ctx,
		cancel:    cancel,
		endTime:   endTime,
	}
}

// Start initializes workload patterns and begins continuous event generation
func (ws *WorkloadState) Start(scenario *config.Scenario, startTime time.Time) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Initialize patterns from scenario
	for _, workloadPattern := range scenario.Workload {
		// Parse target: "serviceID:path"
		serviceID, endpointPath, err := interaction.ParseDownstreamTarget(workloadPattern.To)
		if err != nil {
			return fmt.Errorf("invalid workload target %s: %w", workloadPattern.To, err)
		}

		patternKey := patternKey(workloadPattern.From, workloadPattern.To)
		// Calculate first event time immediately to start generating events
		firstEventTime := ws.calculateNextArrivalTime(workloadPattern.Arrival, startTime)
		ws.patterns[patternKey] = &WorkloadPatternState{
			Pattern:       workloadPattern,
			ServiceID:     serviceID,
			EndpointPath:  endpointPath,
			LastEventTime: startTime,
			NextEventTime: firstEventTime,
			Active:        true,
		}
	}

	// Start continuous event generation
	go ws.generateEventsLoop()

	return nil
}

// Stop stops the workload state manager
func (ws *WorkloadState) Stop() {
	ws.cancel()
}

// UpdateRate updates the rate for a specific workload pattern
func (ws *WorkloadState) UpdateRate(patternKey string, newRateRPS float64) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	patternState, ok := ws.patterns[patternKey]
	if !ok {
		return fmt.Errorf("workload pattern not found: %s", patternKey)
	}

	patternState.mu.Lock()
	defer patternState.mu.Unlock()

	// Update the rate in the pattern
	patternState.Pattern.Arrival.RateRPS = newRateRPS
	// Reset next event time to trigger immediate recalculation
	currentSimTime := ws.engine.GetSimTime()
	patternState.NextEventTime = currentSimTime

	logger.Info("workload rate updated",
		"run_id", ws.runID,
		"pattern", patternKey,
		"new_rate_rps", newRateRPS)

	return nil
}

// UpdatePattern updates an entire workload pattern
func (ws *WorkloadState) UpdatePattern(patternKey string, pattern config.WorkloadPattern) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	patternState, ok := ws.patterns[patternKey]
	if !ok {
		return fmt.Errorf("workload pattern not found: %s", patternKey)
	}

	// Parse target
	serviceID, endpointPath, err := interaction.ParseDownstreamTarget(pattern.To)
	if err != nil {
		return fmt.Errorf("invalid workload target %s: %w", pattern.To, err)
	}

	patternState.mu.Lock()
	defer patternState.mu.Unlock()

	patternState.Pattern = pattern
	patternState.ServiceID = serviceID
	patternState.EndpointPath = endpointPath
	currentSimTime := ws.engine.GetSimTime()
	patternState.NextEventTime = currentSimTime

	logger.Info("workload pattern updated",
		"run_id", ws.runID,
		"pattern", patternKey)

	return nil
}

// GetPattern returns a workload pattern by key
func (ws *WorkloadState) GetPattern(patternKey string) (*WorkloadPatternState, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	pattern, ok := ws.patterns[patternKey]
	return pattern, ok
}

// GetAllPatterns returns all workload patterns
func (ws *WorkloadState) GetAllPatterns() map[string]*WorkloadPatternState {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	result := make(map[string]*WorkloadPatternState)
	for k, v := range ws.patterns {
		result[k] = v
	}
	return result
}

// patternKey generates a unique key for a workload pattern
func patternKey(from, to string) string {
	return fmt.Sprintf("%s:%s", from, to)
}

// generateEventsLoop continuously generates arrival events based on current patterns
func (ws *WorkloadState) generateEventsLoop() {
	// Generate initial batch of events immediately
	ws.generateNextEvents()

	ticker := time.NewTicker(100 * time.Millisecond) // Check every 100ms
	defer ticker.Stop()

	for {
		select {
		case <-ws.ctx.Done():
			return
		case <-ticker.C:
			ws.generateNextEvents()
		}
	}
}

// generateNextEvents generates the next batch of arrival events for active patterns
func (ws *WorkloadState) generateNextEvents() {
	ws.mu.RLock()
	currentSimTime := ws.engine.GetSimTime()

	// Generate events up to 1 second ahead
	lookaheadTime := currentSimTime.Add(1 * time.Second)
	if lookaheadTime.After(ws.endTime) {
		lookaheadTime = ws.endTime
	}

	// Make a copy of patterns to iterate over while holding read lock
	patterns := make([]*WorkloadPatternState, 0, len(ws.patterns))
	for _, pattern := range ws.patterns {
		patterns = append(patterns, pattern)
	}
	ws.mu.RUnlock()

	for _, patternState := range patterns {
		patternState.mu.Lock()
		if !patternState.Active || currentSimTime.After(ws.endTime) {
			patternState.mu.Unlock()
			continue
		}

		// Generate events until we've scheduled up to lookaheadTime
		for patternState.NextEventTime.Before(lookaheadTime) && patternState.NextEventTime.Before(ws.endTime) {
			// Schedule the arrival event
			ws.engine.ScheduleAt(
				engine.EventTypeRequestArrival,
				patternState.NextEventTime,
				nil,
				patternState.ServiceID,
				map[string]interface{}{
					"service_id":    patternState.ServiceID,
					"endpoint_path": patternState.EndpointPath,
				},
			)

			// Calculate next event time based on arrival type and rate
			nextTime := ws.calculateNextArrivalTime(
				patternState.Pattern.Arrival,
				patternState.NextEventTime,
			)
			patternState.LastEventTime = patternState.NextEventTime
			patternState.NextEventTime = nextTime
		}
		patternState.mu.Unlock()
	}
}

// calculateNextArrivalTime calculates the next arrival time based on arrival spec
func (ws *WorkloadState) calculateNextArrivalTime(arrival config.ArrivalSpec, currentTime time.Time) time.Time {
	switch arrival.Type {
	case "poisson", "exponential":
		// Exponential inter-arrival time
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = 1.0 // Default to 1 RPS
		}
		interArrivalSeconds := ws.generator.ExpFloat64(rateRPS)
		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "uniform":
		// Uniform distribution - constant inter-arrival time
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = 1.0
		}
		interArrivalSeconds := 1.0 / rateRPS
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "normal", "gaussian":
		// Normal distribution
		meanRateRPS := arrival.RateRPS
		if meanRateRPS <= 0 {
			meanRateRPS = 1.0
		}
		stddevRPS := arrival.StdDevRPS
		if stddevRPS <= 0 {
			stddevRPS = meanRateRPS * 0.1 // Default 10% stddev
		}
		meanInterArrivalSeconds := 1.0 / meanRateRPS
		stddevInterArrivalSeconds := stddevRPS / (meanRateRPS * meanRateRPS)
		interArrivalSeconds := ws.generator.NormFloat64(meanInterArrivalSeconds, stddevInterArrivalSeconds)
		if interArrivalSeconds < 0.001 { // Minimum 1ms
			interArrivalSeconds = 0.001
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "constant":
		// Constant rate
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = 1.0
		}
		interArrivalSeconds := 1.0 / rateRPS
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "bursty":
		// Bursty - simplified: use base rate for now
		// TODO: Implement full bursty logic
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = 1.0
		}
		interArrivalSeconds := ws.generator.ExpFloat64(rateRPS)
		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	default:
		// Default to poisson
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = 1.0
		}
		interArrivalSeconds := ws.generator.ExpFloat64(rateRPS)
		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))
	}
}
