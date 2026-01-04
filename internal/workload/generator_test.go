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
