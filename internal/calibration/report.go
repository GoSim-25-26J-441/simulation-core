package calibration

import "fmt"

// ConfidenceLevel is a coarse reliability bucket for a calibrated field.
type ConfidenceLevel string

const (
	ConfidenceHigh   ConfidenceLevel = "high"
	ConfidenceMedium ConfidenceLevel = "medium"
	ConfidenceLow    ConfidenceLevel = "low"
)

// FieldChange records one applied adjustment.
type FieldChange struct {
	Path       string // e.g. workload[0].arrival.rate_rps
	OldValue   interface{}
	NewValue   interface{}
	Reason     string
	Confidence ConfidenceLevel
}

// CalibrationReport documents what calibration considered and applied.
type CalibrationReport struct {
	Changes              []FieldChange
	Warnings             []string
	Skipped              []string
	SkippedLowConfidence []string
	AmbiguousMappings    []string
}

func (r *CalibrationReport) add(path string, oldv, newv interface{}, reason string, conf ConfidenceLevel) {
	if r == nil {
		return
	}
	r.Changes = append(r.Changes, FieldChange{
		Path: path, OldValue: oldv, NewValue: newv, Reason: reason, Confidence: conf,
	})
}

func (r *CalibrationReport) warnf(format string, args ...interface{}) {
	if r == nil {
		return
	}
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func (r *CalibrationReport) skipLowConf(format string, args ...interface{}) {
	if r == nil {
		return
	}
	r.SkippedLowConfidence = append(r.SkippedLowConfidence, fmt.Sprintf(format, args...))
}

func (r *CalibrationReport) ambiguousf(format string, args ...interface{}) {
	if r == nil {
		return
	}
	r.AmbiguousMappings = append(r.AmbiguousMappings, fmt.Sprintf(format, args...))
}

func clampScale(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}
