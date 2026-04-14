package config

import "testing"

func TestNormalizeArrivalType(t *testing.T) {
	t.Parallel()
	got, err := NormalizeArrivalType("  BURST ")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "bursty" {
		t.Fatalf("burst alias: got %q want bursty", got)
	}
	if _, err := NormalizeArrivalType("typo"); err == nil {
		t.Fatal("expected error for unknown type")
	}
}
