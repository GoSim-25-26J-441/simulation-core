package utils

import (
	"math"
	"sort"
)

// Min returns the minimum of two integers
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two integers
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// MinFloat64 returns the minimum of two float64 values
func MinFloat64(a, b float64) float64 {
	return math.Min(a, b)
}

// MaxFloat64 returns the maximum of two float64 values
func MaxFloat64(a, b float64) float64 {
	return math.Max(a, b)
}

// Clamp clamps a value between min and max
func Clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ClampFloat64 clamps a float64 value between min and max
func ClampFloat64(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// Mean calculates the mean of a slice of float64 values
func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// Variance calculates the variance of a slice of float64 values
func Variance(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := Mean(values)
	sumSquares := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquares += diff * diff
	}
	return sumSquares / float64(len(values))
}

// StdDev calculates the standard deviation of a slice of float64 values
func StdDev(values []float64) float64 {
	return math.Sqrt(Variance(values))
}

// Percentile calculates the percentile of a slice of float64 values
// percentile should be between 0 and 100
func Percentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Create a copy and sort it
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	// Calculate index
	index := (percentile / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))

	if lower == upper {
		return sorted[lower]
	}

	// Linear interpolation between lower and upper
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// P50 calculates the 50th percentile (median)
func P50(values []float64) float64 {
	return Percentile(values, 50)
}

// P95 calculates the 95th percentile
func P95(values []float64) float64 {
	return Percentile(values, 95)
}

// P99 calculates the 99th percentile
func P99(values []float64) float64 {
	return Percentile(values, 99)
}

// Sum calculates the sum of a slice of float64 values
func Sum(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum
}

// Round rounds a float64 to the specified number of decimal places
func Round(value float64, decimals int) float64 {
	multiplier := math.Pow(10, float64(decimals))
	return math.Round(value*multiplier) / multiplier
}
