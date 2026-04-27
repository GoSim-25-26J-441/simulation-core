package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/internal/calibration"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func runValidateCLI(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	scenarioPath := fs.String("scenario", "", "path to scenario YAML")
	observedPath := fs.String("observed", "", "path to observed metrics JSON")
	format := fs.String("format", calibration.FormatSimulatorExport, "observed format: simulator_export | observed_metrics | prometheus_json")
	simMs := fs.Int64("sim-duration-ms", 10_000, "simulation duration per seed (ms)")
	seeds := fs.String("seeds", "1,2,3", "comma-separated seeds")
	toleranceProfile := fs.String("tolerance-profile", "", "optional: default | strict | loose")
	tolerancesPath := fs.String("tolerances", "", "optional path to JSON file with partial tolerance overrides (merged on top of profile/default)")
	realtimeWorkload := fs.Bool("realtime-workload", false, "use realtime workload generation (lazy arrivals)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if *scenarioPath == "" || *observedPath == "" {
		fmt.Fprintln(os.Stderr, "validate: -scenario and -observed are required")
		fs.Usage()
		return 2
	}
	yamlBytes, err := os.ReadFile(*scenarioPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	obsBytes, err := os.ReadFile(*observedPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sc, err := config.ParseScenarioYAML(yamlBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	obs, err := calibration.DecodeObservedMetrics(*format, obsBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sd, err := parseSeedList(*seeds)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tol := calibration.DefaultValidationTolerances()
	if strings.TrimSpace(*toleranceProfile) != "" {
		tol = calibration.ResolveToleranceProfile(*toleranceProfile)
	}
	if strings.TrimSpace(*tolerancesPath) != "" {
		b, err := os.ReadFile(*tolerancesPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var err2 error
		tol, err2 = calibration.ApplyToleranceJSON(tol, json.RawMessage(b))
		if err2 != nil {
			fmt.Fprintln(os.Stderr, err2)
			return 1
		}
	}
	rep, err := calibration.ValidateScenario(sc, obs, *simMs, &calibration.ValidateOptions{
		Seeds:            sd,
		Tolerances:       tol,
		RealTimeWorkload: *realtimeWorkload,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"pass":              rep.Pass,
		"validation_report": rep,
		"warnings":          rep.Warnings,
		"largest_errors":    rep.LargestErrors,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !rep.Pass {
		return 3
	}
	return 0
}

func runCalibrateCLI(args []string) int {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	scenarioPath := fs.String("scenario", "", "path to scenario YAML")
	observedPath := fs.String("observed", "", "path to observed metrics JSON")
	format := fs.String("format", calibration.FormatSimulatorExport, "observed format")
	outPath := fs.String("out", "", "write calibrated scenario YAML to this path (required)")
	predPath := fs.String("predicted-run", "", "optional path to RunMetrics JSON or {run_metrics:{...}} export")
	simMs := fs.Int64("sim-duration-ms", 10_000, "when -predicted-run is omitted and -auto-predict=true, baseline sim duration (ms) per seed")
	seeds := fs.String("seeds", "1,2,3", "comma-separated seeds for baseline prediction when -predicted-run is omitted")
	autoPredict := fs.Bool("auto-predict", true, "when -predicted-run is omitted, run baseline simulator per seed and merge for ratio calibration")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if *scenarioPath == "" || *observedPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "calibrate: -scenario, -observed, and -out are required")
		fs.Usage()
		return 2
	}
	yamlBytes, err := os.ReadFile(*scenarioPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	obsBytes, err := os.ReadFile(*observedPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sc, err := config.ParseScenarioYAML(yamlBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	obs, err := calibration.DecodeObservedMetrics(*format, obsBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	opts := &calibration.CalibrateOptions{
		Overwrite:       calibration.OverwriteWhenHigherConfidence,
		ConfidenceFloor: 0.2,
		MinScaleFactor:  0.25,
		MaxScaleFactor:  4.0,
	}
	if *predPath != "" {
		pb, err := os.ReadFile(*predPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var rm models.RunMetrics
		if err := json.Unmarshal(pb, &rm); err == nil &&
			(rm.TotalRequests > 0 || rm.ServiceMetrics != nil || len(rm.EndpointRequestStats) > 0) {
			opts.PredictedRun = &rm
		} else {
			var env struct {
				RunMetrics *models.RunMetrics `json:"run_metrics"`
			}
			if err := json.Unmarshal(pb, &env); err == nil && env.RunMetrics != nil {
				opts.PredictedRun = env.RunMetrics
			}
		}
	} else if *autoPredict {
		sd, err := parseSeedList(*seeds)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		pred, err := calibration.RunBaselinePredictedRun(sc, *simMs, sd, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		opts.PredictedRun = pred
	}
	outSc, rep, err := calibration.CalibrateScenario(sc, obs, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	outYAML, err := config.MarshalScenarioYAML(outSc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(*outPath, []byte(outYAML), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"calibration_report": rep,
		"warnings":           mergeCalibCLIWarnings(rep),
		"output":             *outPath,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func mergeCalibCLIWarnings(rep *calibration.CalibrationReport) []string {
	if rep == nil {
		return nil
	}
	var out []string
	out = append(out, rep.Warnings...)
	for _, s := range rep.AmbiguousMappings {
		out = append(out, "ambiguous: "+s)
	}
	for _, s := range rep.SkippedLowConfidence {
		out = append(out, "skipped_low_confidence: "+s)
	}
	return out
}

func parseSeedList(s string) ([]int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []int64{1}, nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("seeds: %w", err)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return []int64{1}, nil
	}
	return out, nil
}
