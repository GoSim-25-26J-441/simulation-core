package simd

import (
	"fmt"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// RunMetricsOptionsFromResourceManager builds conversion options for ConvertToRunMetrics (idle replicas, broker snapshots).
func RunMetricsOptionsFromResourceManager(rm *resource.Manager) *metrics.RunMetricsOptions {
	if rm == nil {
		return nil
	}
	snapshotAt := rm.LastSimTime()
	if snapshotAt.IsZero() {
		snapshotAt = time.Now()
	}
	bySvc := instanceIDsByServiceFromRM(rm)
	queueSnaps := rm.QueueBrokerHealthSnapshots(snapshotAt)
	topicSnaps := rm.TopicBrokerHealthSnapshots(snapshotAt)
	if len(bySvc) == 0 && len(queueSnaps) == 0 && len(topicSnaps) == 0 {
		return nil
	}
	return &metrics.RunMetricsOptions{
		InstanceIDsByService: bySvc,
		QueueBrokerSnapshots: queueSnaps,
		TopicBrokerSnapshots: topicSnaps,
	}
}

// RunScenarioForMetrics executes a discrete-event simulation for simDuration of simulation time and returns aggregated RunMetrics.
// realTime should be false for deterministic calibration/validation (pre-generated arrivals). seed controls RNG/workload/stochastic paths.
func RunScenarioForMetrics(scenario *config.Scenario, simDuration time.Duration, seed int64, realTime bool) (*models.RunMetrics, error) {
	if scenario == nil {
		return nil, fmt.Errorf("scenario is nil")
	}
	if simDuration <= 0 {
		return nil, fmt.Errorf("simDuration must be positive")
	}
	runID := "scenario-for-metrics"
	eng := engine.NewEngine(runID)
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		return nil, err
	}
	collector := metrics.NewCollector()
	collector.Start()
	policies := policy.NewPolicyManager(nil)
	if scenario.Policies != nil {
		policies = policy.NewPolicyManager(&config.Policies{
			Autoscaling: scenario.Policies.Autoscaling,
			Retries:     scenario.Policies.Retries,
		})
	}
	state, err := newScenarioState(scenario, rm, collector, policies, seed)
	if err != nil {
		collector.Stop()
		return nil, err
	}
	RegisterHandlers(eng, state)
	startTime := eng.GetSimTime()
	endTime := startTime.Add(simDuration)
	state.SetSimEndTime(endTime)
	ScheduleDrainSweepKickoff(eng, startTime)
	ws := NewWorkloadState(runID, eng, endTime, seed)
	if err := ws.Start(scenario, startTime, realTime); err != nil {
		collector.Stop()
		ws.Stop()
		return nil, err
	}
	runErr := eng.Run(simDuration)
	ws.Stop()
	collector.Stop()
	if runErr != nil {
		return nil, runErr
	}
	simDur := eng.GetSimTime().Sub(startTime)
	serviceLabels := make([]map[string]string, 0, len(scenario.Services))
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}
	opts := RunMetricsOptionsFromResourceManager(rm)
	rmOut := metrics.ConvertToRunMetrics(collector, serviceLabels, opts)
	attachHostMetrics(scenario, rm, rmOut, collector)
	applyThroughputFromSimDuration(rmOut, simDur)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		if sm := rmOut.ServiceMetrics[svc.ID]; sm != nil {
			sm.ActiveReplicas = rm.ActiveReplicas(svc.ID)
		}
	}
	return rmOut, nil
}
