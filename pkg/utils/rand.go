package utils

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// RandSource is a thread-safe random number generator
type RandSource struct {
	rng *rand.Rand
	mu  sync.Mutex
}

// NewRandSource creates a new random source with the given seed
func NewRandSource(seed int64) *RandSource {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &RandSource{
		rng: rand.New(rand.NewSource(seed)),
	}
}

// Float64 returns a random float64 in [0.0, 1.0)
func (r *RandSource) Float64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.Float64()
}

// Intn returns a random int in [0, n)
func (r *RandSource) Intn(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.Intn(n)
}

// ExpFloat64 returns an exponentially distributed random number with rate lambda
func (r *RandSource) ExpFloat64(lambda float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.ExpFloat64() / lambda
}

// NormFloat64 returns a normally distributed random number with mean and stddev
func (r *RandSource) NormFloat64(mean, stddev float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.NormFloat64()*stddev + mean
}

// PoissonInt returns a Poisson-distributed random integer with rate lambda
func (r *RandSource) PoissonInt(lambda float64) int {
	if lambda <= 0 {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Use Knuth's algorithm for Poisson distribution
	L := math.Exp(-lambda)
	k := 0
	p := 1.0

	for p > L {
		k++
		p *= r.rng.Float64()
	}

	return k - 1
}

// BernoulliFloat64 returns 1.0 with probability p, 0.0 otherwise
func (r *RandSource) BernoulliFloat64(p float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rng.Float64() < p {
		return 1.0
	}
	return 0.0
}

// BernoulliBool returns true with probability p, false otherwise
func (r *RandSource) BernoulliBool(p float64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rng.Float64() < p
}

// UniformFloat64 returns a uniformly distributed random number in [min, max)
func (r *RandSource) UniformFloat64(min, max float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return min + r.rng.Float64()*(max-min)
}

// Global default random source
var defaultRand = NewRandSource(0)
var defaultRandMu sync.Mutex

// SetSeed sets the seed for the default random source
func SetSeed(seed int64) {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	defaultRand = NewRandSource(seed)
}

// Float64 returns a random float64 from the default source
func Float64() float64 {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return defaultRand.rng.Float64()
}

// Intn returns a random int from the default source
func Intn(n int) int {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return defaultRand.rng.Intn(n)
}

// ExpFloat64 returns an exponentially distributed random number from the default source
func ExpFloat64(lambda float64) float64 {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return defaultRand.rng.ExpFloat64() / lambda
}

// NormFloat64 returns a normally distributed random number from the default source
func NormFloat64(mean, stddev float64) float64 {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return defaultRand.rng.NormFloat64()*stddev + mean
}

// PoissonInt returns a Poisson-distributed random integer from the default source
func PoissonInt(lambda float64) int {
	if lambda <= 0 {
		return 0
	}

	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()

	// Access the internal rng directly to avoid double-locking
	// Use Knuth's algorithm for Poisson distribution
	L := math.Exp(-lambda)
	k := 0
	p := 1.0

	for p > L {
		k++
		p *= defaultRand.rng.Float64()
	}

	return k - 1
}

// BernoulliFloat64 returns 1.0 with probability p from the default source
func BernoulliFloat64(p float64) float64 {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	if defaultRand.rng.Float64() < p {
		return 1.0
	}
	return 0.0
}

// BernoulliBool returns true with probability p from the default source
func BernoulliBool(p float64) bool {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return defaultRand.rng.Float64() < p
}

// UniformFloat64 returns a uniformly distributed random number from the default source
func UniformFloat64(min, max float64) float64 {
	defaultRandMu.Lock()
	defer defaultRandMu.Unlock()
	// Access the internal rng directly to avoid double-locking
	return min + defaultRand.rng.Float64()*(max-min)
}
