package simd

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

func TestNewWorkloadState(t *testing.T) {
	eng := engine.NewEngine("test-run")
	endTime := time.Now().Add(10 * time.Second)

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	if ws == nil {
		t.Fatal("NewWorkloadState returned nil")
	}

	if ws.runID != "test-run" {
		t.Errorf("Expected runID 'test-run', got '%s'", ws.runID)
	}
	if ws.engine != eng {
		t.Error("Engine not set correctly")
	}
	if !ws.endTime.Equal(endTime) {
		t.Error("EndTime not set correctly")
	}
}

func TestWorkloadStateStart(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// Check that patterns were initialized
	patterns := ws.GetAllPatterns()
	if len(patterns) != 1 {
		t.Fatalf("Expected 1 pattern, got %d", len(patterns))
	}

	patternKey := patternKey("client", "svc1:/test")
	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}

	if patternState.ServiceID != "svc1" {
		t.Errorf("Expected ServiceID 'svc1', got '%s'", patternState.ServiceID)
	}
	if patternState.EndpointPath != "/test" {
		t.Errorf("Expected EndpointPath '/test', got '%s'", patternState.EndpointPath)
	}
	if !patternState.Active {
		t.Error("Pattern should be active")
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateUpdateRate(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")

	// Update rate
	newRate := 50.0
	err = ws.UpdateRate(patternKey, newRate)
	if err != nil {
		t.Fatalf("UpdateRate() returned error: %v", err)
	}

	// Verify rate was updated
	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}

	if patternState.Pattern.Arrival.RateRPS != newRate {
		t.Errorf("Expected rate %f, got %f", newRate, patternState.Pattern.Arrival.RateRPS)
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateUpdateRateNotFound(t *testing.T) {
	eng := engine.NewEngine("test-run")
	endTime := time.Now().Add(5 * time.Second)

	ws := NewWorkloadState("test-run", eng, endTime, 0)

	// Try to update non-existent pattern
	err := ws.UpdateRate("nonexistent:pattern", 50.0)
	if err == nil {
		t.Error("Expected error for non-existent pattern")
	}

	ws.Stop()
}

func TestWorkloadStateUpdateRateInvalidValues(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")

	// Test with negative rate - executor rejects rates <= 0, so this should return an error
	err = ws.UpdateRate(patternKey, -10.0)
	if err == nil {
		t.Errorf("UpdateRate() with negative value should return an error")
	}

	// Test with zero rate - executor rejects rates <= 0, so this should also return an error
	err = ws.UpdateRate(patternKey, 0.0)
	if err == nil {
		t.Errorf("UpdateRate() with zero value should return an error")
	}

	// Cleanup
	ws.Stop()
}

func TestWorkloadStateStop(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// Stop should not panic
	ws.Stop()

	// Calling Stop multiple times should be safe
	ws.Stop()
}

func TestWorkloadStateUpdatePattern(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10.0,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	patternKey := patternKey("client", "svc1:/test")
	newPattern := config.WorkloadPattern{
		From: "client",
		To:   "svc1:/test",
		Arrival: config.ArrivalSpec{
			Type:    "poisson",
			RateRPS: 25.0,
		},
	}

	err = ws.UpdatePattern(patternKey, newPattern)
	if err != nil {
		t.Fatalf("UpdatePattern() returned error: %v", err)
	}

	patternState, ok := ws.GetPattern(patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}
	if patternState.Pattern.Arrival.RateRPS != 25.0 {
		t.Errorf("Expected rate 25.0, got %f", patternState.Pattern.Arrival.RateRPS)
	}

	ws.Stop()
}

func TestWorkloadStateStartInvalidTarget(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 10}}, // empty path fails
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, true)
	if err == nil {
		t.Fatal("Expected error for invalid workload target")
	}
	ws.Stop()
}

