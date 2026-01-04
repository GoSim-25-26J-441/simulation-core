package workload

import (
	"fmt"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// UserFlow represents a sequence of requests that a user makes
type UserFlow struct {
	ID          string
	Steps       []FlowStep
	StartTime   time.Time
	CurrentStep int
}

// FlowStep represents a single step in a user flow
type FlowStep struct {
	ServiceID   string
	Endpoint    string
	DelayMs     float64 // Delay before this step (relative to previous step)
	Probability float64 // Probability of taking this step (for branching)
}

// UserFlowGenerator generates user flows and schedules their requests
type UserFlowGenerator struct {
	rng *utils.RandSource
}

// NewUserFlowGenerator creates a new user flow generator
func NewUserFlowGenerator(seed int64) *UserFlowGenerator {
	return &UserFlowGenerator{
		rng: utils.NewRandSource(seed),
	}
}

// ScheduleUserFlow schedules a complete user flow starting at the given time
func (g *UserFlowGenerator) ScheduleUserFlow(eng *engine.Engine, flowID string, steps []FlowStep, startTime time.Time) error {
	if len(steps) == 0 {
		return fmt.Errorf("user flow must have at least one step")
	}

	currentTime := startTime

	for i, step := range steps {
		// Check probability for this step (for branching flows)
		// If probability is 0, always skip. If between 0 and 1, use random. If 1, always take.
		if step.Probability <= 0 {
			// Skip this step
			continue
		}
		if step.Probability < 1.0 {
			if !g.rng.BernoulliBool(step.Probability) {
				// Skip this step based on probability
				continue
			}
		}
		// If probability >= 1.0, always take this step

		// Add delay before this step
		if step.DelayMs > 0 {
			currentTime = currentTime.Add(time.Duration(step.DelayMs) * time.Millisecond)
		}

		// Schedule the request arrival
		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, step.ServiceID, map[string]interface{}{
			"service_id":    step.ServiceID,
			"endpoint_path": step.Endpoint,
			"flow_id":       flowID,
			"flow_step":     i,
		})
	}

	return nil
}

// ScheduleUserFlows schedules multiple user flows based on arrival pattern
func (g *UserFlowGenerator) ScheduleUserFlows(eng *engine.Engine, startTime, endTime time.Time, arrival config.ArrivalSpec, flowID string, steps []FlowStep) error {
	// Generate user flow arrivals based on arrival pattern
	currentTime := startTime

	// Generate inter-arrival times based on arrival type
	for currentTime.Before(endTime) {
		var interArrivalSeconds float64

		switch arrival.Type {
		case "poisson", "exponential":
			if arrival.RateRPS <= 0 {
				return fmt.Errorf("rate must be positive for poisson arrival")
			}
			interArrivalSeconds = g.rng.ExpFloat64(arrival.RateRPS)
		case "uniform":
			if arrival.RateRPS <= 0 {
				return fmt.Errorf("rate must be positive for uniform arrival")
			}
			duration := endTime.Sub(startTime).Seconds()
			expectedFlows := arrival.RateRPS * duration
			interArrivalSeconds = duration / expectedFlows
		case "constant":
			if arrival.RateRPS <= 0 {
				return fmt.Errorf("rate must be positive for constant arrival")
			}
			interArrivalSeconds = 1.0 / arrival.RateRPS
		default:
			// Default to poisson
			if arrival.RateRPS <= 0 {
				return fmt.Errorf("rate must be positive")
			}
			interArrivalSeconds = g.rng.ExpFloat64(arrival.RateRPS)
		}

		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0.001
		}

		currentTime = currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		if !currentTime.Before(endTime) {
			break
		}

		// Schedule the user flow
		if err := g.ScheduleUserFlow(eng, fmt.Sprintf("%s-%d", flowID, int(currentTime.Unix())), steps, currentTime); err != nil {
			return err
		}
	}

	return nil
}
