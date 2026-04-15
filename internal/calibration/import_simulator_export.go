package calibration

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// simulatorExportEnvelope is the documented JSON shape for round-tripping simulator metrics into calibration.
// All fields in run_metrics that deserialize successfully are turned into Present observations via FromRunMetrics.
type simulatorExportEnvelope struct {
	WindowSeconds float64          `json:"window_seconds"`
	Window        string           `json:"window"` // alternative: Go duration string, e.g. "60s"
	RunMetrics    *models.RunMetrics `json:"run_metrics"`
}

// ObservedFromSimulatorExportJSON parses {"window_seconds": N, "run_metrics": {...}} or {"window":"60s","run_metrics":{...}}.
// Presence semantics: every populated scalar in run_metrics becomes a Present ObservedValue (simulator snapshot contract).
func ObservedFromSimulatorExportJSON(data []byte) (*ObservedMetrics, error) {
	var env simulatorExportEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("simulator_export: %w", err)
	}
	if env.RunMetrics == nil {
		return nil, fmt.Errorf("simulator_export: run_metrics is required")
	}
	var window time.Duration
	switch {
	case env.Window != "":
		d, err := time.ParseDuration(env.Window)
		if err != nil {
			return nil, fmt.Errorf("simulator_export: window: %w", err)
		}
		window = d
	case env.WindowSeconds > 0:
		window = time.Duration(env.WindowSeconds * float64(time.Second))
	default:
		window = time.Minute
	}
	return FromRunMetrics(env.RunMetrics, window), nil
}