// TestWorkloadStateStandardModeStartsBoundedWindow verifies non-real-time start only
// seeds a bounded lookahead chunk (instead of full-run pre-generation).
func TestWorkloadStateStandardModeStartsBoundedWindow(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	duration := 30 * time.Second
	endTime := startTime.Add(duration)

	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 10.0}},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	err := ws.Start(scenario, startTime, false)
	if err != nil {
		t.Fatalf("Start(..., false) returned error: %v", err)
	}

	// The queue should contain only arrivals for one lookahead horizon + generation tick + sim end.
	maxExpectedArrivals := int(EventGenerationLookaheadWindow.Seconds() * 10.0)
	if eng.GetEventQueue().Size() > maxExpectedArrivals+3 {
		t.Fatalf("expected bounded startup queue, got size=%d", eng.GetEventQueue().Size())
	}

	// Non-real-time mode tracks generated horizon incrementally.
	patternState, ok := ws.GetPattern("client:svc1:/test")
	if !ok {
		t.Fatal("Pattern not found")
	}
	expectedInitialHorizon := startTime.Add(EventGenerationLookaheadWindow)
	if expectedInitialHorizon.After(endTime) {
		expectedInitialHorizon = endTime
	}
	if patternState.NextEventTime.Before(expectedInitialHorizon) {
		t.Errorf("expected next event to advance at least to initial horizon %v, got %v",
			expectedInitialHorizon, patternState.NextEventTime)
	}
}

func TestWorkloadStateStandardModeLongHighRPSBoundedAtStartup(t *testing.T) {
	eng := engine.NewEngine("bounded-high-rps")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(2 * time.Hour)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 16}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 5000}},
		},
	}
	ws := NewWorkloadState("bounded-high-rps", eng, endTime, 123)
	if err := ws.Start(scenario, startTime, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// 5000 rps * 10s lookahead = ~50k arrivals (+non-arrival events).
	if got := eng.GetEventQueue().Size(); got > 55_000 {
		t.Fatalf("expected bounded startup queue under ~55k events, got %d", got)
	}
}

func TestWorkloadStateStandardModeTotalArrivalsMatchLegacyGeneration(t *testing.T) {
	const seed int64 = 98765
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 10}},
		},
	}
	legacyEngine := engine.NewEngine("legacy")
	legacyStart := legacyEngine.GetSimTime()
	legacyEnd := legacyStart.Add(12 * time.Second)
	legacyWS := NewWorkloadState("legacy", legacyEngine, legacyEnd, seed)
	legacyWS.mu.Lock()
	if err := legacyWS.initPatternsLocked(scenario, legacyStart, false); err != nil {
		legacyWS.mu.Unlock()
		t.Fatalf("initPatternsLocked: %v", err)
	}
	legacyWS.mu.Unlock()
	legacyWS.generateAllEventsUpToEndTime()
	legacyArrivals := 0
	for _, e := range drainEvents(legacyEngine) {
		if e.Type == engine.EventTypeRequestArrival {
			legacyArrivals++
		}
	}

	incrementalEngine := engine.NewEngine("incremental")
	start := incrementalEngine.GetSimTime()
	end := start.Add(12 * time.Second)
	ws := NewWorkloadState("incremental", incrementalEngine, end, seed)
	if err := ws.Start(scenario, start, false); err != nil {
		t.Fatalf("incremental Start: %v", err)
	}
	var arrivals int64
	incrementalEngine.RegisterHandler(engine.EventTypeRequestArrival, func(_ *engine.Engine, _ *engine.Event) error {
		atomic.AddInt64(&arrivals, 1)
		return nil
	})
	if err := incrementalEngine.Run(12 * time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := int(atomic.LoadInt64(&arrivals)); got != legacyArrivals {
		t.Fatalf("arrival count mismatch; incremental=%d legacy=%d", got, legacyArrivals)
	}
}

func TestWorkloadStateStandardModeChunkBoundaryNoDuplicatesOrMisses(t *testing.T) {
	eng := engine.NewEngine("chunk-boundary")
	start := eng.GetSimTime()
	end := start.Add(25 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 1}},
		},
	}
	ws := NewWorkloadState("chunk-boundary", eng, end, 42)
	if err := ws.Start(scenario, start, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var times []time.Time
	eng.RegisterHandler(engine.EventTypeRequestArrival, func(e *engine.Engine, _ *engine.Event) error {
		times = append(times, e.GetSimTime())
		return nil
	})
	if err := eng.Run(25 * time.Second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(times) != 24 {
		t.Fatalf("expected 24 arrivals for 25s @1rps (end exclusive), got %d", len(times))
	}
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Fatalf("expected strictly increasing arrivals, got %v then %v", times[i-1], times[i])
		}
	}
}

