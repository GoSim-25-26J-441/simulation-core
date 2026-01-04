package workload

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewGenerator(t *testing.T) {
	g := NewGenerator(12345)
	if g == nil {
		t.Fatalf("expected non-nil generator")
	}
}

func TestGeneratorPoissonArrivals(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "poisson",
		RateRPS: 10.0,
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error: %v", err)
	}

	// Check that events were scheduled
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled")
	}
}

func TestGeneratorUniformArrivals(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "uniform",
		RateRPS: 5.0,
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error: %v", err)
	}

	// Check that events were scheduled
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled")
	}
}

func TestGeneratorNormalArrivals(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:      "normal",
		RateRPS:   10.0,
		StdDevRPS: 2.0,
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error: %v", err)
	}

	// Check that events were scheduled
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled")
	}
}

func TestGeneratorConstantArrivals(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(5 * time.Second)

	arrival := config.ArrivalSpec{
		Type:    "constant",
		RateRPS: 2.0, // 2 requests per second
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error: %v", err)
	}

	// Check that events were scheduled
	// Should have approximately 10 events (2 RPS * 5 seconds)
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled")
	}
	if queueSize < 8 || queueSize > 12 {
		t.Logf("expected around 10 events for constant rate, got %d", queueSize)
	}
}

func TestGeneratorBurstyArrivals(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(10 * time.Second) // Shorter duration for faster test

	arrival := config.ArrivalSpec{
		Type:                 "bursty",
		RateRPS:              5.0,
		BurstRateRPS:         20.0,
		BurstDurationSeconds: 2.0,
		QuietDurationSeconds: 3.0, // Shorter quiet period
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error: %v", err)
	}

	// Check that events were scheduled
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled")
	}
}

func TestGeneratorBurstyArrivalsLongDuration(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	// Test with a longer duration that would have exceeded the old 100k iteration limit
	endTime := startTime.Add(3600 * time.Second) // 1 hour simulation

	arrival := config.ArrivalSpec{
		Type:                 "bursty",
		RateRPS:              10.0,
		BurstRateRPS:         1000.0, // Very high burst rate
		BurstDurationSeconds: 5.0,
		QuietDurationSeconds: 10.0,
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("ScheduleArrivals error for long duration: %v", err)
	}

	// Check that events were scheduled
	queueSize := eng.GetEventQueue().Size()
	if queueSize == 0 {
		t.Fatalf("expected events to be scheduled for long duration simulation")
	}
	// With 1 hour, bursts of 5s every 15s, and 1000 RPS during bursts,
	// we should have many events (this would have exceeded 100k iterations previously)
	t.Logf("Scheduled %d events for 1-hour simulation with high burst rate", queueSize)
}


func TestGeneratorInvalidRate(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "poisson",
		RateRPS: -1.0, // Invalid rate
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err == nil {
		t.Fatalf("expected error for invalid rate")
	}
}

func TestGeneratorDefaultToPoisson(t *testing.T) {
	eng := engine.NewEngine("test-run")
	g := NewGenerator(12345)

	startTime := time.Now()
	endTime := startTime.Add(2 * time.Second) // Shorter duration for faster tests

	arrival := config.ArrivalSpec{
		Type:    "unknown_type",
		RateRPS: 10.0,
	}

	err := g.ScheduleArrivals(eng, startTime, endTime, arrival, "svc1", "/test")
	if err != nil {
		t.Fatalf("expected default to poisson, got error: %v", err)
	}

	// Should have scheduled events (defaults to poisson)
	if eng.GetEventQueue().Size() == 0 {
		t.Fatalf("expected events to be scheduled with default poisson")
	}
}
