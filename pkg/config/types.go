package config

import "time"

// Config represents the main simulation configuration
type Config struct {
	LogLevel     string        `yaml:"log_level"`
	Clusters     []Cluster     `yaml:"clusters"`
	ServiceGraph *ServiceGraph `yaml:"service_graph,omitempty"`
	Workload     *Workload     `yaml:"workload,omitempty"`
	Policies     *Policies     `yaml:"policies,omitempty"`
	Optimization *Optimization `yaml:"optimization,omitempty"`
}

// Cluster represents a compute cluster
type Cluster struct {
	Name         string   `yaml:"name"`
	NetworkRTTMs float64  `yaml:"network_rtt_ms"`
	Capacity     Capacity `yaml:"capacity"`
}

// Capacity represents cluster resource capacity
type Capacity struct {
	CPUCores int `yaml:"cpu_cores"`
	MemGB    int `yaml:"mem_gb"`
}

// ServiceGraph represents the service dependency graph
type ServiceGraph struct {
	Nodes []ServiceNode `yaml:"nodes"`
	Edges []ServiceEdge `yaml:"edges"`
}

// ServiceNode represents a service in the graph
type ServiceNode struct {
	Name        string  `yaml:"name"`
	Cluster     string  `yaml:"cluster"`
	CPUCostMs   float64 `yaml:"cpu_cost_ms"`
	Concurrency int     `yaml:"concurrency,omitempty"`
}

// ServiceEdge represents a dependency between services
type ServiceEdge struct {
	From string  `yaml:"from"`
	To   string  `yaml:"to"`
	Mode string  `yaml:"mode"` // sync or async
	P    float64 `yaml:"p"`    // probability
}

// Workload represents the workload configuration
type Workload struct {
	Arrival  string `yaml:"arrival"` // poisson, uniform, etc.
	RateRPS  int    `yaml:"rate_rps"`
	Duration string `yaml:"duration"` // e.g., "60s"
	Warmup   string `yaml:"warmup"`   // e.g., "5s"
}

// Policies represents simulation policies
type Policies struct {
	Autoscaling *AutoscalingPolicy `yaml:"autoscaling,omitempty"`
	Retries     *RetryPolicy       `yaml:"retries,omitempty"`
}

// AutoscalingPolicy represents autoscaling configuration
type AutoscalingPolicy struct {
	Enabled       bool    `yaml:"enabled"`
	TargetCPUUtil float64 `yaml:"target_cpu_util"`
	ScaleStep     int     `yaml:"scale_step"`
}

// RetryPolicy represents retry configuration
type RetryPolicy struct {
	Enabled    bool   `yaml:"enabled"`
	MaxRetries int    `yaml:"max_retries"`
	Backoff    string `yaml:"backoff"` // exponential, linear, constant
	BaseMs     int    `yaml:"base_ms"`
}

// Optimization represents optimization configuration
type Optimization struct {
	Enabled       bool   `yaml:"enabled"`
	Objective     string `yaml:"objective"` // e.g., "p95_latency_ms"
	MaxIterations int    `yaml:"max_iterations"`
}

// GetDuration parses the duration string to time.Duration
func (w *Workload) GetDuration() (time.Duration, error) {
	return time.ParseDuration(w.Duration)
}

// GetWarmup parses the warmup string to time.Duration
func (w *Workload) GetWarmup() (time.Duration, error) {
	return time.ParseDuration(w.Warmup)
}