func TestWorkloadStateStandardModeStopsGenerationAfterCancellation(t *testing.T) {
	eng := engine.NewEngine("cancel-generate")
	start := eng.GetSimTime()
	end := start.Add(60 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 100}},
		},
	}
	ws := NewWorkloadState("cancel-generate", eng, end, 42)
	if err := ws.Start(scenario, start, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ws.Stop()
	before := eng.GetEventQueue().Size()
	generated := ws.generateUpToHorizon(start.Add(30 * time.Second))
	after := eng.GetEventQueue().Size()
	if generated != 0 || after != before {
		t.Fatalf("expected no generation after stop; generated=%d size %d->%d", generated, before, after)
	}
}

func TestWorkloadStateStandardModeStopsWhenLimitTrips(t *testing.T) {
	eng := engine.NewEngine("limit-generate")
	eng.SetRuntimeLimits(engine.RuntimeLimits{MaxEventsScheduled: 15})
	start := eng.GetSimTime()
	end := start.Add(60 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 100}},
		},
	}
	ws := NewWorkloadState("limit-generate", eng, end, 42)
	_ = ws.Start(scenario, start, false)
	if eng.GuardrailError() == nil {
		_ = ws.generateUpToHorizon(start.Add(20 * time.Second))
	}
	if eng.GuardrailError() == nil {
		t.Fatalf("expected guardrail error after generation pressure")
	}
}

// TestWorkloadStateStartRealTimeSeedsInitialWindow verifies that Start(..., true)
// schedules an initial lookahead window synchronously before returning.
func TestWorkloadStateStartRealTimeSeedsInitialWindow(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	duration := 30 * time.Second
	endTime := startTime.Add(duration)

	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 10.0}},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	if err := ws.Start(scenario, startTime, true); err != nil {
		t.Fatalf("Start(..., true) returned error: %v", err)
	}
	defer ws.Stop()

	patternState, ok := ws.GetPattern("client:svc1:/test")
	if !ok {
		t.Fatal("Pattern not found")
	}

	expectedMinNext := startTime.Add(EventGenerationLookaheadWindow)
	if expectedMinNext.After(endTime) {
		expectedMinNext = endTime
	}
	if patternState.NextEventTime.Before(expectedMinNext) {
		t.Fatalf("real-time Start should seed initial lookahead before return; NextEventTime=%v expected at least %v",
			patternState.NextEventTime, expectedMinNext)
	}
}

func TestPatternKey(t *testing.T) {
	key := patternKey("client", "svc1:/test")
	expected := "client:svc1:/test"
	if key != expected {
		t.Errorf("Expected pattern key '%s', got '%s'", expected, key)
	}
}

func TestWorkloadStateArrivalEventCarriesMetadataMap(t *testing.T) {
	eng := engine.NewEngine("workload-metadata")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(2 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Metadata: map[string]string{
					"client_zone": "zone-a",
					"tenant":      "gold",
				},
				Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 1},
			},
		},
	}
	ws := NewWorkloadState("workload-metadata", eng, endTime, 0)
	if err := ws.Start(scenario, startTime, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	q := eng.GetEventQueue()
	if q.Size() == 0 {
		t.Fatal("expected request arrival events")
	}
	evt := q.Next()
	if evt.Type != engine.EventTypeRequestArrival {
		t.Fatalf("expected request arrival event, got %v", evt.Type)
	}
	md, ok := evt.Data["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata map on event, got %T", evt.Data["metadata"])
	}
	if md["client_zone"] != "zone-a" || md["tenant"] != "gold" {
		t.Fatalf("unexpected metadata map: %+v", md)
	}
}

// TestWorkloadStateUniformNotFixedInterval checks that "uniform" uses random offsets in [start,end)
// (sorted), not the fixed spacing of "constant".
func TestWorkloadStateUniformNotFixedInterval(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(5 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 50}},
		},
	}
	ws := NewWorkloadState("test-run", eng, endTime, 0)
	ws.generator = utils.NewRandSource(90001)
	if err := ws.Start(scenario, startTime, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	times := drainArrivalTimes(eng)
	if len(times) < 10 {
		t.Fatalf("expected many uniform arrivals, got %d", len(times))
	}
	gaps := make([]int64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]).Nanoseconds())
	}
	first := gaps[0]
	for _, g := range gaps[1:] {
		if g != first {
			return
		}
	}
	t.Fatal("uniform workload should not produce strictly periodic inter-arrivals (same as constant)")
}

