package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseConfigYAML parses a Config from YAML bytes and validates it.
// This is used for APIs where config is provided as payload (not via filesystem).
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

// ParseScenarioYAML parses a Scenario from YAML bytes and validates it.
// This is used for APIs where scenario is provided as payload (not via filesystem).
func ParseScenarioYAML(data []byte) (*Scenario, error) {
	var scenario Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return nil, fmt.Errorf("failed to parse scenario yaml: %w", err)
	}

	if err := validateScenario(&scenario); err != nil {
		return nil, fmt.Errorf("invalid scenario: %w", err)
	}

	return &scenario, nil
}

// ParseScenarioYAMLString parses a Scenario from a YAML string and validates it.
func ParseScenarioYAMLString(yamlText string) (*Scenario, error) {
	return ParseScenarioYAML([]byte(yamlText))
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
