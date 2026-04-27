package simd

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

const (
	// DefaultFallbackRateRPS is the default rate used when an invalid rate is specified
	DefaultFallbackRateRPS = 1.0
	// DefaultStdDevPercentage is the default standard deviation as a percentage of mean rate for normal distribution
	DefaultStdDevPercentage = 0.1
	// MinInterArrivalTimeSeconds is the minimum inter-arrival time to prevent extremely rapid event generation
	MinInterArrivalTimeSeconds = 0.001
	// EventGenerationLookaheadWindow is how far ahead events are pre-generated
	// Increased to ensure events are available throughout the simulation duration
	EventGenerationLookaheadWindow = 10 * time.Second
	// EventGenerationTickerInterval is the interval at which the event generation loop checks for new events to generate
	EventGenerationTickerInterval = 500 * time.Millisecond
)

// WorkloadPatternState tracks the state of a workload pattern during simulation
type WorkloadPatternState struct {
	Pattern      config.WorkloadPattern
	ServiceID    string
	EndpointPath string
	// Epoch is simulation start time for this pattern; used to align bursty burst/quiet cycles
	// and (for lazy uniform) as the anchor for cumulative expected arrivals per chunk.
	Epoch         time.Time
	LastEventTime time.Time
	NextEventTime time.Time
	// uniformTimes is a sorted schedule of arrivals for type "uniform" (i.i.d. uniform in [start,end)),
	// matching internal/workload.Generator.scheduleUniformArrivals. uniformCursor indexes NextEventTime in that slice.
	uniformTimes  []time.Time
	uniformCursor int
	// uniformLazy: real-time uniform loads chunks of EventGenerationLookaheadWindow instead of the full [start,endTime].
	uniformLazy            bool
	uniformStreamWatermark time.Time // exclusive end of simulation time range already sampled (lazy only)
	Active                 bool
	mu                     sync.RWMutex
}

// WorkloadState manages workload patterns for a simulation run with continuous event generation
type WorkloadState struct {
	runID     string
	patterns  map[string]*WorkloadPatternState // key: "from:to"
	generator *utils.RandSource
	engine    *engine.Engine
	ctx       context.Context
	cancel    context.CancelFunc
	endTime   time.Time // Immutable after initialization, safe to read without lock
	// realTimeMode matches WorkloadState.Start(..., realTime); used for uniform chunking and updates.
	realTimeMode     bool
	generatedHorizon time.Time
	mu               sync.RWMutex
}