// TestWorkloadStateUniformRealTimeDoesNotPreallocateFullHorizon ensures long endTime + uniform
// does not materialize rate × duration samples at Start when real-time mode uses lazy chunks.
func TestWorkloadStateUniformRealTimeDoesNotPreallocateFullHorizon(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(8760 * time.Hour) // 1 year
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 100}},
		},
	}
	ws := NewWorkloadState("test-run", eng, endTime, 0)
	ws.generator = utils.NewRandSource(42)
	if err := ws.Start(scenario, startTime, true); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ws.Stop()
	ps, ok := ws.GetPattern("client:svc1:/test")
	if !ok {
		t.Fatal("pattern missing")
	}
	// Lazy path: only first lookahead window(s) sampled at startup
	if len(ps.uniformTimes) > 500_000 {
		t.Fatalf("unexpected uniform preallocation: %d points", len(ps.uniformTimes))
	}
}

func TestWorkloadStateUniformStandardModeDoesNotPreallocateFullHorizon(t *testing.T) {
	eng := engine.NewEngine("test-run-std-uniform")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(8760 * time.Hour) // 1 year
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 100}},
		},
	}
	ws := NewWorkloadState("test-run-std-uniform", eng, endTime, 0)
	ws.generator = utils.NewRandSource(42)
	if err := ws.Start(scenario, startTime, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ps, ok := ws.GetPattern("client:svc1:/test")
	if !ok {
		t.Fatal("pattern missing")
	}
	if !ps.uniformLazy {
		t.Fatal("expected standard-mode uniform to use lazy horizon generation")
	}
	if len(ps.uniformTimes) > 500_000 {
		t.Fatalf("unexpected uniform preallocation in standard mode: %d points", len(ps.uniformTimes))
	}
}

// TestLazyUniformLongRunRate001 verifies realtime lazy uniform does not round each 10s chunk
// independently (which would yield zero arrivals at 0.01 RPS).
func TestLazyUniformLongRunRate001(t *testing.T) {
	eng := engine.NewEngine("lazy-001")
	start := eng.GetSimTime()
	end := start.Add(1000 * time.Second)
	ws := NewWorkloadState("lazy-001", eng, end, 42)
	ws.generator = utils.NewRandSource(1001)
	ps := &WorkloadPatternState{
		Pattern: config.WorkloadPattern{
			From: "client", To: "svc1:/test",
			Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.01},
		},
		uniformLazy:            true,
		uniformStreamWatermark: start,
		Epoch:                  start,
	}
	ws.ensureUniformHorizon(ps, start.Add(1000*time.Second))
	if n := len(ps.uniformTimes); n != 10 {
		t.Fatalf("expected 10 arrivals over 1000s at 0.01 RPS, got %d", n)
	}
}

// TestLazyUniformLongRunRate005 verifies 0.05 RPS over 1000s yields ~50 arrivals, not 100
// (independent per-chunk rounding would give 1 per 10s chunk).
func TestLazyUniformLongRunRate005(t *testing.T) {
	eng := engine.NewEngine("lazy-005")
	start := eng.GetSimTime()
	end := start.Add(1000 * time.Second)
	ws := NewWorkloadState("lazy-005", eng, end, 42)
	ws.generator = utils.NewRandSource(2002)
	ps := &WorkloadPatternState{
		Pattern: config.WorkloadPattern{
			From: "client", To: "svc1:/test",
			Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.05},
		},
		uniformLazy:            true,
		uniformStreamWatermark: start,
		Epoch:                  start,
	}
	ws.ensureUniformHorizon(ps, start.Add(1000*time.Second))
	if n := len(ps.uniformTimes); n != 50 {
		t.Fatalf("expected 50 arrivals over 1000s at 0.05 RPS, got %d", n)
	}
}

