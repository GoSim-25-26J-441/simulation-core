package utils

import (
	"math"
	"testing"
)

func TestNewRandSource(t *testing.T) {
	// Test with seed
	rng1 := NewRandSource(12345)
	if rng1 == nil {
		t.Fatal("Expected RandSource to be created")
	}

	// Test with zero seed (should use current time)
	rng2 := NewRandSource(0)
	if rng2 == nil {
		t.Fatal("Expected RandSource to be created with zero seed")
	}
}

func TestRandSourceFloat64(t *testing.T) {
	rng := NewRandSource(12345)

	for i := 0; i < 100; i++ {
		val := rng.Float64()
		if val < 0 || val >= 1.0 {
			t.Errorf("Float64() returned value outside [0, 1): %f", val)
		}
	}
}

func TestRandSourceIntn(t *testing.T) {
	rng := NewRandSource(12345)

	for i := 0; i < 100; i++ {
		val := rng.Intn(10)
		if val < 0 || val >= 10 {
			t.Errorf("Intn(10) returned value outside [0, 10): %d", val)
		}
	}
}

func TestRandSourceExpFloat64(t *testing.T) {
	rng := NewRandSource(12345)
	lambda := 2.0

	// Generate samples
	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = rng.ExpFloat64(lambda)
		if samples[i] < 0 {
			t.Errorf("ExpFloat64() returned negative value: %f", samples[i])
		}
	}

	// Check mean is approximately 1/lambda
	mean := Mean(samples)
	expectedMean := 1.0 / lambda
	tolerance := 0.1

	if math.Abs(mean-expectedMean) > tolerance {
		t.Errorf("ExpFloat64 mean %f not close to expected %f", mean, expectedMean)
	}
}

func TestRandSourceNormFloat64(t *testing.T) {
	rng := NewRandSource(12345)
	meanVal := 10.0
	stddev := 2.0

	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = rng.NormFloat64(meanVal, stddev)
	}

	// Check mean
	actualMean := Mean(samples)
	tolerance := 0.5
	if math.Abs(actualMean-meanVal) > tolerance {
		t.Errorf("NormFloat64 mean %f not close to expected %f", actualMean, meanVal)
	}

	// Check stddev
	actualStddev := StdDev(samples)
	if math.Abs(actualStddev-stddev) > tolerance {
		t.Errorf("NormFloat64 stddev %f not close to expected %f", actualStddev, stddev)
	}
}

func TestRandSourcePoissonInt(t *testing.T) {
	rng := NewRandSource(12345)

	// Test with lambda = 0
	val := rng.PoissonInt(0)
	if val != 0 {
		t.Errorf("PoissonInt(0) should return 0, got %d", val)
	}

	// Test with positive lambda
	lambda := 5.0
	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = float64(rng.PoissonInt(lambda))
		if samples[i] < 0 {
			t.Errorf("PoissonInt() returned negative value: %f", samples[i])
		}
	}

	// Check mean is approximately lambda
	mean := Mean(samples)
	tolerance := 1.0
	if math.Abs(mean-lambda) > tolerance {
		t.Errorf("PoissonInt mean %f not close to expected %f", mean, lambda)
	}
}

func TestRandSourceBernoulliFloat64(t *testing.T) {
	rng := NewRandSource(12345)
	p := 0.6

	ones := 0
	trials := 1000
	for i := 0; i < trials; i++ {
		val := rng.BernoulliFloat64(p)
		if val != 0.0 && val != 1.0 {
			t.Errorf("BernoulliFloat64() should return 0.0 or 1.0, got %f", val)
		}
		if val == 1.0 {
			ones++
		}
	}

	// Check proportion is approximately p
	proportion := float64(ones) / float64(trials)
	tolerance := 0.1
	if math.Abs(proportion-p) > tolerance {
		t.Errorf("Bernoulli proportion %f not close to expected %f", proportion, p)
	}
}

func TestRandSourceBernoulliBool(t *testing.T) {
	rng := NewRandSource(12345)
	p := 0.7

	trueCount := 0
	trials := 1000
	for i := 0; i < trials; i++ {
		if rng.BernoulliBool(p) {
			trueCount++
		}
	}

	// Check proportion is approximately p
	proportion := float64(trueCount) / float64(trials)
	tolerance := 0.1
	if math.Abs(proportion-p) > tolerance {
		t.Errorf("Bernoulli bool proportion %f not close to expected %f", proportion, p)
	}
}

func TestRandSourceUniformFloat64(t *testing.T) {
	rng := NewRandSource(12345)
	min := 5.0
	max := 15.0

	for i := 0; i < 100; i++ {
		val := rng.UniformFloat64(min, max)
		if val < min || val >= max {
			t.Errorf("UniformFloat64(%f, %f) returned value outside range: %f", min, max, val)
		}
	}
}

func TestGlobalRandFunctions(t *testing.T) {
	SetSeed(12345)

	// Test Float64
	val := Float64()
	if val < 0 || val >= 1.0 {
		t.Errorf("Float64() returned value outside [0, 1): %f", val)
	}

	// Test Intn
	n := Intn(100)
	if n < 0 || n >= 100 {
		t.Errorf("Intn(100) returned value outside [0, 100): %d", n)
	}

	// Test ExpFloat64
	exp := ExpFloat64(2.0)
	if exp < 0 {
		t.Errorf("ExpFloat64() returned negative value: %f", exp)
	}

	// Test NormFloat64
	_ = NormFloat64(10, 2)
	// Just ensure it doesn't crash

	// Test PoissonInt
	poisson := PoissonInt(5.0)
	if poisson < 0 {
		t.Errorf("PoissonInt() returned negative value: %d", poisson)
	}

	// Test BernoulliFloat64
	bern := BernoulliFloat64(0.5)
	if bern != 0.0 && bern != 1.0 {
		t.Errorf("BernoulliFloat64() returned invalid value: %f", bern)
	}

	// Test BernoulliBool
	_ = BernoulliBool(0.5)

	// Test UniformFloat64
	uniform := UniformFloat64(0, 10)
	if uniform < 0 || uniform >= 10 {
		t.Errorf("UniformFloat64(0, 10) returned value outside range: %f", uniform)
	}
}

func TestDeterministicBehavior(t *testing.T) {
	// Same seed should produce same sequence
	rng1 := NewRandSource(999)
	rng2 := NewRandSource(999)

	for i := 0; i < 10; i++ {
		val1 := rng1.Float64()
		val2 := rng2.Float64()
		if val1 != val2 {
			t.Errorf("Same seed should produce same sequence: %f != %f", val1, val2)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	// Test that RandSource is thread-safe
	rng := NewRandSource(12345)
	const numGoroutines = 100
	const numIterations = 100

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numIterations; j++ {
				_ = rng.Float64()
				_ = rng.Intn(100)
				_ = rng.ExpFloat64(2.0)
				_ = rng.NormFloat64(10, 2)
				_ = rng.PoissonInt(5.0)
				_ = rng.BernoulliFloat64(0.5)
				_ = rng.BernoulliBool(0.5)
				_ = rng.UniformFloat64(0, 10)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestConcurrentGlobalAccess(t *testing.T) {
	// Test that global functions are thread-safe
	SetSeed(12345)
	const numGoroutines = 100
	const numIterations = 100

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < numIterations; j++ {
				_ = Float64()
				_ = Intn(100)
				_ = ExpFloat64(2.0)
				_ = NormFloat64(10, 2)
				_ = PoissonInt(5.0)
				_ = BernoulliFloat64(0.5)
				_ = BernoulliBool(0.5)
				_ = UniformFloat64(0, 10)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}
