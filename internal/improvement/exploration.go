package improvement

import (
	"math"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ParameterExplorer defines strategies for exploring the parameter space
type ParameterExplorer interface {
	// GenerateNeighbors creates neighboring configurations by adjusting parameters
	GenerateNeighbors(base *config.Scenario, stepSize float64) []*config.Scenario
	// Name returns the name of the exploration strategy
	Name() string
}

// DefaultExplorer implements a comprehensive parameter space exploration strategy
type DefaultExplorer struct {
	maxReplicas      int
	minReplicas      int
	replicaStep      int
	policyStepSize   float64
	workloadStepSize float64
	resourceStepSize float64
}

// NewDefaultExplorer creates a new default parameter explorer
func NewDefaultExplorer() *DefaultExplorer {
	return &DefaultExplorer{
		maxReplicas:      20,
		minReplicas:      1,
		replicaStep:      1,
		policyStepSize:   0.05,
		workloadStepSize: 0.1,
		resourceStepSize: 0.1,
	}
}

// WithMaxReplicas sets the maximum number of replicas to explore
func (e *DefaultExplorer) WithMaxReplicas(max int) *DefaultExplorer {
	e.maxReplicas = max
	return e
}

// WithMinReplicas sets the minimum number of replicas to explore
func (e *DefaultExplorer) WithMinReplicas(min int) *DefaultExplorer {
	e.minReplicas = min
	return e
}

// WithReplicaStep sets the step size for replica adjustments
func (e *DefaultExplorer) WithReplicaStep(step int) *DefaultExplorer {
	e.replicaStep = step
	return e
}

// WithPolicyStepSize sets the step size for policy parameter adjustments
func (e *DefaultExplorer) WithPolicyStepSize(step float64) *DefaultExplorer {
	e.policyStepSize = step
	return e
}

func (e *DefaultExplorer) Name() string {
	return "default"
}

// GenerateNeighbors generates neighboring configurations by exploring various parameters
func (e *DefaultExplorer) GenerateNeighbors(base *config.Scenario, stepSize float64) []*config.Scenario {
	neighbors := make([]*config.Scenario, 0)

	// 1. Explore service replica counts
	neighbors = append(neighbors, e.exploreReplicas(base)...)

	// 2. Explore resource allocations (CPU, memory)
	neighbors = append(neighbors, e.exploreResources(base)...)

	// 3. Explore policy parameters
	neighbors = append(neighbors, e.explorePolicies(base)...)

	// 4. Explore workload parameters (arrival rates)
	neighbors = append(neighbors, e.exploreWorkload(base)...)

	return neighbors
}

// exploreReplicas generates neighbors by adjusting service replica counts
func (e *DefaultExplorer) exploreReplicas(base *config.Scenario) []*config.Scenario {
	neighbors := make([]*config.Scenario, 0)

	for i := range base.Services {
		// Try increasing replicas
		if base.Services[i].Replicas < e.maxReplicas {
			neighbor := cloneScenario(base)
			newReplicas := base.Services[i].Replicas + e.replicaStep
			if newReplicas <= e.maxReplicas {
				neighbor.Services[i].Replicas = newReplicas
				neighbors = append(neighbors, neighbor)
			}
		}

		// Try decreasing replicas
		if base.Services[i].Replicas > e.minReplicas {
			neighbor := cloneScenario(base)
			newReplicas := base.Services[i].Replicas - e.replicaStep
			if newReplicas >= e.minReplicas {
				neighbor.Services[i].Replicas = newReplicas
				neighbors = append(neighbors, neighbor)
			}
		}
	}

	return neighbors
}

// exploreResources generates neighbors by adjusting CPU and memory allocations
func (e *DefaultExplorer) exploreResources(base *config.Scenario) []*config.Scenario {
	neighbors := make([]*config.Scenario, 0)

	for i := range base.Services {
		// Explore CPU cores
		currentCPU := base.Services[i].CPUCores
		if currentCPU == 0 {
			currentCPU = 1.0 // Default
		}

		// Increase CPU
		neighbor := cloneScenario(base)
		neighbor.Services[i].CPUCores = currentCPU * (1.0 + e.resourceStepSize)
		neighbors = append(neighbors, neighbor)

		// Decrease CPU (but keep at least 0.1)
		if currentCPU > 0.1 {
			neighbor2 := cloneScenario(base)
			neighbor2.Services[i].CPUCores = math.Max(0.1, currentCPU*(1.0-e.resourceStepSize))
			neighbors = append(neighbors, neighbor2)
		}

		// Explore memory
		currentMemory := base.Services[i].MemoryMB
		if currentMemory == 0 {
			currentMemory = 512.0 // Default
		}

		// Increase memory
		neighbor3 := cloneScenario(base)
		neighbor3.Services[i].MemoryMB = currentMemory * (1.0 + e.resourceStepSize)
		neighbors = append(neighbors, neighbor3)

		// Decrease memory (but keep at least 64MB)
		if currentMemory > 64.0 {
			neighbor4 := cloneScenario(base)
			neighbor4.Services[i].MemoryMB = math.Max(64.0, currentMemory*(1.0-e.resourceStepSize))
			neighbors = append(neighbors, neighbor4)
		}
	}

	return neighbors
}

// explorePolicies generates neighbors by adjusting policy parameters
func (e *DefaultExplorer) explorePolicies(base *config.Scenario) []*config.Scenario {
	neighbors := make([]*config.Scenario, 0)

	if base.Policies == nil {
		return neighbors
	}

	// Explore autoscaling policy
	if base.Policies.Autoscaling != nil && base.Policies.Autoscaling.Enabled {
		// Adjust target CPU utilization
		targetCPU := base.Policies.Autoscaling.TargetCPUUtil
		if targetCPU > 0.1 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Autoscaling.TargetCPUUtil = math.Max(0.1, targetCPU-e.policyStepSize)
			neighbors = append(neighbors, neighbor)
		}
		if targetCPU < 0.9 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Autoscaling.TargetCPUUtil = math.Min(0.9, targetCPU+e.policyStepSize)
			neighbors = append(neighbors, neighbor)
		}

		// Adjust scale step
		scaleStep := base.Policies.Autoscaling.ScaleStep
		if scaleStep > 1 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Autoscaling.ScaleStep = scaleStep - 1
			neighbors = append(neighbors, neighbor)
		}
		if scaleStep < 5 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Autoscaling.ScaleStep = scaleStep + 1
			neighbors = append(neighbors, neighbor)
		}
	}

	// Explore retry policy
	if base.Policies.Retries != nil && base.Policies.Retries.Enabled {
		// Adjust max retries
		maxRetries := base.Policies.Retries.MaxRetries
		if maxRetries > 0 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Retries.MaxRetries = maxRetries - 1
			neighbors = append(neighbors, neighbor)
		}
		if maxRetries < 10 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Retries.MaxRetries = maxRetries + 1
			neighbors = append(neighbors, neighbor)
		}

		// Adjust base backoff time
		baseMs := base.Policies.Retries.BaseMs
		if baseMs > 1 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Retries.BaseMs = baseMs - 1
			neighbors = append(neighbors, neighbor)
		}
		if baseMs < 1000 {
			neighbor := cloneScenario(base)
			neighbor.Policies.Retries.BaseMs = baseMs + 1
			neighbors = append(neighbors, neighbor)
		}
	}

	return neighbors
}