func TestNonRealtimeUniformBoundedCountRound(t *testing.T) {
	eng := engine.NewEngine("nr-uniform")
	start := eng.GetSimTime()
	end := start.Add(5 * time.Second)
	ws := NewWorkloadState("nr-uniform", eng, end, 42)
	ws.generator = utils.NewRandSource(90001)
	times := ws.sampleUniformArrivalTimes(start, end, 50)
	if len(times) != 250 {
		t.Fatalf("non-realtime uniform uses N=round(rate*duration): expected 250, got %d", len(times))
	}
}

func TestLazyUniformDeterministicSeed(t *testing.T) {
	fixedStart := time.Unix(1700000000, 0).UTC()
	build := func() []time.Time {
		eng := engine.NewEngineWithSimStart("det", fixedStart)
		start := eng.GetSimTime()
		end := start.Add(500 * time.Second)
		ws := NewWorkloadState("det", eng, end, 42)
		ws.generator = utils.NewRandSource(424242)
		ps := &WorkloadPatternState{
			Pattern: config.WorkloadPattern{
				From: "client", To: "svc1:/test",
				Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.02},
			},
			uniformLazy:            true,
			uniformStreamWatermark: start,
			Epoch:                  start,
		}
		ws.ensureUniformHorizon(ps, start.Add(500*time.Second))
		return append([]time.Time(nil), ps.uniformTimes...)
	}
	a := build()
	b := build()
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			t.Fatalf("determinism broken at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestWorkloadStateStandardModeUniformDeterministicSeed(t *testing.T) {
	fixedStart := time.Unix(1700000000, 0).UTC()
	build := func() []time.Time {
		eng := engine.NewEngineWithSimStart("std-uniform-det", fixedStart)
		start := eng.GetSimTime()
		end := start.Add(120 * time.Second)
		ws := NewWorkloadState("std-uniform-det", eng, end, 42)
		ws.generator = utils.NewRandSource(424242)
		scenario := &config.Scenario{
			Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
			Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
			Workload: []config.WorkloadPattern{
				{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 7}},
			},
		}
		if err := ws.Start(scenario, start, false); err != nil {
			t.Fatalf("Start: %v", err)
		}
		var times []time.Time
		eng.RegisterHandler(engine.EventTypeRequestArrival, func(e *engine.Engine, _ *engine.Event) error {
			times = append(times, e.GetSimTime())
			return nil
		})
		if err := eng.Run(120 * time.Second); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return times
	}
	a := build()
	b := build()
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			t.Fatalf("determinism broken at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestWorkloadStateStandardModeUniformTotalCountMatchesRoundContract(t *testing.T) {
	eng := engine.NewEngine("std-uniform-round")
	start := eng.GetSimTime()
	end := start.Add(123 * time.Second)
	const rate = 2.5
	ws := NewWorkloadState("std-uniform-round", eng, end, 42)
	ws.generator = utils.NewRandSource(76543)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: rate}},
		},
	}
	if err := ws.Start(scenario, start, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var times []time.Time
	eng.RegisterHandler(engine.EventTypeRequestArrival, func(e *engine.Engine, _ *engine.Event) error {
		times = append(times, e.GetSimTime())
		return nil
	})
	if err := eng.Run(end.Sub(start)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := int(math.Round(rate * end.Sub(start).Seconds()))
	if len(times) != want {
		t.Fatalf("expected %d arrivals (round contract), got %d", want, len(times))
	}
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Fatalf("expected strictly increasing arrivals, got %v then %v", times[i-1], times[i])
		}
	}
}

