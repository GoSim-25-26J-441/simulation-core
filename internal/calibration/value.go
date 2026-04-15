package calibration

import "math"

// ObservedValue separates "missing" from an explicit measured zero for optional observation fields.
type ObservedValue[T comparable] struct {
	Present bool
	Value   T
}

// F64 records a float observation.
func F64(v float64) ObservedValue[float64] {
	return ObservedValue[float64]{Present: true, Value: v}
}

// I64 records an int64 observation.
func I64(v int64) ObservedValue[int64] {
	return ObservedValue[int64]{Present: true, Value: v}
}

func fieldEmptyFloat(old float64) bool {
	return math.Abs(old) < 1e-12
}

func fieldEmptyInt(old int) bool {
	return old <= 0
}
