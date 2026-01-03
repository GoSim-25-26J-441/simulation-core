package utils

import (
	"testing"
	"time"
)

func TestNewSimTime(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewSimTime(start)

	if st == nil {
		t.Fatal("Expected SimTime to be created")
	}

	if !st.Now().Equal(start) {
		t.Errorf("Expected start time %v, got %v", start, st.Now())
	}
}

func TestSimTimeAdvance(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewSimTime(start)

	st.Advance(5 * time.Second)
	expected := start.Add(5 * time.Second)

	if !st.Now().Equal(expected) {
		t.Errorf("Expected time %v, got %v", expected, st.Now())
	}

	st.Advance(10 * time.Minute)
	expected = expected.Add(10 * time.Minute)

	if !st.Now().Equal(expected) {
		t.Errorf("Expected time %v, got %v", expected, st.Now())
	}
}

func TestSimTimeSet(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewSimTime(start)

	newTime := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	st.Set(newTime)

	if !st.Now().Equal(newTime) {
		t.Errorf("Expected time %v, got %v", newTime, st.Now())
	}
}

func TestSimTimeSince(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewSimTime(start)

	st.Advance(10 * time.Second)
	since := st.Since(start)

	if since != 10*time.Second {
		t.Errorf("Expected 10s since start, got %v", since)
	}
}

func TestSimTimeUntil(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewSimTime(start)

	future := start.Add(20 * time.Second)
	until := st.Until(future)

	if until != 20*time.Second {
		t.Errorf("Expected 20s until future, got %v", until)
	}
}

func TestMsToTime(t *testing.T) {
	tests := []struct {
		ms       float64
		expected time.Duration
	}{
		{1000, 1 * time.Second},
		{500, 500 * time.Millisecond},
		{0, 0},
		{1.5, 1500 * time.Microsecond},
	}

	for _, tt := range tests {
		result := MsToTime(tt.ms)
		if result != tt.expected {
			t.Errorf("MsToTime(%f) = %v, expected %v", tt.ms, result, tt.expected)
		}
	}
}

func TestTimeToMs(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected float64
	}{
		{1 * time.Second, 1000},
		{500 * time.Millisecond, 500},
		{0, 0},
		{1500 * time.Microsecond, 1.5},
	}

	for _, tt := range tests {
		result := TimeToMs(tt.duration)
		if result != tt.expected {
			t.Errorf("TimeToMs(%v) = %f, expected %f", tt.duration, result, tt.expected)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		contains string
	}{
		{500 * time.Nanosecond, "ns"},
		{500 * time.Microsecond, "µs"},
		{500 * time.Millisecond, "ms"},
		{5 * time.Second, "s"},
		{2 * time.Minute, "m"},
	}

	for _, tt := range tests {
		result := FormatDuration(tt.duration)
		if result == "" {
			t.Errorf("FormatDuration(%v) returned empty string", tt.duration)
		}
	}
}

func TestMinTime(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	min := MinTime(t1, t2)
	if !min.Equal(t1) {
		t.Errorf("Expected min to be %v, got %v", t1, min)
	}

	min = MinTime(t2, t1)
	if !min.Equal(t1) {
		t.Errorf("Expected min to be %v, got %v", t1, min)
	}
}

func TestMaxTime(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	max := MaxTime(t1, t2)
	if !max.Equal(t2) {
		t.Errorf("Expected max to be %v, got %v", t2, max)
	}

	max = MaxTime(t2, t1)
	if !max.Equal(t2) {
		t.Errorf("Expected max to be %v, got %v", t2, max)
	}
}

func TestMinDuration(t *testing.T) {
	d1 := 5 * time.Second
	d2 := 10 * time.Second

	min := MinDuration(d1, d2)
	if min != d1 {
		t.Errorf("Expected min to be %v, got %v", d1, min)
	}

	min = MinDuration(d2, d1)
	if min != d1 {
		t.Errorf("Expected min to be %v, got %v", d1, min)
	}
}

func TestMaxDuration(t *testing.T) {
	d1 := 5 * time.Second
	d2 := 10 * time.Second

	max := MaxDuration(d1, d2)
	if max != d2 {
		t.Errorf("Expected max to be %v, got %v", d2, max)
	}

	max = MaxDuration(d2, d1)
	if max != d2 {
		t.Errorf("Expected max to be %v, got %v", d2, max)
	}
}
