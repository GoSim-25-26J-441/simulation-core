package calibration

import "testing"

func TestShouldApplyOverwriteNever(t *testing.T) {
	floor := 0.2
	if !shouldApply(OverwriteNever, true, ConfidenceLow, floor) {
		t.Fatal("empty field: low confidence should apply above floor")
	}
	if shouldApply(OverwriteNever, false, ConfidenceHigh, floor) {
		t.Fatal("non-empty: never overwrite")
	}
}

func TestShouldApplyOverwriteWhenHigherConfidence(t *testing.T) {
	floor := 0.2
	if !shouldApply(OverwriteWhenHigherConfidence, true, ConfidenceLow, floor) {
		t.Fatal("empty + low should apply")
	}
	if shouldApply(OverwriteWhenHigherConfidence, false, ConfidenceLow, floor) {
		t.Fatal("non-empty + low should skip")
	}
	if !shouldApply(OverwriteWhenHigherConfidence, false, ConfidenceMedium, floor) {
		t.Fatal("non-empty + medium should apply")
	}
	if !shouldApply(OverwriteWhenHigherConfidence, false, ConfidenceHigh, floor) {
		t.Fatal("non-empty + high should apply")
	}
	if shouldApply(OverwriteWhenHigherConfidence, false, ConfidenceLow, 0.99) {
		t.Fatal("below floor should never apply")
	}
}

func TestShouldApplyOverwriteAlways(t *testing.T) {
	floor := 0.2
	if !shouldApply(OverwriteAlways, false, ConfidenceLow, floor) {
		t.Fatal("always + low should apply when above floor")
	}
	if shouldApply(OverwriteAlways, false, ConfidenceLow, 0.99) {
		t.Fatal("below floor should skip")
	}
}