// NewWorkloadState creates a new workload state manager.
// seed 0 uses a non-deterministic base; non-zero values derive a stable workload RNG stream (seed+1).
func NewWorkloadState(runID string, eng *engine.Engine, endTime time.Time, seed int64) *WorkloadState {
	wsSeed := seed
	if wsSeed == 0 {
		wsSeed = time.Now().UnixNano()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkloadState{
		runID:     runID,
		patterns:  make(map[string]*WorkloadPatternState),
		generator: utils.NewRandSource(wsSeed + 1),
		engine:    eng,
		ctx:       ctx,
		cancel:    cancel,
		endTime:   endTime,
	}
}

// Start initializes workload patterns and either pre-generates all arrival events (non-real-time)
// or starts continuous event generation (real-time mode).
func (ws *WorkloadState) Start(scenario *config.Scenario, startTime time.Time, realTime bool) error {
	ws.mu.Lock()
	ws.realTimeMode = realTime
	if err := ws.initPatternsLocked(scenario, startTime, realTime); err != nil {
		ws.mu.Unlock()
		return err
	}
	ws.mu.Unlock()

	if realTime {
		// Seed initial events synchronously to avoid startup races where the
		// simulation end event is processed before workload arrivals are queued.
		ws.generateNextEvents()
		// Start continuous event generation (ticker-based loop)
		go ws.generateEventsLoop()
		return nil
	}

	// Non-real-time: generate a bounded initial window and refill using simulation-time events.
	initialHorizon := startTime.Add(EventGenerationLookaheadWindow)
	if initialHorizon.After(ws.endTime) {
		initialHorizon = ws.endTime
	}
	generated := ws.generateUpToHorizon(initialHorizon)
	ws.mu.Lock()
	ws.generatedHorizon = initialHorizon
	ws.mu.Unlock()
	logger.Debug("standard workload initial chunk generated",
		"run_id", ws.runID,
		"generated_horizon", initialHorizon,
		"arrivals_generated", generated)
	if initialHorizon.Before(ws.endTime) {
		ws.engine.RegisterHandler(engine.EventTypeWorkloadGenerate, ws.handleWorkloadGenerate())
		ws.engine.ScheduleAt(engine.EventTypeWorkloadGenerate, initialHorizon, nil, "", nil)
	}
	return nil
}

func (ws *WorkloadState) initPatternsLocked(scenario *config.Scenario, startTime time.Time, realTime bool) error {
	ws.patterns = make(map[string]*WorkloadPatternState)
	for _, workloadPattern := range scenario.Workload {
		serviceID, endpointPath, err := interaction.ParseDownstreamTarget(workloadPattern.To)
		if err != nil {
			return fmt.Errorf("invalid workload target %s: %w", workloadPattern.To, err)
		}
		key := patternKey(workloadPattern.From, workloadPattern.To)
		arrival := workloadPattern.Arrival
		var firstEventTime time.Time
		var uniformTimes []time.Time
		uniformLazy := false
		var uniformWatermark time.Time
		if arrival.Type == "uniform" {
			if realTime {
				uniformLazy = true
				uniformWatermark = startTime
			} else {
				uniformTimes = ws.sampleUniformArrivalTimes(startTime, ws.endTime, arrival.RateRPS)
				if len(uniformTimes) == 0 {
					firstEventTime = ws.endTime
				} else {
					firstEventTime = uniformTimes[0]
				}
			}
		} else {
			firstEventTime = ws.calculateNextArrivalTime(arrival, startTime, startTime)
		}
		ps := &WorkloadPatternState{
			Pattern:                workloadPattern,
			ServiceID:              serviceID,
			EndpointPath:           endpointPath,
			Epoch:                  startTime,
			LastEventTime:          startTime,
			NextEventTime:          firstEventTime,
			uniformTimes:           uniformTimes,
			uniformCursor:          0,
			uniformLazy:            uniformLazy,
			uniformStreamWatermark: uniformWatermark,
			Active:                 true,
		}
		if arrival.Type == "uniform" && realTime {
			ws.ensureUniformHorizon(ps, startTime.Add(EventGenerationLookaheadWindow))
			if len(ps.uniformTimes) > 0 {
				ps.NextEventTime = ps.uniformTimes[0]
			} else {
				ps.NextEventTime = ws.endTime
			}
		}
		ws.patterns[key] = ps
	}
	return nil
}

// generateAllEventsUpToEndTime schedules all arrival events from each pattern's
// NextEventTime up to ws.endTime. Used when not in real-time mode.
func (ws *WorkloadState) generateAllEventsUpToEndTime() {
	ws.mu.RLock()
	patterns := make([]*WorkloadPatternState, 0, len(ws.patterns))
	for _, patternState := range ws.patterns {
		patterns = append(patterns, patternState)
	}
	ws.mu.RUnlock()

	for _, patternState := range patterns {
		patternState.mu.Lock()
		if !patternState.Active {
			patternState.mu.Unlock()
			continue
		}
		for patternState.NextEventTime.Before(ws.endTime) {
			ws.engine.ScheduleAt(
				engine.EventTypeRequestArrival,
				patternState.NextEventTime,
				nil,
				patternState.ServiceID,
				workloadArrivalEventData(patternState),
			)
			nextTime := ws.advanceToNextArrival(patternState, patternState.NextEventTime)
			patternState.LastEventTime = patternState.NextEventTime
			patternState.NextEventTime = nextTime
		}
		patternState.mu.Unlock()
	}
}

func (ws *WorkloadState) handleWorkloadGenerate() engine.EventHandler {
	return func(eng *engine.Engine, _ *engine.Event) error {
		select {
		case <-ws.ctx.Done():
			return nil
		default:
		}
		current := eng.GetSimTime()
		nextHorizon := current.Add(EventGenerationLookaheadWindow)
		if nextHorizon.After(ws.endTime) {
			nextHorizon = ws.endTime
		}
		generated := ws.generateUpToHorizon(nextHorizon)
		ws.mu.Lock()
		if nextHorizon.After(ws.generatedHorizon) {
			ws.generatedHorizon = nextHorizon
		}
		ws.mu.Unlock()
		logger.Debug("standard workload chunk generated",
			"run_id", ws.runID,
			"current_sim_time", current,
			"generated_horizon", nextHorizon,
			"arrivals_generated", generated)
		if nextHorizon.Before(ws.endTime) {
			eng.ScheduleAt(engine.EventTypeWorkloadGenerate, nextHorizon, nil, "", nil)
		}
		return nil
	}
}

func workloadArrivalEventData(patternState *WorkloadPatternState) map[string]interface{} {
	data := map[string]interface{}{
		"service_id":    patternState.ServiceID,
		"endpoint_path": patternState.EndpointPath,
		"from":          patternState.Pattern.From,
		"source_kind":   patternState.Pattern.SourceKind,
		"traffic_class": patternState.Pattern.TrafficClass,
	}
	if len(patternState.Pattern.Metadata) > 0 {
		md := make(map[string]interface{}, len(patternState.Pattern.Metadata))
		for k, v := range patternState.Pattern.Metadata {
			md[k] = v
		}
		data["metadata"] = md
	}
	return data
}

// Engine returns the simulation engine for this run (e.g. current simulation time).
func (ws *WorkloadState) Engine() *engine.Engine {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.engine
}

// Stop stops the workload state manager
func (ws *WorkloadState) Stop() {
	ws.cancel()
}

func (ws *WorkloadState) GeneratedHorizon() time.Time {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.generatedHorizon
}

// UpdateRate updates the rate for a specific workload pattern
func (ws *WorkloadState) UpdateRate(patternKey string, newRateRPS float64) error {
	if newRateRPS <= 0 {
		return fmt.Errorf("rate must be positive, got: %f", newRateRPS)
	}

	ws.mu.RLock()
	patternState, ok := ws.patterns[patternKey]
	ws.mu.RUnlock()

	if !ok {
		return fmt.Errorf("workload pattern not found: %s", patternKey)
	}

	patternState.mu.Lock()
	defer patternState.mu.Unlock()

	// Update the rate in the pattern
	patternState.Pattern.Arrival.RateRPS = newRateRPS
	currentSimTime := ws.engine.GetSimTime()
	if patternState.Pattern.Arrival.Type == "uniform" {
		if ws.realTimeMode {
			patternState.uniformLazy = true
			patternState.uniformTimes = nil
			patternState.uniformCursor = 0
			patternState.uniformStreamWatermark = currentSimTime
			patternState.Epoch = currentSimTime
			ws.ensureUniformHorizon(patternState, currentSimTime.Add(EventGenerationLookaheadWindow))
			if len(patternState.uniformTimes) > 0 {
				patternState.NextEventTime = patternState.uniformTimes[0]
			} else {
				patternState.NextEventTime = ws.endTime
			}
		} else {
			patternState.uniformLazy = false
			patternState.uniformStreamWatermark = time.Time{}
			patternState.uniformTimes = ws.sampleUniformArrivalTimes(currentSimTime, ws.endTime, newRateRPS)
			patternState.uniformCursor = 0
			patternState.Epoch = currentSimTime
			if len(patternState.uniformTimes) > 0 {
				patternState.NextEventTime = patternState.uniformTimes[0]
			} else {
				patternState.NextEventTime = ws.endTime
			}
		}
	} else {
		// Reset next event time to trigger immediate recalculation
		patternState.NextEventTime = currentSimTime
	}

	logger.Info("workload rate updated",
		"run_id", ws.runID,
		"pattern", patternKey,
		"new_rate_rps", newRateRPS)

	return nil
}

// UpdatePattern updates an entire workload pattern
func (ws *WorkloadState) UpdatePattern(patternKey string, pattern config.WorkloadPattern) error {
	ws.mu.RLock()
	patternState, ok := ws.patterns[patternKey]
	ws.mu.RUnlock()

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
	// Restart burst/quiet cycle alignment from now so mid-run pattern changes
	// (especially bursty timing) do not stay tied to the original simulation start.
	patternState.Epoch = currentSimTime
	if pattern.Arrival.Type == "uniform" {
		if ws.realTimeMode {
			patternState.uniformLazy = true
			patternState.uniformTimes = nil
			patternState.uniformCursor = 0
			patternState.uniformStreamWatermark = currentSimTime
			ws.ensureUniformHorizon(patternState, currentSimTime.Add(EventGenerationLookaheadWindow))
			if len(patternState.uniformTimes) > 0 {
				patternState.NextEventTime = patternState.uniformTimes[0]
			} else {
				patternState.NextEventTime = ws.endTime
			}
		} else {
			patternState.uniformLazy = false
			patternState.uniformStreamWatermark = time.Time{}
			patternState.uniformTimes = ws.sampleUniformArrivalTimes(currentSimTime, ws.endTime, pattern.Arrival.RateRPS)
			patternState.uniformCursor = 0
			if len(patternState.uniformTimes) > 0 {
				patternState.NextEventTime = patternState.uniformTimes[0]
			} else {
				patternState.NextEventTime = ws.endTime
			}
		}
	} else {
		patternState.uniformTimes = nil
		patternState.uniformCursor = 0
		patternState.uniformLazy = false
		patternState.uniformStreamWatermark = time.Time{}
		patternState.NextEventTime = currentSimTime
	}

	logger.Info("workload pattern updated",
		"run_id", ws.runID,
		"pattern", patternKey)

	return nil
}

// GetPattern returns a deep copy of a workload pattern by key to prevent concurrent access issues.
// The returned copy is a snapshot and should be treated as read-only.
func (ws *WorkloadState) GetPattern(patternKey string) (*WorkloadPatternState, bool) {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	pattern, ok := ws.patterns[patternKey]
	if !ok {
		return nil, false
	}

	// Return a deep copy to prevent concurrent access issues.
	// Note: The mutex is not initialized in the copy as the returned state is intended
	// to be a read-only snapshot and callers should not perform locking operations on it.
	pattern.mu.RLock()
	defer pattern.mu.RUnlock()

	copy := &WorkloadPatternState{
		Pattern:                pattern.Pattern,
		ServiceID:              pattern.ServiceID,
		EndpointPath:           pattern.EndpointPath,
		Epoch:                  pattern.Epoch,
		LastEventTime:          pattern.LastEventTime,
		NextEventTime:          pattern.NextEventTime,
		uniformTimes:           append([]time.Time(nil), pattern.uniformTimes...),
		uniformCursor:          pattern.uniformCursor,
		uniformLazy:            pattern.uniformLazy,
		uniformStreamWatermark: pattern.uniformStreamWatermark,
		Active:                 pattern.Active,
	}
	return copy, true
}

// GetAllPatterns returns deep copies of all workload patterns to prevent concurrent access issues.
// The returned copies are snapshots and should be treated as read-only.
func (ws *WorkloadState) GetAllPatterns() map[string]*WorkloadPatternState {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	result := make(map[string]*WorkloadPatternState)
	for k, v := range ws.patterns {
		// Create a deep copy of each pattern state.
		// Note: The mutex is not initialized in the copy as the returned state is intended
		// to be a read-only snapshot and callers should not perform locking operations on it.
		v.mu.RLock()
		copy := &WorkloadPatternState{
			Pattern:                v.Pattern,
			ServiceID:              v.ServiceID,
			EndpointPath:           v.EndpointPath,
			Epoch:                  v.Epoch,
			LastEventTime:          v.LastEventTime,
			NextEventTime:          v.NextEventTime,
			uniformTimes:           append([]time.Time(nil), v.uniformTimes...),
			uniformCursor:          v.uniformCursor,
			uniformLazy:            v.uniformLazy,
			uniformStreamWatermark: v.uniformStreamWatermark,
			Active:                 v.Active,
		}
		v.mu.RUnlock()
		result[k] = copy
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

	ticker := time.NewTicker(EventGenerationTickerInterval)
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

	// Generate events up to EventGenerationLookaheadWindow ahead
	lookaheadTime := currentSimTime.Add(EventGenerationLookaheadWindow)
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

		if patternState.Pattern.Arrival.Type == "uniform" && patternState.uniformLazy {
			ws.ensureUniformHorizon(patternState, lookaheadTime)
		}

		// Generate events until we've scheduled up to lookaheadTime
		for patternState.NextEventTime.Before(lookaheadTime) && patternState.NextEventTime.Before(ws.endTime) {
			// Schedule the arrival event
			ws.engine.ScheduleAt(
				engine.EventTypeRequestArrival,
				patternState.NextEventTime,
				nil,
				patternState.ServiceID,
				workloadArrivalEventData(patternState),
			)

			nextTime := ws.advanceToNextArrival(patternState, patternState.NextEventTime)
			patternState.LastEventTime = patternState.NextEventTime
			patternState.NextEventTime = nextTime
		}
		patternState.mu.Unlock()
	}
}

// generateUpToHorizon schedules arrivals up to the provided simulation-time horizon (exclusive).
// Returns the number of arrivals generated.
func (ws *WorkloadState) generateUpToHorizon(horizon time.Time) int {
	ws.mu.RLock()
	currentSimTime := ws.engine.GetSimTime()
	if horizon.After(ws.endTime) {
		horizon = ws.endTime
	}
	patterns := make([]*WorkloadPatternState, 0, len(ws.patterns))
	for _, pattern := range ws.patterns {
		patterns = append(patterns, pattern)
	}
	ws.mu.RUnlock()
	if !currentSimTime.Before(ws.endTime) {
		return 0
	}

	generated := 0
	for _, patternState := range patterns {
		select {
		case <-ws.ctx.Done():
			return generated
		default:
		}
		patternState.mu.Lock()
		if !patternState.Active {
			patternState.mu.Unlock()
			continue
		}
		if patternState.Pattern.Arrival.Type == "uniform" && patternState.uniformLazy {
			ws.ensureUniformHorizon(patternState, horizon)
		}
		for patternState.NextEventTime.Before(horizon) && patternState.NextEventTime.Before(ws.endTime) {
			select {
			case <-ws.ctx.Done():
				patternState.mu.Unlock()
				return generated
			default:
			}
			ws.engine.ScheduleAt(
				engine.EventTypeRequestArrival,
				patternState.NextEventTime,
				nil,
				patternState.ServiceID,
				workloadArrivalEventData(patternState),
			)
			generated++
			if ws.engine.GuardrailError() != nil {
				patternState.mu.Unlock()
				return generated
			}
			nextTime := ws.advanceToNextArrival(patternState, patternState.NextEventTime)
			patternState.LastEventTime = patternState.NextEventTime
			patternState.NextEventTime = nextTime
		}
		patternState.mu.Unlock()
	}
	return generated
}

func (ws *WorkloadState) sampleUniformArrivalTimes(startTime, endTime time.Time, rateRPS float64) []time.Time {
	duration := endTime.Sub(startTime)
	totalSeconds := duration.Seconds()
	if totalSeconds <= 0 {
		return nil
	}
	n := int64(math.Round(rateRPS * totalSeconds))
	return ws.uniformPlaceNInInterval(startTime, endTime, n)
}

// uniformPlaceNInInterval samples n independent uniform offsets in [start, end) and returns sorted times.
func (ws *WorkloadState) uniformPlaceNInInterval(startTime, endTime time.Time, n int64) []time.Time {
	if n <= 0 {
		return nil
	}
	totalSeconds := endTime.Sub(startTime).Seconds()
	if totalSeconds <= 0 {
		return nil
	}
	times := make([]time.Time, 0, n)
	for i := int64(0); i < n; i++ {
		offsetSeconds := ws.generator.UniformFloat64(0, totalSeconds)
		arrivalTime := startTime.Add(time.Duration(offsetSeconds * float64(time.Second)))
		if arrivalTime.After(endTime) {
			continue
		}
		times = append(times, arrivalTime)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times
}

// sampleLazyUniformChunk schedules arrivals for one lazy realtime chunk. Count is
// floor(rate*Δt_end) - floor(rate*Δt_start) with Δ measured from pattern Epoch (same
// anchor as Start/UpdateRate/UpdatePattern), so totals over many chunks match
// floor(rate * horizon_seconds) without floating carry drift or per-chunk rounding bias.
func (ws *WorkloadState) sampleLazyUniformChunk(chunkStart, chunkEnd time.Time, rateRPS float64, epoch time.Time) []time.Time {
	sec0 := chunkStart.Sub(epoch).Seconds()
	sec1 := chunkEnd.Sub(epoch).Seconds()
	if sec1 <= sec0 {
		return nil
	}
	if sec0 < 0 {
		sec0 = 0
	}
	n := int64(math.Floor(rateRPS*sec1)) - int64(math.Floor(rateRPS*sec0))
	if n < 0 {
		n = 0
	}
	return ws.uniformPlaceNInInterval(chunkStart, chunkEnd, n)
}

// ensureUniformHorizon extends uniformTimes for lazy real-time uniform patterns by sampling
// additional [uniformStreamWatermark, …) chunks until simulation time coverThrough is covered
// or endTime is reached.
func (ws *WorkloadState) ensureUniformHorizon(ps *WorkloadPatternState, coverThrough time.Time) {
	if !ps.uniformLazy {
		return
	}
	if coverThrough.After(ws.endTime) {
		coverThrough = ws.endTime
	}
	rate := ps.Pattern.Arrival.RateRPS
	for ps.uniformStreamWatermark.Before(coverThrough) && ps.uniformStreamWatermark.Before(ws.endTime) {
		chunkStart := ps.uniformStreamWatermark
		chunkEnd := chunkStart.Add(EventGenerationLookaheadWindow)
		if chunkEnd.After(ws.endTime) {
			chunkEnd = ws.endTime
		}
		if !chunkStart.Before(chunkEnd) {
			break
		}
		part := ws.sampleLazyUniformChunk(chunkStart, chunkEnd, rate, ps.Epoch)
		ps.uniformTimes = append(ps.uniformTimes, part...)
		if len(ps.uniformTimes) > ps.uniformCursor {
			tail := ps.uniformTimes[ps.uniformCursor:]
			sort.Slice(tail, func(i, j int) bool { return tail[i].Before(tail[j]) })
		}
		ps.uniformStreamWatermark = chunkEnd
	}
}

// advanceToNextArrival returns the next arrival instant after scheduling at scheduledAt.
// For type "uniform" it walks the precomputed sorted schedule (see sampleUniformArrivalTimes).
func (ws *WorkloadState) advanceToNextArrival(patternState *WorkloadPatternState, scheduledAt time.Time) time.Time {
	if patternState.Pattern.Arrival.Type != "uniform" {
		return ws.calculateNextArrivalTime(patternState.Pattern.Arrival, scheduledAt, patternState.Epoch)
	}
	if patternState.uniformLazy {
		ws.ensureUniformHorizon(patternState, scheduledAt.Add(EventGenerationLookaheadWindow))
	}
	if len(patternState.uniformTimes) == 0 {
		return ws.endTime
	}
	patternState.uniformCursor++
	if patternState.uniformCursor < len(patternState.uniformTimes) {
		return patternState.uniformTimes[patternState.uniformCursor]
	}
	if patternState.uniformLazy && patternState.uniformStreamWatermark.Before(ws.endTime) {
		ws.ensureUniformHorizon(patternState, patternState.uniformStreamWatermark.Add(EventGenerationLookaheadWindow))
		if patternState.uniformCursor < len(patternState.uniformTimes) {
			return patternState.uniformTimes[patternState.uniformCursor]
		}
	}
	return ws.endTime
}

// calculateNextArrivalTime calculates the next arrival time after the last scheduled arrival
// at currentTime. epoch is simulation start for bursty cycle alignment (ignored for other types).
func (ws *WorkloadState) calculateNextArrivalTime(arrival config.ArrivalSpec, currentTime time.Time, epoch time.Time) time.Time {
	switch arrival.Type {
	case "poisson", "exponential":
		// Exponential inter-arrival time
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = DefaultFallbackRateRPS
		}
		interArrivalSeconds := ws.generator.ExpFloat64(rateRPS)
		if interArrivalSeconds < MinInterArrivalTimeSeconds {
			interArrivalSeconds = MinInterArrivalTimeSeconds
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "constant":
		// Fixed interval = 1/rate (deterministic).
		rateRPS := arrival.RateRPS
		if rateRPS <= 0 {
			rateRPS = DefaultFallbackRateRPS
		}
		interArrivalSeconds := 1.0 / rateRPS
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "normal", "gaussian":
		// Normal distribution
		meanRateRPS := arrival.RateRPS
		if meanRateRPS <= 0 {
			meanRateRPS = DefaultFallbackRateRPS
		}
		stddevRPS := arrival.StdDevRPS
		if stddevRPS <= 0 {
			stddevRPS = meanRateRPS * DefaultStdDevPercentage
		}
		meanInterArrivalSeconds := 1.0 / meanRateRPS
		stddevInterArrivalSeconds := stddevRPS / (meanRateRPS * meanRateRPS)
		interArrivalSeconds := ws.generator.NormFloat64(meanInterArrivalSeconds, stddevInterArrivalSeconds)
		if interArrivalSeconds < MinInterArrivalTimeSeconds {
			interArrivalSeconds = MinInterArrivalTimeSeconds
		}
		return currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

	case "bursty":
		return burstyNextArrivalTime(epoch, currentTime, arrival, ws.generator)

	default:
		panic(fmt.Sprintf("workload: unsupported arrival type %q (expected validated scenario)", arrival.Type))
	}
}

// burstyNextArrivalTime returns the time of the next arrival after lastEventTime, aligned to epoch.
// It mirrors internal/workload.Generator.scheduleBurstyArrivals: Poisson inter-arrivals at burst rate
// during burst windows, no arrivals during quiet (advance to the next burst start).
func burstyNextArrivalTime(epoch, lastEventTime time.Time, arrival config.ArrivalSpec, gen *utils.RandSource) time.Time {
	baseRate := arrival.RateRPS
	if baseRate <= 0 {
		baseRate = 10.0
	}
	burstRate := baseRate * 5.0
	if arrival.BurstRateRPS > 0 {
		burstRate = arrival.BurstRateRPS
	}
	burstDuration := 5.0
	if arrival.BurstDurationSeconds > 0 {
		burstDuration = arrival.BurstDurationSeconds
	}
	quietDuration := 10.0
	if arrival.QuietDurationSeconds > 0 {
		quietDuration = arrival.QuietDurationSeconds
	}
	cycleDuration := burstDuration + quietDuration
	if cycleDuration <= 0 || burstDuration <= 0 {
		interArrivalSeconds := gen.ExpFloat64(baseRate)
		if interArrivalSeconds < MinInterArrivalTimeSeconds {
			interArrivalSeconds = MinInterArrivalTimeSeconds
		}
		return lastEventTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))
	}

	t := lastEventTime
	if t.Before(epoch) {
		t = epoch
	}

	const maxIter = 100000
	for i := 0; i < maxIter; i++ {
		timeSinceEpoch := t.Sub(epoch).Seconds()
		if timeSinceEpoch < 0 {
			timeSinceEpoch = 0
			t = epoch
		}
		cycleNumber := int(math.Floor(timeSinceEpoch / cycleDuration))
		timeInCycle := timeSinceEpoch - float64(cycleNumber)*cycleDuration
		inBurst := timeInCycle < burstDuration

		if !inBurst {
			nextBurstStart := epoch.Add(time.Duration(float64(cycleNumber+1) * cycleDuration * float64(time.Second)))
			if !nextBurstStart.After(t) {
				nextBurstStart = epoch.Add(time.Duration(float64(cycleNumber+2) * cycleDuration * float64(time.Second)))
			}
			t = nextBurstStart
			continue
		}

		interArrivalSeconds := gen.ExpFloat64(burstRate)
		if interArrivalSeconds < MinInterArrivalTimeSeconds {
			interArrivalSeconds = MinInterArrivalTimeSeconds
		}
		candidate := t.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		timeSinceEpoch = candidate.Sub(epoch).Seconds()
		cycleNumber = int(math.Floor(timeSinceEpoch / cycleDuration))
		timeInCycle = timeSinceEpoch - float64(cycleNumber)*cycleDuration

		if timeInCycle < burstDuration {
			return candidate
		}

		nextBurstStart := epoch.Add(time.Duration(float64(cycleNumber+1) * cycleDuration * float64(time.Second)))
		if !nextBurstStart.After(t) {
			nextBurstStart = epoch.Add(time.Duration(float64(cycleNumber+2) * cycleDuration * float64(time.Second)))
		}
		t = nextBurstStart
	}

	panic("bursty: exceeded iteration limit; check burst/quiet durations")
}

// newWorkloadStateWithPatternsStub returns a WorkloadState with patterns loaded from the scenario
// so GetRunConfiguration can observe workload entries. Used by online controller tests that call
// runOnlineController without a live engine.
func newWorkloadStateWithPatternsStub(runID string, scenario *config.Scenario, startTime time.Time) (*WorkloadState, error) {
	ws := NewWorkloadState(runID, nil, startTime.Add(24*time.Hour), 0)
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, workloadPattern := range scenario.Workload {
		serviceID, endpointPath, err := interaction.ParseDownstreamTarget(workloadPattern.To)
		if err != nil {
			return nil, err
		}
		key := patternKey(workloadPattern.From, workloadPattern.To)
		ws.patterns[key] = &WorkloadPatternState{
			Pattern:       workloadPattern,
			ServiceID:     serviceID,
			EndpointPath:  endpointPath,
			Epoch:         startTime,
			LastEventTime: startTime,
			NextEventTime: startTime,
			Active:        true,
		}
	}
	return ws, nil
}
