package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/calibration"
)

const cliTestScenarioYAML = `hosts: [{id: h1, cores: 8, memory_gb: 16}]
services:
  - id: api
    replicas: 1
    model: cpu
    endpoints:
      - path: /a
        mean_cpu_ms: 0.5
        cpu_sigma_ms: 0
        net_latency_ms: {mean: 0, sigma: 0}
        failure_rate: 0
workload:
  - from: c
    to: "api:/a"
    arrival: {type: constant, rate_rps: 5}
`

func TestValidateCLI_smoke(t *testing.T) {
	dir := t.TempDir()
	scPath := filepath.Join(dir, "scenario.yaml")
	obsPath := filepath.Join(dir, "observed.json")
	if err := os.WriteFile(scPath, []byte(cliTestScenarioYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := map[string]any{
		"window_seconds": 60,
		"run_metrics": map[string]any{
			"total_requests": 100, "ingress_requests": 100,
			"ingress_error_rate": 0, "latency_p50_ms": 5, "latency_p95_ms": 10,
			"latency_p99_ms": 20, "latency_mean_ms": 6,
			"throughput_rps": 5, "ingress_throughput_rps": 5,
		},
	}
	b, _ := json.Marshal(obs)
	if err := os.WriteFile(obsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	tolPath := filepath.Join(dir, "tol.json")
	_ = os.WriteFile(tolPath, []byte(`{"throughput_rel": 0.2}`), 0o644)

	code := runValidateCLI([]string{
		"-scenario", scPath,
		"-observed", obsPath,
		"-format", calibration.FormatSimulatorExport,
		"-sim-duration-ms", "400",
		"-seeds", "42",
		"-tolerance-profile", "loose",
		"-tolerances", tolPath,
	})
	if code != 0 && code != 3 {
		t.Fatalf("validate CLI exit %d (0 pass, 3 fail)", code)
	}
}

func TestCalibrateCLI_smoke(t *testing.T) {
	dir := t.TempDir()
	scPath := filepath.Join(dir, "scenario.yaml")
	obsPath := filepath.Join(dir, "observed.json")
	outPath := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(scPath, []byte(cliTestScenarioYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	obs := map[string]any{
		"window_seconds": 60,
		"run_metrics": map[string]any{
			"total_requests": 100, "ingress_requests": 100,
			"ingress_error_rate": 0, "latency_p50_ms": 5, "latency_p95_ms": 10,
			"latency_p99_ms": 20, "latency_mean_ms": 6,
			"throughput_rps": 5, "ingress_throughput_rps": 5,
		},
	}
	b, _ := json.Marshal(obs)
	if err := os.WriteFile(obsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	code := runCalibrateCLI([]string{
		"-scenario", scPath,
		"-observed", obsPath,
		"-out", outPath,
		"-sim-duration-ms", "300",
		"-seeds", "7,8",
		"-auto-predict", "true",
	})
	if code != 0 {
		t.Fatalf("calibrate CLI exit %d", code)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "workload:") {
		t.Fatalf("expected calibrated YAML workload section")
	}
}