func TestLazyUniformCompactionKeepsScheduleBounded(t *testing.T) {
	eng := engine.NewEngine("uniform-compact-bound")
	start := eng.GetSimTime()
	end := start.Add(600 * time.Second)
	ws := NewWorkloadState("uniform-compact-bound", eng, end, 42)
	ws.generator = utils.NewRandSource(123456)
	ps := &WorkloadPatternState{
		Pattern: config.WorkloadPattern{
			From: "client", To: "svc1:/test",
			Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 20},
		},
		uniformLazy:            true,
		uniformStreamWatermark: start,
		Epoch:                  start,
	}
	ws.ensureUniformHorizon(ps, start.Add(EventGenerationLookaheadWindow))
	if len(ps.uniformTimes) == 0 {
		t.Fatal("expected initial uniform schedule")
	}
	ps.NextEventTime = ps.uniformTimes[0]

	maxLen := len(ps.uniformTimes)
	for i := 0; i < 6000; i++ {
		if ps.NextEventTime.IsZero() || !ps.NextEventTime.Before(end) {
			break
		}
		next := ws.advanceToNextArrival(ps, ps.NextEventTime)
		ps.LastEventTime = ps.NextEventTime
		ps.NextEventTime = next
		if len(ps.uniformTimes) > maxLen {
			maxLen = len(ps.uniformTimes)
		}
	}
	// Expect bounded memory: compaction threshold + one lookahead window (~200 points @20rps).
	if maxLen > uniformCompactionThreshold+1000 {
		t.Fatalf("expected bounded uniform schedule, max len=%d", maxLen)
	}
}

func TestLazyUniformCompactionNoMissesOrDuplicates(t *testing.T) {
	eng := engine.NewEngine("uniform-compact-integrity")
	start := eng.GetSimTime()
	end := start.Add(400 * time.Second)
	ws := NewWorkloadState("uniform-compact-integrity", eng, end, 42)
	ws.generator = utils.NewRandSource(222333)
	ps := &WorkloadPatternState{
		Pattern: config.WorkloadPattern{
			From: "client", To: "svc1:/test",
			Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 10},
		},
		uniformLazy:            true,
		uniformStreamWatermark: start,
		Epoch:                  start,
	}
	ws.ensureUniformHorizon(ps, start.Add(EventGenerationLookaheadWindow))
	if len(ps.uniformTimes) == 0 {
		t.Fatal("expected initial schedule")
	}
	ps.NextEventTime = ps.uniformTimes[0]

	var times []time.Time
	for ps.NextEventTime.Before(end) {
		times = append(times, ps.NextEventTime)
		next := ws.advanceToNextArrival(ps, ps.NextEventTime)
		ps.LastEventTime = ps.NextEventTime
		ps.NextEventTime = next
	}
	expected := int(math.Floor(10 * end.Sub(start).Seconds()))
	if len(times) != expected {
		t.Fatalf("expected %d arrivals, got %d", expected, len(times))
	}
	for i := 1; i < len(times); i++ {
		if !times[i].After(times[i-1]) {
			t.Fatalf("expected strictly increasing arrivals, got %v then %v", times[i-1], times[i])
		}
	}
}

func TestLazyUniformDeterministicSeedAfterCompaction(t *testing.T) {
	fixedStart := time.Unix(1700000000, 0).UTC()
	build := func() []time.Time {
		eng := engine.NewEngineWithSimStart("lazy-compact-det", fixedStart)
		start := eng.GetSimTime()
		end := start.Add(500 * time.Second)
		ws := NewWorkloadState("lazy-compact-det", eng, end, 42)
		ws.generator = utils.NewRandSource(999111)
		ps := &WorkloadPatternState{
			Pattern: config.WorkloadPattern{
				From: "client", To: "svc1:/test",
				Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 8},
			},
			uniformLazy:            true,
			uniformStreamWatermark: start,
			Epoch:                  start,
		}
		ws.ensureUniformHorizon(ps, start.Add(EventGenerationLookaheadWindow))
		if len(ps.uniformTimes) == 0 {
			t.Fatal("expected initial schedule")
		}
		ps.NextEventTime = ps.uniformTimes[0]
		var out []time.Time
		for ps.NextEventTime.Before(end) {
			out = append(out, ps.NextEventTime)
			next := ws.advanceToNextArrival(ps, ps.NextEventTime)
			ps.LastEventTime = ps.NextEventTime
			ps.NextEventTime = next
		}
		return out
	}
	a := build()
	b := build()
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			t.Fatalf("determinism broken at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestLazyUniformRealtimeNotPeriodic checks sorted arrivals across lazy chunks are not
// evenly spaced like a constant-rate ticker.
func TestLazyUniformRealtimeNotPeriodic(t *testing.T) {
	eng := engine.NewEngine("lazy-np")
	start := eng.GetSimTime()
	end := start.Add(120 * time.Second)
	ws := NewWorkloadState("lazy-np", eng, end, 42)
	ws.generator = utils.NewRandSource(777888)
	ps := &WorkloadPatternState{
		Pattern: config.WorkloadPattern{
			From: "client", To: "svc1:/test",
			Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.3},
		},
		uniformLazy:            true,
		uniformStreamWatermark: start,
		Epoch:                  start,
	}
	ws.ensureUniformHorizon(ps, start.Add(120*time.Second))
	times := ps.uniformTimes
	if len(times) < 8 {
		t.Fatalf("expected several arrivals, got %d", len(times))
	}
	gaps := make([]int64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]).Nanoseconds())
	}
	first := gaps[0]
	same := true
	for _, g := range gaps[1:] {
		if g != first {
			same = false
			break
		}
	}
	if same {
		t.Fatal("expected non-uniform inter-arrival gaps (not fixed interval)")
	}
}

