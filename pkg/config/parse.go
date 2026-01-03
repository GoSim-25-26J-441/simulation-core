package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ParseConfigYAML parses a configuration from YAML bytes.
func ParseConfigYAML(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// ParseConfigYAMLString parses a configuration from a YAML string.
func ParseConfigYAMLString(yamlText string) (*Config, error) {
	return ParseConfigYAML([]byte(yamlText))
}

// ParseScenarioYAML parses a scenario from YAML bytes.
func ParseScenarioYAML(data []byte) (*Scenario, error) {
	var scenario Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return nil, fmt.Errorf("failed to parse scenario YAML: %w", err)
	}

	if err := validateScenario(&scenario); err != nil {
		return nil, fmt.Errorf("invalid scenario: %w", err)
	}

	return &scenario, nil
}

// ParseScenarioYAMLString parses a scenario from a YAML string.
func ParseScenarioYAMLString(yamlText string) (*Scenario, error) {
	return ParseScenarioYAML([]byte(yamlText))
}
