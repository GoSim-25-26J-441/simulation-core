//go:build ignore

// Run-optimization creates and starts an optimization run via the simd HTTP API,
// polls until complete, then pulls candidate configurations (scenario YAML + metrics).
// Candidates are distinct node configurations (specifications), deduplicated by scenario
// content; each row is one unique config with a representative run's metrics.
// Node specs (replicas, CPU, memory per service) are printed and written to the summary
// so you can see the optimum configuration for the shared workload.
// Usage: go run scripts/run_optimization.go [--addr=...] [--duration=3000] [--out-dir=...]
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// serviceSpec is the node specification for one service (replicas, CPU, memory).
type serviceSpec struct {
	ID       string  `json:"id"`
	Replicas int     `json:"replicas"`
	CPUCores float64 `json:"cpu_cores,omitempty"`
	MemoryMB float64 `json:"memory_mb,omitempty"`
}

// extractNodeSpecs parses scenario YAML and returns service-level node specs and a short summary string.
func extractNodeSpecs(scenarioYAML string) (specs []serviceSpec, summary string) {
	s, err := config.ParseScenarioYAMLString(scenarioYAML)
	if err != nil || s == nil {
		return nil, ""
	}
	var parts []string
	for i := range s.Services {
		svc := &s.Services[i]
		specs = append(specs, serviceSpec{
			ID:       svc.ID,
			Replicas: svc.Replicas,
			CPUCores: svc.CPUCores,
			MemoryMB: svc.MemoryMB,
		})
		// e.g. "auth: 2r 2C 1024M"
		p := fmt.Sprintf("%s: %dr", svc.ID, svc.Replicas)
		if svc.CPUCores > 0 {
			p += fmt.Sprintf(" %.1gC", svc.CPUCores)
		}
		if svc.MemoryMB > 0 {
			p += fmt.Sprintf(" %.0fM", svc.MemoryMB)
		}
		parts = append(parts, p)
	}
	return specs, strings.Join(parts, "; ")
}

