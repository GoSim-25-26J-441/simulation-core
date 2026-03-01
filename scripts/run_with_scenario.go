//go:build ignore

// Run-with-scenario loads config/scenario.yaml and runs a simulation via the local simd HTTP API.
// Usage: go run scripts/run_with_scenario.go [--addr=http://localhost:8080] [--duration=5000]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	addr := flag.String("addr", "http://localhost:8080", "simd HTTP base URL")
	durationMs := flag.Int64("duration", 5000, "simulation duration in ms")
	flag.Parse()

	// Load scenario YAML from config/scenario.yaml (relative to repo root)
	repoRoot := "."
	if d := os.Getenv("REPO_ROOT"); d != "" {
		repoRoot = d
	}
	scenarioPath := filepath.Join(repoRoot, "config", "scenario.yaml")
	scenarioYAML, err := os.ReadFile(scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read scenario file: %v\n", err)
		os.Exit(1)
	}

	// Create run
	createBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": string(scenarioYAML),
			"duration_ms":   *durationMs,
		},
	}
	createJSON, _ := json.Marshal(createBody)
	resp, err := http.Post(*addr+"/v1/runs", "application/json", bytes.NewReader(createJSON))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create run request: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var buf bytes.Buffer
		buf.ReadFrom(resp.Body)
		fmt.Fprintf(os.Stderr, "create run failed %d: %s\n", resp.StatusCode, buf.String())
		os.Exit(1)
	}

	var createResult struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResult); err != nil {
		fmt.Fprintf(os.Stderr, "decode create response: %v\n", err)
		os.Exit(1)
	}
	runID := createResult.Run.ID
	fmt.Printf("Created run: %s\n", runID)

	// Start run
	resp2, err := http.Post(*addr+"/v1/runs/"+runID, "application/json", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start run request: %v\n", err)
		os.Exit(1)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "start run failed: %d\n", resp2.StatusCode)
		os.Exit(1)
	}
	fmt.Println("Run started.")

	// Poll until completed (or timeout)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		resp3, err := http.Get(*addr + "/v1/runs/" + runID)
		if err != nil {
			continue
		}
		var runState struct {
			Run struct {
				Status string `json:"status"`
			} `json:"run"`
		}
		_ = json.NewDecoder(resp3.Body).Decode(&runState)
		resp3.Body.Close()
		status := runState.Run.Status
		if status == "RUN_STATUS_COMPLETED" {
			fmt.Println("Run completed.")
			time.Sleep(200 * time.Millisecond) // allow metrics to be written
			break
		}
		if status == "RUN_STATUS_FAILED" {
			fmt.Fprintf(os.Stderr, "Run failed.\n")
			os.Exit(1)
		}
	}

	// Fetch metrics
	resp4, err := http.Get(*addr + "/v1/runs/" + runID + "/metrics")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get metrics: %v\n", err)
		os.Exit(1)
	}
	defer resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "metrics failed: %d\n", resp4.StatusCode)
		os.Exit(1)
	}
	var metrics map[string]any
	if err := json.NewDecoder(resp4.Body).Decode(&metrics); err != nil {
		fmt.Fprintf(os.Stderr, "decode metrics: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Metrics:")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(metrics)

	// Accuracy check (for config/scenario.yaml: 20 + 5 RPS, 5s sim)
	durationSec := float64(*durationMs) / 1000.0
	expectedRateRPS := 25.0
	expectedRequestsLo := expectedRateRPS * durationSec * 0.5
	expectedRequestsHi := expectedRateRPS * durationSec * 1.8
		if m, ok := metrics["metrics"].(map[string]any); ok {
		fmt.Println("\n--- Accuracy check (vs config/scenario.yaml) ---")
		total, _ := m["total_requests"].(float64)
		success, _ := m["successful_requests"].(float64)
		failed, _ := m["failed_requests"].(float64)
		latMean, _ := m["latency_mean_ms"].(float64)
		latP50, _ := m["latency_p50_ms"].(float64)

		okTotal := total >= expectedRequestsLo && total <= expectedRequestsHi
		fmt.Printf("Total requests: %.0f (expected ~%.0f–%.0f for %.1f s @ %.0f RPS) — %v\n",
			total, expectedRequestsLo, expectedRequestsHi, durationSec, expectedRateRPS, okTotal)
		fmt.Printf("Successful: %.0f, Failed: %.0f — %v\n", success, failed, failed == 0)
		// Sim-time throughput = total_requests / sim_duration (reported throughput_rps is wall-clock)
		simThroughput := total / durationSec
		throughputOK := simThroughput >= expectedRateRPS*0.5 && simThroughput <= expectedRateRPS*1.8
		fmt.Printf("Sim-time throughput: %.1f RPS (total/sim_duration; offered ~%.0f RPS) — %v\n",
			simThroughput, expectedRateRPS, throughputOK)
		// Scenario: auth/login ~52ms, user/get+db ~34ms+; overall mean should be in 20–80ms range
		latOK := latMean >= 20 && latMean <= 100 && latP50 >= 15 && latP50 <= 120
		fmt.Printf("Latency mean: %.1f ms, P50: %.1f ms (expected ~30–70 ms range) — %v\n",
			latMean, latP50, latOK)
		if total == 0 {
			fmt.Println("Result: no requests recorded (sim may have ended before arrivals; try longer --duration).")
		} else if okTotal && failed == 0 && throughputOK && latOK {
			fmt.Println("Result: metrics look accurate.")
		} else {
			fmt.Println("Result: some checks outside expected range (review above).")
		}
	}
}