// exploreWorkload generates neighbors by adjusting workload arrival rates
func (e *DefaultExplorer) exploreWorkload(base *config.Scenario) []*config.Scenario {
	neighbors := make([]*config.Scenario, 0)

	for i := range base.Workload {
		arrival := &base.Workload[i].Arrival
		currentRate := arrival.RateRPS

		// Increase arrival rate
		neighbor := cloneScenario(base)
		neighbor.Workload[i].Arrival.RateRPS = currentRate * (1.0 + e.workloadStepSize)
		neighbors = append(neighbors, neighbor)

		// Decrease arrival rate (but keep at least 0.1 RPS)
		if currentRate > 0.1 {
			neighbor2 := cloneScenario(base)
			neighbor2.Workload[i].Arrival.RateRPS = math.Max(0.1, currentRate*(1.0-e.workloadStepSize))
			neighbors = append(neighbors, neighbor2)
		}

		// For normal distribution, also adjust stddev
		if arrival.Type == "normal" && arrival.StdDevRPS > 0 {
			// Increase stddev
			neighbor3 := cloneScenario(base)
			neighbor3.Workload[i].Arrival.StdDevRPS = arrival.StdDevRPS * (1.0 + e.workloadStepSize)
			neighbors = append(neighbors, neighbor3)

			// Decrease stddev (but keep at least 0.01)
			if arrival.StdDevRPS > 0.01 {
				neighbor4 := cloneScenario(base)
				neighbor4.Workload[i].Arrival.StdDevRPS = math.Max(0.01, arrival.StdDevRPS*(1.0-e.workloadStepSize))
				neighbors = append(neighbors, neighbor4)
			}
		}

		// For bursty distribution, adjust burst parameters
		if arrival.Type == "bursty" {
			// Adjust burst rate
			if arrival.BurstRateRPS > 0 {
				neighbor5 := cloneScenario(base)
				neighbor5.Workload[i].Arrival.BurstRateRPS = arrival.BurstRateRPS * (1.0 + e.workloadStepSize)
				neighbors = append(neighbors, neighbor5)

				if arrival.BurstRateRPS > 0.1 {
					neighbor6 := cloneScenario(base)
					neighbor6.Workload[i].Arrival.BurstRateRPS = math.Max(0.1, arrival.BurstRateRPS*(1.0-e.workloadStepSize))
					neighbors = append(neighbors, neighbor6)
				}
			}
		}
	}

	return neighbors
}

// ConservativeExplorer implements a conservative exploration strategy
// that makes smaller, more cautious adjustments
type ConservativeExplorer struct {
	*DefaultExplorer
}

// NewConservativeExplorer creates a new conservative explorer
func NewConservativeExplorer() *ConservativeExplorer {
	base := NewDefaultExplorer()
	base.policyStepSize = 0.02
	base.workloadStepSize = 0.05
	base.resourceStepSize = 0.05
	base.replicaStep = 1
	return &ConservativeExplorer{DefaultExplorer: base}
}

func (e *ConservativeExplorer) Name() string {
	return "conservative"
}

// AggressiveExplorer implements an aggressive exploration strategy
// that makes larger adjustments to explore the space more quickly
type AggressiveExplorer struct {
	*DefaultExplorer
}

// NewAggressiveExplorer creates a new aggressive explorer
func NewAggressiveExplorer() *AggressiveExplorer {
	base := NewDefaultExplorer()
	base.policyStepSize = 0.1
	base.workloadStepSize = 0.2
	base.resourceStepSize = 0.2
	base.replicaStep = 2
	return &AggressiveExplorer{DefaultExplorer: base}
}

func (e *AggressiveExplorer) Name() string {
	return "aggressive"
}
