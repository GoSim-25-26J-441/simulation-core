package config

// Scenario represents a complete simulation scenario
type Scenario struct {
	Hosts    []Host            `yaml:"hosts"`
	Services []Service         `yaml:"services"`
	Workload []WorkloadPattern `yaml:"workload"`
	Policies *Policies         `yaml:"policies,omitempty"`
}

// Host represents a physical host
type Host struct {
	ID    string `yaml:"id"`
	Cores int    `yaml:"cores"`
}

// Service represents a microservice
type Service struct {
	ID        string     `yaml:"id"`
	Replicas  int        `yaml:"replicas"`
	Model     string     `yaml:"model"`               // cpu, mixed, db_latency
	CPUCores  float64    `yaml:"cpu_cores,omitempty"` // CPU cores per instance (optional, defaults to 1.0)
	MemoryMB  float64    `yaml:"memory_mb,omitempty"` // Memory in MB per instance (optional, defaults to 512.0)
	Endpoints []Endpoint `yaml:"endpoints"`
}

// Endpoint represents a service endpoint
type Endpoint struct {
	Path            string           `yaml:"path"`
	MeanCPUMs       float64          `yaml:"mean_cpu_ms"`
	CPUSigmaMs      float64          `yaml:"cpu_sigma_ms"`
	DefaultMemoryMB float64          `yaml:"default_memory_mb,omitempty"` // Default memory usage in MB (optional, defaults to 10.0)
	Downstream      []DownstreamCall `yaml:"downstream"`
	NetLatencyMs    LatencySpec      `yaml:"net_latency_ms"`
}

// DownstreamCall represents a call to a downstream service
type DownstreamCall struct {
	To                    string      `yaml:"to"`
	CallCountMean         float64     `yaml:"call_count_mean,omitempty"`
	CallLatencyMs         LatencySpec `yaml:"call_latency_ms,omitempty"`
	DownstreamFractionCPU float64     `yaml:"downstream_fraction_cpu,omitempty"`
}

// LatencySpec represents latency with mean and standard deviation
type LatencySpec struct {
	Mean  float64 `yaml:"mean"`
	Sigma float64 `yaml:"sigma"`
}

// WorkloadPattern represents a workload entry point
type WorkloadPattern struct {
	From    string      `yaml:"from"` // e.g., "client"
	To      string      `yaml:"to"`   // e.g., "auth:/auth/login"
	Arrival ArrivalSpec `yaml:"arrival"`
}

// ArrivalSpec represents arrival process specification
type ArrivalSpec struct {
	Type                 string  `yaml:"type"`                             // poisson, uniform, normal, bursty, constant
	RateRPS              float64 `yaml:"rate_rps"`                         // Mean/constant rate in requests per second
	StdDevRPS            float64 `yaml:"stddev_rps,omitempty"`             // Standard deviation for normal distribution
	BurstRateRPS         float64 `yaml:"burst_rate_rps,omitempty"`         // Rate during bursts (for bursty type)
	BurstDurationSeconds float64 `yaml:"burst_duration_seconds,omitempty"` // Duration of burst periods
	QuietDurationSeconds float64 `yaml:"quiet_duration_seconds,omitempty"` // Duration of quiet periods between bursts
}
