package workload

import (
	"fmt"
	"math"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// Generator generates workload arrival events based on arrival specifications
type Generator struct {
	rng *utils.RandSource
}

// NewGenerator creates a new workload generator
func NewGenerator(seed int64) *Generator {
	return &Generator{
		rng: utils.NewRandSource(seed),
	}
}

// ScheduleArrivals schedules arrival events based on arrival specification
func (g *Generator) ScheduleArrivals(eng *engine.Engine, startTime, endTime time.Time, arrival config.ArrivalSpec, serviceID, endpointPath string) error {
	switch arrival.Type {
	case "poisson", "exponential":
		return g.schedulePoissonArrivals(eng, startTime, endTime, arrival.RateRPS, serviceID, endpointPath)
	case "uniform":
		return g.scheduleUniformArrivals(eng, startTime, endTime, arrival.RateRPS, serviceID, endpointPath)
	case "normal", "gaussian":
		meanRate := arrival.RateRPS
		stddev := arrival.RateRPS * 0.1 // Default 10% stddev if not specified
		if arrival.StdDevRPS > 0 {
			stddev = arrival.StdDevRPS
		}
		return g.scheduleNormalArrivals(eng, startTime, endTime, meanRate, stddev, serviceID, endpointPath)
	case "bursty":
		return g.scheduleBurstyArrivals(eng, startTime, endTime, arrival, serviceID, endpointPath)
	case "constant":
		return g.scheduleConstantArrivals(eng, startTime, endTime, arrival.RateRPS, serviceID, endpointPath)
	default:
		// Default to poisson
		return g.schedulePoissonArrivals(eng, startTime, endTime, arrival.RateRPS, serviceID, endpointPath)
	}
}

// schedulePoissonArrivals schedules arrivals using Poisson process (exponential inter-arrival times)
func (g *Generator) schedulePoissonArrivals(eng *engine.Engine, startTime, endTime time.Time, rateRPS float64, serviceID, endpointPath string) error {
	if rateRPS <= 0 {
		return fmt.Errorf("rate must be positive, got %f", rateRPS)
	}

	currentTime := startTime
	lambda := rateRPS // rate parameter for exponential distribution

	for currentTime.Before(endTime) {
		// Generate next inter-arrival time (exponential with rate lambda)
		interArrivalSeconds := g.rng.ExpFloat64(lambda)
		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0
		}
		currentTime = currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		if !currentTime.Before(endTime) {
			break
		}

		// Schedule arrival event
		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	return nil
}

// scheduleUniformArrivals schedules arrivals uniformly over the duration
func (g *Generator) scheduleUniformArrivals(eng *engine.Engine, startTime, endTime time.Time, rateRPS float64, serviceID, endpointPath string) error {
	if rateRPS <= 0 {
		return fmt.Errorf("rate must be positive, got %f", rateRPS)
	}

	duration := endTime.Sub(startTime)
	totalSeconds := duration.Seconds()
	expectedArrivals := int64(math.Round(rateRPS * totalSeconds))

	// Distribute arrivals uniformly
	for i := int64(0); i < expectedArrivals; i++ {
		// Uniform distribution over duration
		offsetSeconds := g.rng.UniformFloat64(0, totalSeconds)
		arrivalTime := startTime.Add(time.Duration(offsetSeconds * float64(time.Second)))

		if arrivalTime.After(endTime) {
			continue
		}

		eng.ScheduleAt(engine.EventTypeRequestArrival, arrivalTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	return nil
}

// scheduleNormalArrivals schedules arrivals with normally distributed inter-arrival times
func (g *Generator) scheduleNormalArrivals(eng *engine.Engine, startTime, endTime time.Time, meanRateRPS, stddevRPS float64, serviceID, endpointPath string) error {
	if meanRateRPS <= 0 {
		return fmt.Errorf("mean rate must be positive, got %f", meanRateRPS)
	}

	currentTime := startTime
	meanInterArrivalSeconds := 1.0 / meanRateRPS

	for currentTime.Before(endTime) {
		// Generate inter-arrival time using normal distribution
		// Clamp to ensure positive values
		interArrivalSeconds := g.rng.NormFloat64(meanInterArrivalSeconds, stddevRPS/meanRateRPS)
		if interArrivalSeconds < 0.001 { // Minimum 1ms
			interArrivalSeconds = 0.001
		}
		currentTime = currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		if !currentTime.Before(endTime) {
			break
		}

		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	return nil
}

// scheduleConstantArrivals schedules arrivals at a constant rate
func (g *Generator) scheduleConstantArrivals(eng *engine.Engine, startTime, endTime time.Time, rateRPS float64, serviceID, endpointPath string) error {
	if rateRPS <= 0 {
		return fmt.Errorf("rate must be positive, got %f", rateRPS)
	}

	intervalSeconds := 1.0 / rateRPS
	currentTime := startTime

	for currentTime.Before(endTime) {
		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})

		currentTime = currentTime.Add(time.Duration(intervalSeconds * float64(time.Second)))
	}

	return nil
}

// scheduleBurstyArrivals schedules arrivals with burstiness (on/off periods)
func (g *Generator) scheduleBurstyArrivals(eng *engine.Engine, startTime, endTime time.Time, arrival config.ArrivalSpec, serviceID, endpointPath string) error {
	// Bursty workload parameters
	baseRate := arrival.RateRPS
	if baseRate <= 0 {
		baseRate = 10.0 // Default base rate
	}

	// Burst parameters
	burstRate := baseRate * 5.0 // 5x during bursts
	if arrival.BurstRateRPS > 0 {
		burstRate = arrival.BurstRateRPS
	}

	burstDuration := 5.0 // seconds
	if arrival.BurstDurationSeconds > 0 {
		burstDuration = arrival.BurstDurationSeconds
	}

	quietDuration := 10.0 // seconds
	if arrival.QuietDurationSeconds > 0 {
		quietDuration = arrival.QuietDurationSeconds
	}

	cycleDuration := burstDuration + quietDuration
	currentTime := startTime
	maxIterations := 100000 // Safety limit to prevent infinite loops
	iteration := 0

	for currentTime.Before(endTime) && iteration < maxIterations {
		iteration++

		// Calculate position in burst/quiet cycle
		timeSinceStart := currentTime.Sub(startTime).Seconds()
		cycleNumber := int(timeSinceStart / cycleDuration)
		timeInCycle := timeSinceStart - float64(cycleNumber)*cycleDuration

		// Determine if we're in a burst or quiet period
		inBurst := timeInCycle < burstDuration

		if !inBurst {
			// In quiet period, skip to next burst
			nextBurstStart := startTime.Add(time.Duration((float64(cycleNumber) + 1) * cycleDuration * float64(time.Second)))
			if nextBurstStart.After(endTime) {
				break
			}
			currentTime = nextBurstStart
			continue
		}

		// In burst period, schedule arrivals
		interArrivalSeconds := g.rng.ExpFloat64(burstRate)
		if interArrivalSeconds < 0.001 {
			interArrivalSeconds = 0.001 // Minimum 1ms
		}
		currentTime = currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		// Check if we've exceeded the burst period
		timeSinceStart = currentTime.Sub(startTime).Seconds()
		cycleNumber = int(timeSinceStart / cycleDuration)
		timeInCycle = timeSinceStart - float64(cycleNumber)*cycleDuration

		if timeInCycle >= burstDuration {
			// End of burst, move to next cycle
			nextBurstStart := startTime.Add(time.Duration((float64(cycleNumber) + 1) * cycleDuration * float64(time.Second)))
			if nextBurstStart.After(endTime) {
				break
			}
			currentTime = nextBurstStart
			continue
		}

		if !currentTime.Before(endTime) {
			break
		}

		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	if iteration >= maxIterations {
		return fmt.Errorf("bursty arrivals exceeded maximum iterations (%d), possible infinite loop", maxIterations)
	}

	return nil
}
