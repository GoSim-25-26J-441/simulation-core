package calibration

import "github.com/GoSim-25-26J-441/simulation-core/pkg/models"

// OverwritePolicy controls whether explicit scenario fields may be replaced by calibration.
type OverwritePolicy int

const (
	// OverwriteNever keeps user-provided values; only fill missing/zero fields from observations.
	OverwriteNever OverwritePolicy = iota
	// OverwriteWhenHigherConfidence replaces values when the calibration report marks higher confidence than "low".
	OverwriteWhenHigherConfidence
	// OverwriteAlways applies heuristic adjustments whenever observations provide a signal.
	OverwriteAlways
)

// CalibrateOptions configures calibration behavior.
type CalibrateOptions struct {
	Overwrite OverwritePolicy

	// PredictedRunMetrics is from RunScenarioForMetrics (or live run) of the scenario **before** calibration.
	// Ratio-based tuning uses observed / predicted when both are non-zero.
	PredictedRun *models.RunMetrics

	// ConfidenceFloor skips updates when heuristic confidence would be below this (0..1).
	ConfidenceFloor float64

	// MinScaleFactor and MaxScaleFactor clamp multiplicative adjustments to endpoint CPU and workload rate.
	MinScaleFactor float64
	MaxScaleFactor float64
}

func defaultCalibrateOptions() *CalibrateOptions {
	return &CalibrateOptions{
		Overwrite:       OverwriteWhenHigherConfidence,
		ConfidenceFloor: 0.2,
		MinScaleFactor:  0.25,
		MaxScaleFactor:  4.0,
	}
}

// ValidateOptions configures multi-seed validation.
type ValidateOptions struct {
	Seeds            []int64
	RealTimeWorkload bool
	Tolerances       *ValidationTolerances
	// AllowPartialFields is retained for API compatibility. Validation only compares
	// metrics marked Present on ObservedMetrics (missing fields are skipped).
	AllowPartialFields bool
}

func defaultValidateOptions() *ValidateOptions {
	return &ValidateOptions{
		Seeds:              []int64{1, 2, 3},
		RealTimeWorkload:   false,
		Tolerances:         DefaultValidationTolerances(),
		AllowPartialFields: true,
	}
}
