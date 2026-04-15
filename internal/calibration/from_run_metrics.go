package calibration

import (
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// FromRunMetrics builds ObservedMetrics from a completed simulator RunMetrics and window duration.
// Used for golden scenarios and calibration recovery tests.
func FromRunMetrics(rm *models.RunMetrics, window time.Duration) *ObservedMetrics {
	if rm == nil {
		return &ObservedMetrics{}
	}
	ingTP := rm.IngressThroughputRPS
	sec := window.Seconds()
	if sec > 0 && ingTP < 1e-9 {
		if rm.IngressRequests > 0 {
			ingTP = float64(rm.IngressRequests) / sec
		} else if rm.TotalRequests > 0 {
			ingTP = float64(rm.TotalRequests) / sec
		}
	}
	obs := &ObservedMetrics{
		Window: ObservationWindow{
			Duration: window,
			Source:   "simulator_run_metrics",
		},
		Global: GlobalObservation{
			RootLatencyP50Ms:      F64(rm.LatencyP50),
			RootLatencyP95Ms:      F64(rm.LatencyP95),
			RootLatencyP99Ms:      F64(rm.LatencyP99),
			RootLatencyMeanMs:     F64(rm.LatencyMean),
			IngressThroughputRPS:  F64(ingTP),
			IngressErrorRate:      F64(rm.IngressErrorRate),
			TotalRequests:         I64(rm.TotalRequests),
			IngressRequests:       I64(rm.IngressRequests),
			FailedRequests:        I64(rm.FailedRequests),
			RetryAttempts:         I64(rm.RetryAttempts),
			TimeoutErrors:         I64(rm.TimeoutErrors),
			IngressFailedRequests: I64(rm.IngressFailedRequests),
		},
	}
	for name, sm := range rm.ServiceMetrics {
		if sm == nil {
			continue
		}
		obs.Services = append(obs.Services, ServiceObservation{
			ServiceID:         name,
			CPUUtilization:    F64(sm.CPUUtilization),
			MemoryUtilization: F64(sm.MemoryUtilization),
		})
	}

	if len(rm.EndpointRequestStats) > 0 {
		for i := range rm.EndpointRequestStats {
			es := &rm.EndpointRequestStats[i]
			obs.Endpoints = append(obs.Endpoints, endpointObservationFromStats(es, window))
		}
	} else {
		for name, sm := range rm.ServiceMetrics {
			if sm == nil {
				continue
			}
			obs.Endpoints = append(obs.Endpoints, EndpointObservation{
				ServiceID:               name,
				EndpointPath:            "*", // aggregate per service when reconstructing from RunMetrics only
				ThroughputRPS:           F64(float64(sm.RequestCount) / maxSec(window.Seconds())),
				LatencyP50Ms:            F64(sm.LatencyP50),
				LatencyP95Ms:            F64(sm.LatencyP95),
				LatencyP99Ms:            F64(sm.LatencyP99),
				LatencyMeanMs:           F64(sm.LatencyMean),
				ProcessingLatencyP50Ms:  F64(sm.ProcessingLatencyP50Ms),
				ProcessingLatencyP95Ms:  F64(sm.ProcessingLatencyP95Ms),
				ProcessingLatencyP99Ms:  F64(sm.ProcessingLatencyP99Ms),
				ProcessingLatencyMeanMs: F64(sm.ProcessingLatencyMeanMs),
				QueueWaitP50Ms:          F64(sm.QueueWaitP50Ms),
				QueueWaitP95Ms:          F64(sm.QueueWaitP95Ms),
				QueueWaitP99Ms:          F64(sm.QueueWaitP99Ms),
				QueueWaitMeanMs:         F64(sm.QueueWaitMeanMs),
				RequestCount:            I64(sm.RequestCount),
				ErrorCount:              I64(sm.ErrorCount),
			})
		}
	}
	return obs
}

func endpointObservationFromStats(es *models.EndpointRequestStats, window time.Duration) EndpointObservation {
	if es == nil {
		return EndpointObservation{}
	}
	eo := EndpointObservation{
		ServiceID:    es.ServiceName,
		EndpointPath: es.EndpointPath,
		RequestCount: I64(es.RequestCount),
		ErrorCount:   I64(es.ErrorCount),
	}
	sec := maxSec(window.Seconds())
	if es.RequestCount > 0 && window > 0 {
		eo.ThroughputRPS = F64(float64(es.RequestCount) / sec)
	}
	if es.LatencyP50Ms != nil {
		eo.LatencyP50Ms = F64(*es.LatencyP50Ms)
	}
	if es.LatencyP95Ms != nil {
		eo.LatencyP95Ms = F64(*es.LatencyP95Ms)
	}
	if es.LatencyP99Ms != nil {
		eo.LatencyP99Ms = F64(*es.LatencyP99Ms)
	}
	if es.LatencyMeanMs != nil {
		eo.LatencyMeanMs = F64(*es.LatencyMeanMs)
	}
	if es.QueueWaitP50Ms != nil {
		eo.QueueWaitP50Ms = F64(*es.QueueWaitP50Ms)
	}
	if es.QueueWaitP95Ms != nil {
		eo.QueueWaitP95Ms = F64(*es.QueueWaitP95Ms)
	}
	if es.QueueWaitP99Ms != nil {
		eo.QueueWaitP99Ms = F64(*es.QueueWaitP99Ms)
	}
	if es.QueueWaitMeanMs != nil {
		eo.QueueWaitMeanMs = F64(*es.QueueWaitMeanMs)
	}
	if es.ProcessingLatencyP50Ms != nil {
		eo.ProcessingLatencyP50Ms = F64(*es.ProcessingLatencyP50Ms)
	}
	if es.ProcessingLatencyP95Ms != nil {
		eo.ProcessingLatencyP95Ms = F64(*es.ProcessingLatencyP95Ms)
	}
	if es.ProcessingLatencyP99Ms != nil {
		eo.ProcessingLatencyP99Ms = F64(*es.ProcessingLatencyP99Ms)
	}
	if es.ProcessingLatencyMeanMs != nil {
		eo.ProcessingLatencyMeanMs = F64(*es.ProcessingLatencyMeanMs)
	}
	return eo
}

func maxSec(s float64) float64 {
	if s <= 0 {
		return 1
	}
	return s
}