func TestUniformEpochAndWatermarkResetOnUpdateRate(t *testing.T) {
	eng := engine.NewEngine("ur-reset")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(100 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.1}},
		},
	}
	ws := NewWorkloadState("ur-reset", eng, endTime, 42)
	if err := ws.Start(scenario, startTime, true); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ws.Stop()
	pk := patternKey("client", "svc1:/test")
	ws.mu.RLock()
	ps := ws.patterns[pk]
	ws.mu.RUnlock()
	ps.mu.Lock()
	ps.uniformStreamWatermark = startTime.Add(50 * time.Second)
	ps.mu.Unlock()
	if err := ws.UpdateRate(pk, 0.2); err != nil {
		t.Fatalf("UpdateRate: %v", err)
	}
	snap, ok := ws.GetPattern(pk)
	if !ok {
		t.Fatal("pattern missing")
	}
	now := eng.GetSimTime()
	if snap.uniformStreamWatermark.Equal(startTime.Add(50 * time.Second)) {
		t.Fatalf("stale uniformStreamWatermark should not persist after UpdateRate")
	}
	if snap.uniformStreamWatermark.Before(snap.Epoch) {
		t.Fatalf("uniformStreamWatermark should not trail Epoch after re-anchor")
	}
	if !snap.Epoch.Equal(now) {
		t.Fatalf("Epoch should reset to current sim time, got %v want %v", snap.Epoch, now)
	}
}

func TestUniformEpochAndWatermarkResetOnUpdatePattern(t *testing.T) {
	eng := engine.NewEngine("up-reset")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(100 * time.Second)
	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.1}},
		},
	}
	ws := NewWorkloadState("up-reset", eng, endTime, 42)
	if err := ws.Start(scenario, startTime, true); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ws.Stop()
	pk := patternKey("client", "svc1:/test")
	ws.mu.RLock()
	ps := ws.patterns[pk]
	ws.mu.RUnlock()
	ps.mu.Lock()
	ps.uniformStreamWatermark = startTime.Add(50 * time.Second)
	ps.mu.Unlock()
	newPat := config.WorkloadPattern{
		From: "client", To: "svc1:/test",
		Arrival: config.ArrivalSpec{Type: "uniform", RateRPS: 0.15},
	}
	if err := ws.UpdatePattern(pk, newPat); err != nil {
		t.Fatalf("UpdatePattern: %v", err)
	}
	snap, ok := ws.GetPattern(pk)
	if !ok {
		t.Fatal("pattern missing")
	}
	now := eng.GetSimTime()
	if snap.uniformStreamWatermark.Equal(startTime.Add(50 * time.Second)) {
		t.Fatalf("stale uniformStreamWatermark should not persist after UpdatePattern")
	}
	if snap.uniformStreamWatermark.Before(snap.Epoch) {
		t.Fatalf("uniformStreamWatermark should not trail Epoch after re-anchor")
	}
	if !snap.Epoch.Equal(now) {
		t.Fatalf("Epoch should reset to current sim time, got %v want %v", snap.Epoch, now)
	}
}

func burstyTimeInBurst(t, epoch time.Time, burstDur, quietDur float64) bool {
	cd := burstDur + quietDur
	if cd <= 0 {
		return true
	}
	s := t.Sub(epoch).Seconds()
	tic := s - math.Floor(s/cd)*cd
	return tic < burstDur
}