func main() {
	addr := flag.String("addr", "http://localhost:8080", "simd HTTP base URL")
	durationMs := flag.Int64("duration", 3000, "simulation duration per iteration (ms)")
	outDir := flag.String("out-dir", "", "if set, write candidate scenario YAMLs and summary JSON here")
	flag.Parse()

	repoRoot := "."
	if d := os.Getenv("REPO_ROOT"); d != "" {
		repoRoot = d
	}
	scenarioPath := filepath.Join(repoRoot, "config", "scenario.yaml")
	scenarioYAML, err := os.ReadFile(scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read scenario: %v\n", err)
		os.Exit(1)
	}

	// Create optimization run (must include "optimization" in input)
	createBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": string(scenarioYAML),
			"duration_ms":   *durationMs,
			"optimization": map[string]any{
				"objective":      "p95_latency_ms",
				"max_iterations": 2,
				"step_size":      1.0,
			},
		},
	}
	createJSON, _ := json.Marshal(createBody)
	resp, err := http.Post(*addr+"/v1/runs", "application/json", bytes.NewReader(createJSON))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create run: %v\n", err)
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
			ID              string `json:"id"`
			CreatedAtUnixMs float64 `json:"created_at_unix_ms"`
		} `json:"run"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResult); err != nil {
		fmt.Fprintf(os.Stderr, "decode create response: %v\n", err)
		os.Exit(1)
	}
	runID := createResult.Run.ID
	parentCreatedMs := createResult.Run.CreatedAtUnixMs
	fmt.Printf("Created optimization run: %s\n", runID)

	// Start run
	resp2, err := http.Post(*addr+"/v1/runs/"+runID, "application/json", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start run: %v\n", err)
		os.Exit(1)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "start run failed: %d\n", resp2.StatusCode)
		os.Exit(1)
	}
	fmt.Println("Optimization started. Polling for completion...")

	deadline := time.Now().Add(2 * time.Minute)
	var bestRunID string
	var bestScore float64
	var iterations float64
poll:
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		resp3, err := http.Get(*addr + "/v1/runs/" + runID)
		if err != nil {
			continue
		}
		var getResp struct {
			Run map[string]any `json:"run"`
		}
		_ = json.NewDecoder(resp3.Body).Decode(&getResp)
		resp3.Body.Close()

		run := getResp.Run
		if run == nil {
			continue
		}
		status, _ := run["status"].(string)

		switch status {
		case "RUN_STATUS_COMPLETED":
			bestRunID, _ = run["best_run_id"].(string)
			bestScore, _ = run["best_score"].(float64)
			iterations, _ = run["iterations"].(float64)
			break poll
		case "RUN_STATUS_FAILED":
			errMsg, _ := run["error"].(string)
			fmt.Fprintf(os.Stderr, "Optimization failed: %s\n", errMsg)
			if errMsg == "optimization not enabled" {
				fmt.Fprintf(os.Stderr, "Hint: ensure the server was started with cmd/simd (main.go sets the optimization runner).\n")
			}
			os.Exit(1)
		}
	}

	if bestRunID == "" {
		fmt.Fprintf(os.Stderr, "Timeout or optimization completed without best_run_id.\n")
		os.Exit(1)
	}

	fmt.Printf("Optimization completed.\n  best_run_id: %s\n  best_score: %.2f\n  iterations: %.0f\n", bestRunID, bestScore, iterations)
	fmt.Println("OK: optimization is working.")

	// Prefer candidate_run_ids from the optimization run (sent by the simulation engine).
	// Fall back to discovering opt-* runs in a time window if not present.
	var runIDsToUse []string
	getResp, err := http.Get(*addr + "/v1/runs/" + runID)
	if err == nil {
		var getResult struct {
			Run map[string]any `json:"run"`
		}
		if json.NewDecoder(getResp.Body).Decode(&getResult) == nil && getResult.Run != nil {
			if ids, ok := getResult.Run["candidate_run_ids"].([]interface{}); ok && len(ids) > 0 {
				for _, v := range ids {
					if s, _ := v.(string); s != "" {
						runIDsToUse = append(runIDsToUse, s)
					}
				}
			}
		}
		getResp.Body.Close()
	}
	if len(runIDsToUse) > 0 {
		fmt.Printf("Using %d candidate run(s) from optimization result (simulation engine).\n", len(runIDsToUse))
	}
	if len(runIDsToUse) == 0 {
		// Fallback: list runs and take opt-* runs created within window after parent
		const candidateWindowMs = 10 * 60 * 1000 // 10 minutes
		listResp, err := http.Get(*addr + "/v1/runs?limit=500")
		if err != nil {
			fmt.Fprintf(os.Stderr, "list runs: %v\n", err)
			os.Exit(1)
		}
		var listResult struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.NewDecoder(listResp.Body).Decode(&listResult); err != nil {
			listResp.Body.Close()
			fmt.Fprintf(os.Stderr, "decode list: %v\n", err)
			os.Exit(1)
		}
		listResp.Body.Close()
		for _, r := range listResult.Runs {
			id, _ := r["id"].(string)
			createdMs, _ := r["created_at_unix_ms"].(float64)
			if strings.HasPrefix(id, "opt-") && createdMs >= parentCreatedMs && createdMs <= parentCreatedMs+candidateWindowMs {
				runIDsToUse = append(runIDsToUse, id)
			}
		}
	}

	// Fetch export for each run and deduplicate by scenario (node configuration).
	// configKey = hash of normalized scenario YAML; value = best representative run for that config.
	type runInfo struct {
		ID         string
		ScenarioYAML string
		ScoreP95   float64
		TotalReqs  int64
	}
	configToRun := make(map[string]*runInfo) // configKey -> best representative
	for _, id := range runIDsToUse {
		expResp, err := http.Get(*addr + "/v1/runs/" + id + "/export")
		if err != nil {
			continue
		}
		var export struct {
			Input   map[string]any `json:"input"`
			Metrics map[string]any `json:"metrics"`
		}
		_ = json.NewDecoder(expResp.Body).Decode(&export)
		expResp.Body.Close()

		scenarioYAML, _ := export.Input["scenario_yaml"].(string)
		scenarioYAML = strings.TrimSpace(scenarioYAML)
		if scenarioYAML == "" {
			continue
		}
		key := sha256.Sum256([]byte(scenarioYAML))
		configKey := hex.EncodeToString(key[:])

		scoreP95 := 0.0
		if m, ok := export.Metrics["metrics"].(map[string]any); ok {
			if v, ok := m["latency_p95_ms"].(float64); ok {
				scoreP95 = v
			}
		}
		totalReqs := int64(0)
		if m, ok := export.Metrics["metrics"].(map[string]any); ok {
			if v, ok := m["total_requests"].(float64); ok {
				totalReqs = int64(v)
			}
		}

		existing, ok := configToRun[configKey]
		replace := !ok
		if ok {
			if scoreP95 < existing.ScoreP95 {
				replace = true
			} else if scoreP95 == existing.ScoreP95 && id == bestRunID {
				replace = true // prefer the run that won the optimization
			}
		}
		if replace {
			configToRun[configKey] = &runInfo{
				ID:           id,
				ScenarioYAML: scenarioYAML,
				ScoreP95:     scoreP95,
				TotalReqs:    totalReqs,
			}
		}
	}

	// Build sorted list of unique candidate configurations (by p95 then id)
	type candidateEntry struct {
		RunID        string
		ScenarioYAML string
		ScoreP95     float64
		TotalReqs    int64
	}
	var candidates []candidateEntry
	for _, info := range configToRun {
		candidates = append(candidates, candidateEntry{
			RunID:        info.ID,
			ScenarioYAML: info.ScenarioYAML,
			ScoreP95:     info.ScoreP95,
			TotalReqs:    info.TotalReqs,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ScoreP95 != candidates[j].ScoreP95 {
			return candidates[i].ScoreP95 < candidates[j].ScoreP95
		}
		return candidates[i].RunID < candidates[j].RunID
	})

	fmt.Printf("\nCandidate configurations (unique node specs): %d\n", len(candidates))

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir out-dir: %v\n", err)
			os.Exit(1)
		}
	}

	type candidateSummary struct {
		Index        int          `json:"index"`
		RunID        string       `json:"run_id"`
		ScoreP95Ms   float64      `json:"score_p95_latency_ms,omitempty"`
		TotalReqs    int64        `json:"total_requests,omitempty"`
		IsBest       bool         `json:"is_best"`
		ScenarioPath string       `json:"scenario_path,omitempty"`
		NodeSpecs    []serviceSpec `json:"node_specs,omitempty"`   // replicas, cpu_cores, memory_mb per service
		SpecSummary  string       `json:"spec_summary,omitempty"`   // one-line e.g. "auth: 2r 2C 1024M; user: 2r"
	}
	var summary []candidateSummary

	for i, c := range candidates {
		isBest := c.RunID == bestRunID
		label := ""
		if isBest {
			label = " (best)"
		}
		specs, specSummary := extractNodeSpecs(c.ScenarioYAML)
		fmt.Printf("  [%d] %s  p95=%.2f ms  requests=%d%s\n", i+1, c.RunID, c.ScoreP95, c.TotalReqs, label)
		if specSummary != "" {
			fmt.Printf("       specs: %s\n", specSummary)
		}

		summary = append(summary, candidateSummary{
			Index:        i + 1,
			RunID:        c.RunID,
			ScoreP95Ms:   c.ScoreP95,
			TotalReqs:    c.TotalReqs,
			IsBest:       isBest,
			NodeSpecs:    specs,
			SpecSummary:  specSummary,
		})

		if *outDir != "" && c.ScenarioYAML != "" {
			safeID := strings.ReplaceAll(c.RunID, ":", "-")
			name := fmt.Sprintf("candidate_%d_%s.yaml", i+1, safeID)
			if isBest {
				name = "best_scenario.yaml"
			}
			path := filepath.Join(*outDir, name)
			if err := os.WriteFile(path, []byte(c.ScenarioYAML), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			} else {
				summary[len(summary)-1].ScenarioPath = path
				if isBest {
					fmt.Printf("      best config written to %s\n", path)
				}
			}
		}
	}

	if *outDir != "" {
		summaryPath := filepath.Join(*outDir, "candidates_summary.json")
		b, _ := json.MarshalIndent(map[string]any{
			"optimization_run_id": runID,
			"best_run_id":         bestRunID,
			"best_score":          bestScore,
			"iterations":          iterations,
			"candidate_configs":   summary,
			"note":                "candidates are unique node configurations (specs); node_specs/spec_summary give replicas, cpu_cores, memory_mb per service for the shared workload",
		}, "", "  ")
		if err := os.WriteFile(summaryPath, b, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write summary: %v\n", err)
		} else {
			fmt.Printf("\nSummary written to %s\n", summaryPath)
		}
	}
}
