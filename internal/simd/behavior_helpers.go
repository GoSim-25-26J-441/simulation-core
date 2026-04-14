package simd

import (
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

func isDatastoreWorkload(svc *config.Service, ep *config.Endpoint) bool {
	if svc == nil || ep == nil {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(svc.Kind))
	r := strings.ToLower(strings.TrimSpace(svc.Role))
	m := strings.ToLower(strings.TrimSpace(svc.Model))
	if k == "database" || r == "datastore" || m == "db_latency" {
		return true
	}
	// External services keep prior one-phase behavior unless IO/pool fields are set explicitly.
	if k == "external" {
		if ep.IOMs.Mean > 0 || ep.IOMs.Sigma > 0 {
			return true
		}
		if ep.ConnectionPool > 0 {
			return true
		}
		if svc.Behavior != nil && svc.Behavior.MaxConnections > 0 {
			return true
		}
	}
	return false
}

func effectiveDBMaxConnections(svc *config.Service, ep *config.Endpoint) int {
	if ep != nil && ep.ConnectionPool > 0 {
		return ep.ConnectionPool
	}
	if svc != nil && svc.Behavior != nil && svc.Behavior.MaxConnections > 0 {
		return svc.Behavior.MaxConnections
	}
	return 0
}

func sampleEndpointIOWorkloadMs(ep *config.Endpoint, rng *utils.RandSource) float64 {
	if ep == nil || rng == nil {
		return 0
	}
	if ep.IOMs.Mean > 0 || ep.IOMs.Sigma > 0 {
		v := rng.NormFloat64(ep.IOMs.Mean, ep.IOMs.Sigma)
		if v < 0 {
			return 0
		}
		return v
	}
	v := rng.NormFloat64(ep.NetLatencyMs.Mean, ep.NetLatencyMs.Sigma)
	if v < 0 {
		return 0
	}
	return v
}

func mergedDependencyFailureRate(ds config.DownstreamCall, tgt *config.Service) float64 {
	p := ds.FailureRate
	if p < 0 {
		p = 0
	}
	if tgt != nil && tgt.Behavior != nil {
		q := tgt.Behavior.FailureRate
		if strings.ToLower(strings.TrimSpace(tgt.Kind)) == "external" && q > 0 {
			p = 1 - (1-p)*(1-q)
		}
	}
	if p > 1 {
		return 1
	}
	return p
}

func mergedLocalFailureRate(svc *config.Service, ep *config.Endpoint) float64 {
	var p float64
	if svc != nil && svc.Behavior != nil {
		p += svc.Behavior.FailureRate
	}
	if ep != nil {
		p += ep.FailureRate
	}
	if p > 1 {
		return 1
	}
	if p < 0 {
		return 0
	}
	return p
}
