package calibration

import "testing"

func TestDefaultCalibrateOptions(t *testing.T) {
	opts := defaultCalibrateOptions()
	if opts == nil {
		t.Fatalf("expected non-nil calibrate options")
	}
	if opts.Overwrite != OverwriteWhenHigherConfidence {
		t.Fatalf("unexpected overwrite default: %v", opts.Overwrite)
	}
	if opts.ConfidenceFloor != 0.2 {
		t.Fatalf("unexpected confidence floor: %v", opts.ConfidenceFloor)
	}
	if opts.MinScaleFactor != 0.25 || opts.MaxScaleFactor != 4.0 {
		t.Fatalf("unexpected scale factor defaults: min=%v max=%v", opts.MinScaleFactor, opts.MaxScaleFactor)
	}
}

func TestDefaultValidateOptions(t *testing.T) {
	opts := defaultValidateOptions()
	if opts == nil {
		t.Fatalf("expected non-nil validate options")
	}
	if len(opts.Seeds) != 3 || opts.Seeds[0] != 1 || opts.Seeds[1] != 2 || opts.Seeds[2] != 3 {
		t.Fatalf("unexpected default seeds: %#v", opts.Seeds)
	}
	if opts.RealTimeWorkload {
		t.Fatalf("expected default real-time workload false")
	}
	if !opts.AllowPartialFields {
		t.Fatalf("expected allow_partial_fields default true")
	}
	if opts.Tolerances == nil {
		t.Fatalf("expected default tolerances")
	}
}
