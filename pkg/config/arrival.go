package config

import (
	"fmt"
	"strings"
)

// NormalizeArrivalType returns the canonical arrival type string for [Scenario] workload
// validation and execution. It accepts alias "burst" as "bursty" (case-insensitive).
func NormalizeArrivalType(t string) (string, error) {
	t = strings.TrimSpace(strings.ToLower(t))
	if t == "" {
		return "", fmt.Errorf("arrival type cannot be empty")
	}
	if t == "burst" {
		t = "bursty"
	}
	switch t {
	case "poisson", "exponential", "uniform", "normal", "gaussian", "bursty", "constant":
		return t, nil
	default:
		return "", fmt.Errorf("invalid arrival type %q (supported: poisson, exponential, uniform, normal, gaussian, bursty, constant; alias burst -> bursty)", t)
	}
}
