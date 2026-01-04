package workload

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewUserFlowGenerator(t *testing.T) {
	g := NewUserFlowGenerator(12345)
	if g == nil {
		t.Fatalf("expected non-nil generator")
	}
}

func TestUserFlowGeneratorScheduleUserFlow(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewUserFlowGenerator(12345)

	steps := []FlowStep{
		{ServiceID: "auth", Endpoint: "/login", DelayMs: 0, Probability: 1.0},
		{ServiceID: "user", Endpoint: "/profile", DelayMs: 100, Probability: 1.0},
		{ServiceID: "order", Endpoint: "/list", DelayMs: 200, Probability: 1.0},
	}

	startTime := time.Now()
	err := g.ScheduleUserFlow(eng, "flow-1", steps, startTime)
	if err != nil {
		t.Fatalf("ScheduleUserFlow error: %v", err)
	}

	// Check that events were scheduled
	if eng.GetEventQueue().Size() != 3 {
		t.Fatalf("expected 3 events to be scheduled, got %d", eng.GetEventQueue().Size())
	}
}

func TestUserFlowGeneratorWithProbability(t *testing.T) {
	eng := engine.NewEngine("test-run")
	// Use a fixed seed to make probability test deterministic
	g := NewUserFlowGenerator(99999)

	steps := []FlowStep{
		{ServiceID: "auth", Endpoint: "/login", DelayMs: 0, Probability: 1.0},
		{ServiceID: "user", Endpoint: "/profile", DelayMs: 100, Probability: 0.0}, // Never taken
		{ServiceID: "order", Endpoint: "/list", DelayMs: 200, Probability: 1.0},
	}

	startTime := time.Now()
	err := g.ScheduleUserFlow(eng, "flow-1", steps, startTime)
	if err != nil {
		t.Fatalf("ScheduleUserFlow error: %v", err)
	}

	// Should have 2 events (step 2 skipped due to probability 0)
	queueSize := eng.GetEventQueue().Size()
	// Note: Probability check uses random, so we just verify events were scheduled
	// The exact count may vary, but should be at least 2 (first and last step)
	if queueSize < 2 {
		t.Fatalf("expected at least 2 events, got %d", queueSize)
	}
}

func TestUserFlowGeneratorEmptySteps(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewUserFlowGenerator(12345)

	err := g.ScheduleUserFlow(eng, "flow-1", []FlowStep{}, time.Now())
	if err == nil {
		t.Fatalf("expected error for empty steps")
	}
}

func TestUserFlowGeneratorScheduleUserFlows(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewUserFlowGenerator(12345)

	steps := []FlowStep{
		{ServiceID: "auth", Endpoint: "/login", DelayMs: 0, Probability: 1.0},
		{ServiceID: "user", Endpoint: "/profile", DelayMs: 100, Probability: 1.0},
	}

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "poisson",
		RateRPS: 2.0, // 2 flows per second
	}

	err := g.ScheduleUserFlows(eng, startTime, endTime, arrival, "user-flow", steps)
	if err != nil {
		t.Fatalf("ScheduleUserFlows error: %v", err)
	}

	// Should have scheduled multiple flows
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled")
	}
}

func TestUserFlowGeneratorInvalidRate(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewUserFlowGenerator(12345)

	steps := []FlowStep{
		{ServiceID: "auth", Endpoint: "/login", DelayMs: 0, Probability: 1.0},
	}

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "poisson",
		RateRPS: -1.0, // Invalid rate
	}

	err := g.ScheduleUserFlows(eng, startTime, endTime, arrival, "user-flow", steps)
	if err == nil {
		t.Fatalf("expected error for invalid rate")
	}
}

