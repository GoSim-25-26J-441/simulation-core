package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseConfigYAML parses a Config from YAML bytes (legacy format).
// For simulation runs, use ParseScenarioYAML instead.
func ParseConfigYAML(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config yaml: %w", err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// ParseConfigYAMLString parses a Config from a YAML string and validates it.
func ParseConfigYAMLString(yamlText string) (*Config, error) {
	return ParseConfigYAML([]byte(yamlText))
}

// UnmarshalScenarioYAML parses YAML into a Scenario without semantic validation.
// Use [ValidateScenario] for full checks, or [ParseScenarioYAML] for parse+validate.
func UnmarshalScenarioYAML(data []byte) (*Scenario, error) {
	var scenario Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return nil, fmt.Errorf("failed to parse scenario yaml: %w", err)
	}
	return &scenario, nil
}

// ParseScenarioYAML parses a Scenario from YAML bytes and validates it.
// This is used for APIs where scenario is provided as payload (not via filesystem).
func ParseScenarioYAML(data []byte) (*Scenario, error) {
	scenario, err := UnmarshalScenarioYAML(data)
	if err != nil {
		return nil, err
	}

	if err := ValidateScenario(scenario); err != nil {
		return nil, fmt.Errorf("invalid scenario: %w", err)
	}

	return scenario, nil
}

// ParseScenarioYAMLString parses a Scenario from a YAML string and validates it.
func ParseScenarioYAMLString(yamlText string) (*Scenario, error) {
	return ParseScenarioYAML([]byte(yamlText))
}

// UnmarshalScenarioYAMLString parses YAML into a Scenario without calling [ValidateScenario].
func UnmarshalScenarioYAMLString(yamlText string) (*Scenario, error) {
	return UnmarshalScenarioYAML([]byte(yamlText))
}

// MarshalScenarioYAML marshals a Scenario to YAML bytes.
func MarshalScenarioYAML(scenario *Scenario) (string, error) {
	if scenario == nil {
		return "", fmt.Errorf("scenario is nil")
	}
	data, err := yaml.Marshal(scenario)
	if err != nil {
		return "", fmt.Errorf("failed to marshal scenario yaml: %w", err)
	}
	return string(data), nil
}
