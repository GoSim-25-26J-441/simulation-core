package calibration

import "math"

// confidenceScore maps qualitative calibration confidence to [0,1] for floor checks.
func confidenceScore(c ConfidenceLevel) float64 {
	switch c {
	case ConfidenceHigh:
		return 0.9
	case ConfidenceMedium:
		return 0.65
	case ConfidenceLow:
		return 0.35
	default:
		return 0.35
	}
}

// shouldApply enforces OverwritePolicy and ConfidenceFloor against heuristic confidence.
// fieldEmpty means the scenario field is unset or zero-valued so it can be filled without overwriting
// an explicit non-zero user value (OverwriteNever).
// OverwriteNever: only fill missing/zero fields.
// OverwriteWhenHigherConfidence: apply medium/high confidence changes; low confidence only when fieldEmpty.
// OverwriteAlways: apply whenever confidenceScore meets ConfidenceFloor.
func shouldApply(policy OverwritePolicy, fieldEmpty bool, conf ConfidenceLevel, floor float64) bool {
	if math.IsNaN(floor) || floor < 0 {
		floor = 0
	}
	score := confidenceScore(conf)
	if score < floor {
		return false
	}
	switch policy {
	case OverwriteNever:
		return fieldEmpty
	case OverwriteWhenHigherConfidence:
		if fieldEmpty {
			return true
		}
		return conf == ConfidenceMedium || conf == ConfidenceHigh
	case OverwriteAlways:
		return true
	default:
		return false
	}
}