func drainArrivalTimes(eng *engine.Engine) []time.Time {
	q := eng.GetEventQueue()
	var times []time.Time
	for q.Size() > 0 {
		e := q.Next()
		if e.Type == engine.EventTypeRequestArrival {
			times = append(times, e.Time)
		}
	}
	return times
}

func drainEvents(eng *engine.Engine) []*engine.Event {
	q := eng.GetEventQueue()
	out := make([]*engine.Event, 0, q.Size())
	for q.Size() > 0 {
		out = append(out, q.Next())
	}
	return out
}

// TestWorkloadStateBurstyNonRealTimeWindows asserts arrivals only land in burst windows
// for a short non-real-time run (burst 2s, quiet 2s over 9s).
func TestWorkloadStateBurstyNonRealTimeWindows(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(9 * time.Second)
	const burstDur = 2.0
	const quietDur = 2.0

	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:                 "bursty",
					RateRPS:              1,
					BurstRateRPS:         200,
					BurstDurationSeconds: burstDur,
					QuietDurationSeconds: quietDur,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	ws.generator = utils.NewRandSource(424242)
	if err := ws.Start(scenario, startTime, false); err != nil {
		t.Fatalf("Start: %v", err)
	}

	times := drainArrivalTimes(eng)
	if len(times) == 0 {
		t.Fatal("expected bursty arrivals")
	}
	for _, at := range times {
		if !burstyTimeInBurst(at, startTime, burstDur, quietDur) {
			t.Errorf("arrival at %v not in burst window (epoch=%v)", at, startTime)
		}
	}
}

// TestWorkloadStateBurstyRealTimeLookahead checks seeded real-time events respect burst/quiet windows.
func TestWorkloadStateBurstyRealTimeLookahead(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	endTime := startTime.Add(60 * time.Second)
	const burstDur = 2.0
	const quietDur = 2.0

	scenario := &config.Scenario{
		Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:                 "bursty",
					RateRPS:              1,
					BurstRateRPS:         200,
					BurstDurationSeconds: burstDur,
					QuietDurationSeconds: quietDur,
				},
			},
		},
	}

	ws := NewWorkloadState("test-run", eng, endTime, 0)
	ws.generator = utils.NewRandSource(777001)
	if err := ws.Start(scenario, startTime, true); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ws.Stop()

	times := drainArrivalTimes(eng)
	if len(times) == 0 {
		t.Fatal("expected lookahead bursty arrivals")
	}
	for _, at := range times {
		if !burstyTimeInBurst(at, startTime, burstDur, quietDur) {
			t.Errorf("arrival at %v not in burst window (epoch=%v)", at, startTime)
		}
	}
}

// TestWorkloadStateBurstyBurstQuietParamsChangeVolume verifies burst/quiet durations affect
// how many arrivals fit in a fixed window (same seed).
func TestWorkloadStateBurstyBurstQuietParamsChangeVolume(t *testing.T) {
	const seed int64 = 900001
	duration := 20 * time.Second

	run := func(quiet float64) int {
		eng := engine.NewEngine("test-run")
		startTime := eng.GetSimTime()
		endTime := startTime.Add(duration)
		scenario := &config.Scenario{
			Hosts:    []config.Host{{ID: "host-1", Cores: 2}},
			Services: []config.Service{{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}}},
			Workload: []config.WorkloadPattern{
				{
					From: "client",
					To:   "svc1:/test",
					Arrival: config.ArrivalSpec{
						Type:                 "bursty",
						RateRPS:              1,
						BurstRateRPS:         50,
						BurstDurationSeconds: 2,
						QuietDurationSeconds: quiet,
					},
				},
			},
		}
		ws := NewWorkloadState("test-run", eng, endTime, 0)
		ws.generator = utils.NewRandSource(seed)
		if err := ws.Start(scenario, startTime, false); err != nil {
			t.Fatalf("Start: %v", err)
		}
		return len(drainArrivalTimes(eng))
	}

	nLongQuiet := run(18)
	nShortQuiet := run(1)
	if nLongQuiet == nShortQuiet {
		t.Fatalf("expected different arrival counts when quiet duration changes; got %d for both", nLongQuiet)
	}
}
