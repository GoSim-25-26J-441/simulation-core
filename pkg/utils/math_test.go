package utils

import (
	"math"
	"testing"
)

func TestMin(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{5, 10, 5},
		{10, 5, 5},
		{-5, 5, -5},
		{0, 0, 0},
	}

	for _, tt := range tests {
		result := Min(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("Min(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestMax(t *testing.T) {
	tests := []struct {
		a, b, expected int
	}{
		{5, 10, 10},
		{10, 5, 10},
		{-5, 5, 5},
		{0, 0, 0},
	}

	for _, tt := range tests {
		result := Max(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("Max(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestMinFloat64(t *testing.T) {
	tests := []struct {
		a, b, expected float64
	}{
		{5.5, 10.3, 5.5},
		{10.3, 5.5, 5.5},
		{-5.2, 5.2, -5.2},
		{0.0, 0.0, 0.0},
	}

	for _, tt := range tests {
		result := MinFloat64(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("MinFloat64(%f, %f) = %f, expected %f", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestMaxFloat64(t *testing.T) {
	tests := []struct {
		a, b, expected float64
	}{
		{5.5, 10.3, 10.3},
		{10.3, 5.5, 10.3},
		{-5.2, 5.2, 5.2},
		{0.0, 0.0, 0.0},
	}

	for _, tt := range tests {
		result := MaxFloat64(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("MaxFloat64(%f, %f) = %f, expected %f", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		value, min, max, expected int
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{5, 5, 10, 5},
		{10, 5, 10, 10},
	}

	for _, tt := range tests {
		result := Clamp(tt.value, tt.min, tt.max)
		if result != tt.expected {
			t.Errorf("Clamp(%d, %d, %d) = %d, expected %d",
				tt.value, tt.min, tt.max, result, tt.expected)
		}
	}
}

func TestClampFloat64(t *testing.T) {
	tests := []struct {
		value, min, max, expected float64
	}{
		{5.5, 0.0, 10.0, 5.5},
		{-5.5, 0.0, 10.0, 0.0},
		{15.5, 0.0, 10.0, 10.0},
		{5.5, 5.5, 10.0, 5.5},
		{10.0, 5.0, 10.0, 10.0},
	}

	for _, tt := range tests {
		result := ClampFloat64(tt.value, tt.min, tt.max)
		if result != tt.expected {
			t.Errorf("ClampFloat64(%f, %f, %f) = %f, expected %f",
				tt.value, tt.min, tt.max, result, tt.expected)
		}
	}
}

func TestMean(t *testing.T) {
	tests := []struct {
		values   []float64
		expected float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 3.0},
		{[]float64{10, 20, 30}, 20.0},
		{[]float64{5}, 5.0},
		{[]float64{}, 0.0},
		{[]float64{-10, 10}, 0.0},
	}

	for _, tt := range tests {
		result := Mean(tt.values)
		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("Mean(%v) = %f, expected %f", tt.values, result, tt.expected)
		}
	}
}

func TestVariance(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	variance := Variance(values)

	// Variance of 1,2,3,4,5 is 2.0
	expected := 2.0
	if math.Abs(variance-expected) > 1e-9 {
		t.Errorf("Variance(%v) = %f, expected %f", values, variance, expected)
	}

	// Empty slice
	emptyVariance := Variance([]float64{})
	if emptyVariance != 0.0 {
		t.Errorf("Variance of empty slice should be 0, got %f", emptyVariance)
	}
}

func TestStdDev(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	stddev := StdDev(values)

	// StdDev of 1,2,3,4,5 is sqrt(2.0) ≈ 1.414
	expected := math.Sqrt(2.0)
	if math.Abs(stddev-expected) > 1e-9 {
		t.Errorf("StdDev(%v) = %f, expected %f", values, stddev, expected)
	}
}

func TestPercentile(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	tests := []struct {
		percentile float64
		expected   float64
	}{
		{0, 1},
		{25, 3.25},
		{50, 5.5},
		{75, 7.75},
		{100, 10},
	}

	for _, tt := range tests {
		result := Percentile(values, tt.percentile)
		if math.Abs(result-tt.expected) > 0.01 {
			t.Errorf("Percentile(%v, %f) = %f, expected %f",
				values, tt.percentile, result, tt.expected)
		}
	}

	// Empty slice
	emptyP50 := Percentile([]float64{}, 50)
	if emptyP50 != 0.0 {
		t.Errorf("Percentile of empty slice should be 0, got %f", emptyP50)
	}
}

func TestP50(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	p50 := P50(values)

	expected := 3.0
	if math.Abs(p50-expected) > 1e-9 {
		t.Errorf("P50(%v) = %f, expected %f", values, p50, expected)
	}
}

func TestP95(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p95 := P95(values)

	expected := 9.55
	if math.Abs(p95-expected) > 0.01 {
		t.Errorf("P95(%v) = %f, expected %f", values, p95, expected)
	}
}

func TestP99(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p99 := P99(values)

	expected := 9.91
	if math.Abs(p99-expected) > 0.01 {
		t.Errorf("P99(%v) = %f, expected %f", values, p99, expected)
	}
}

func TestSum(t *testing.T) {
	tests := []struct {
		values   []float64
		expected float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 15.0},
		{[]float64{10, 20, 30}, 60.0},
		{[]float64{5}, 5.0},
		{[]float64{}, 0.0},
		{[]float64{-10, 10}, 0.0},
	}

	for _, tt := range tests {
		result := Sum(tt.values)
		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("Sum(%v) = %f, expected %f", tt.values, result, tt.expected)
		}
	}
}

func TestRound(t *testing.T) {
	tests := []struct {
		value    float64
		decimals int
		expected float64
	}{
		{3.14159, 2, 3.14},
		{3.14159, 4, 3.1416},
		{3.5, 0, 4.0},
		{3.4, 0, 3.0},
		{123.456, 1, 123.5},
	}

	for _, tt := range tests {
		result := Round(tt.value, tt.decimals)
		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("Round(%f, %d) = %f, expected %f",
				tt.value, tt.decimals, result, tt.expected)
		}
	}
}

func TestPercentileEdgeCases(t *testing.T) {
	// Single value
	single := []float64{5.0}
	if P50(single) != 5.0 {
		t.Error("P50 of single value should be that value")
	}

	// Two values
	two := []float64{1.0, 2.0}
	p50 := P50(two)
	if math.Abs(p50-1.5) > 1e-9 {
		t.Errorf("P50 of [1, 2] should be 1.5, got %f", p50)
	}
}
