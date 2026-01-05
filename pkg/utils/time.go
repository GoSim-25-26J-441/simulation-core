package utils

import (
	"sync"
	"time"
)

// SimTime represents simulation time
type SimTime struct {
	mu      sync.RWMutex
	current time.Time
}

// NewSimTime creates a new simulation time starting at the given time
func NewSimTime(start time.Time) *SimTime {
	return &SimTime{current: start}
}

// Now returns the current simulation time
func (st *SimTime) Now() time.Time {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.current
}

// Advance advances the simulation time by the given duration
func (st *SimTime) Advance(d time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.current = st.current.Add(d)
}

// Set sets the simulation time to the given time
func (st *SimTime) Set(t time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.current = t
}

// Since returns the duration since the given time
func (st *SimTime) Since(t time.Time) time.Duration {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.current.Sub(t)
}

// Until returns the duration until the given time
func (st *SimTime) Until(t time.Time) time.Duration {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return t.Sub(st.current)
}

// MsToTime converts milliseconds to time.Duration
func MsToTime(ms float64) time.Duration {
	return time.Duration(ms * float64(time.Millisecond))
}

// TimeToMs converts time.Duration to milliseconds
func TimeToMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// FormatDuration formats a duration in a human-readable way
func FormatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return d.String()
	}
	if d < time.Millisecond {
		return d.Round(time.Microsecond).String()
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	if d < time.Minute {
		return d.Round(10 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// MinTime returns the earlier of two times
func MinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// MaxTime returns the later of two times
func MaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// MinDuration returns the smaller of two durations
func MinDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// MaxDuration returns the larger of two durations
func MaxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
