//go:build integration
// +build integration

package integration_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

func TestIntegration_ConfigAndScenarioLoadSmoke(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "config", "config.yaml")
	scenarioPath := filepath.Join("..", "..", "config", "scenario.yaml")

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig(%s) failed: %v", cfgPath, err)
	}
	if cfg == nil {
		t.Fatalf("LoadConfig(%s) returned nil config", cfgPath)
	}

	scenario, err := config.LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("LoadScenario(%s) failed: %v", scenarioPath, err)
	}
	if scenario == nil {
		t.Fatalf("LoadScenario(%s) returned nil scenario", scenarioPath)
	}
	if len(scenario.Services) == 0 {
		t.Fatalf("expected scenario to define at least one service")
	}
	if len(scenario.Workload) == 0 {
		t.Fatalf("expected scenario to define at least one workload pattern")
	}
}

func TestIntegration_EngineRunFromScenarioWorkloadSmoke(t *testing.T) {
	scenarioPath := filepath.Join("..", "..", "config", "scenario.yaml")
	scenario, err := config.LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("LoadScenario(%s) failed: %v", scenarioPath, err)
	}

	runID := utils.GenerateRunID()
	e := engine.NewEngine(runID)

	// Minimal handlers to simulate request lifecycle and produce run metrics.
	e.RegisterHandler(engine.EventTypeRequestArrival, func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return nil
		}
		now := eng.GetSimTime()
		evt.Request.Status = models.RequestStatusProcessing
		evt.Request.ArrivalTime = now
		evt.Request.StartTime = now

		// Fixed 1ms service time for smoke purposes.
		eng.ScheduleAfter(engine.EventTypeRequestComplete, time.Millisecond, evt.Request, evt.ServiceID, nil)
		return nil
	})

	e.RegisterHandler(engine.EventTypeRequestComplete, func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return nil
		}
		now := eng.GetSimTime()
		evt.Request.Status = models.RequestStatusCompleted
		evt.Request.CompletionTime = now
		evt.Request.Duration = now.Sub(evt.Request.StartTime)

		eng.GetRunManager().AddRequest(evt.Request)
		eng.GetRunManager().RecordLatency(utils.TimeToMs(evt.Request.Duration))
		return nil
	})

	// Schedule a small number of arrivals based on scenario workload patterns.
	start := e.GetSimTime()
	totalScheduled := 0

	for _, wl := range scenario.Workload {
		// Expected format: "service:/path"
		parts := strings.SplitN(wl.To, ":", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid workload.to format %q (expected service:/path)", wl.To)
		}
		serviceID := parts[0]
		endpoint := parts[1]

		// Keep it intentionally tiny/fast: schedule 3 arrivals per workload entry.
		for i := 0; i < 3; i++ {
			req := &models.Request{
				ID:          utils.GenerateRequestID(),
				TraceID:     utils.GenerateTraceID(),
				ServiceName: serviceID,
				Endpoint:    endpoint,
				Status:      models.RequestStatusPending,
				Metadata:    map[string]any{"workload_from": wl.From},
			}
			e.ScheduleAt(engine.EventTypeRequestArrival, start.Add(time.Duration(i)*time.Millisecond), req, serviceID, nil)
			totalScheduled++
		}
	}

	// Run long enough to process all scheduled arrivals and completions.
	if err := e.Run(50 * time.Millisecond); err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	run := e.GetRunManager().GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Fatalf("expected run status %q, got %q", models.RunStatusCompleted, run.Status)
	}
	if run.Metrics == nil {
		t.Fatalf("expected run metrics to be computed")
	}
	if got := int(run.Metrics.TotalRequests); got != totalScheduled {
		t.Fatalf("expected %d total requests, got %d", totalScheduled, got)
	}
}
