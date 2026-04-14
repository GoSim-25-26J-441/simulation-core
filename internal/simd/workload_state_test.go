package simd

import (
	"math"
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

// TestWorkloadStatePreGenerateAllEvents verifies that when realTime is false,
// Start pre-generates all arrival events up to endTime (so simulation-time runs
// get full request count instead of only one lookahead batch).
func TestWorkloadStatePreGenerateAllEvents(t *testing.T) {
	eng := engine.NewEngine("test-run")
	startTime := eng.GetSimTime()
	duration := 5 * time.Second
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

	// Pre-generation should advance each pattern's NextEventTime to at least endTime
	patternState, ok := ws.GetPattern("client:svc1:/test")
	if !ok {
		t.Fatal("Pattern not found")
	}
	if patternState.NextEventTime.Before(endTime) {
		t.Errorf("Pre-generate should schedule events up to endTime; NextEventTime=%v is before endTime=%v",
			patternState.NextEventTime, endTime)
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
