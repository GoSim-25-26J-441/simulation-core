package resource

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

const (
	RoutingRoundRobin       = "round_robin"
	RoutingRandom           = "random"
	RoutingLeastConnections = "least_connections"
	RoutingLeastQueue       = "least_queue"
	RoutingLeastCPU         = "least_cpu"
	RoutingWeightedRR       = "weighted_round_robin"
	RoutingSticky           = "sticky"
	weightedWheelScale      = 1000
)

// SetRoutingSeed configures deterministic random/weighted routing for reproducible simulations.
func (m *Manager) SetRoutingSeed(seed int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routingRand = rand.New(rand.NewSource(seed))
}

func normalizeRoutingStrategy(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return RoutingRoundRobin
	}
	return s
}

func (m *Manager) routingPolicyForRequestLocked(serviceName string, req *models.Request) *config.RoutingPolicy {
	if req != nil {
		if epPol := m.endpointRouting[serviceName+":"+req.Endpoint]; epPol != nil {
			return epPol
		}
	}
	return m.serviceRouting[serviceName]
}

func (m *Manager) applyLocalityPreferenceLocked(instances []*ServiceInstance, pol *config.RoutingPolicy, req *models.Request) []*ServiceInstance {
	if len(instances) == 0 || pol == nil || req == nil || req.Metadata == nil {
		return instances
	}
	key := strings.TrimSpace(pol.LocalityZoneFrom)
	if key == "" {
		return instances
	}
	raw, ok := req.Metadata[key]
	if !ok {
		return instances
	}
	targetZone := strings.TrimSpace(fmt.Sprint(raw))
	if targetZone == "" {
		return instances
	}
	preferred := make([]*ServiceInstance, 0, len(instances))
	for _, inst := range instances {
		if inst == nil {
			continue
		}
		h, ok := m.hosts[inst.HostID()]
		if !ok || h == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(h.Zone()), targetZone) {
			preferred = append(preferred, inst)
		}
	}
	if len(preferred) > 0 {
		return preferred
	}
	return instances
}

// SelectInstanceForRequest selects an eligible instance for serviceName based on routing policy and request context.
func (m *Manager) SelectInstanceForRequest(serviceName string, req *models.Request, simTime time.Time) (*ServiceInstance, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instances, ok := m.sortedServiceInstMap[serviceName]
	if !ok || len(instances) == 0 {
		return nil, "", fmt.Errorf("no instances available for service %s", serviceName)
	}
	pol := m.routingPolicyForRequestLocked(serviceName, req)
	instances = m.applyLocalityPreferenceLocked(instances, pol, req)
	strategy := RoutingRoundRobin
	if pol != nil {
		strategy = normalizeRoutingStrategy(pol.Strategy)
	}
	switch strategy {
	case RoutingRoundRobin:
		return m.selectRoundRobinLocked(serviceName, instances), strategy, nil
	case RoutingRandom:
		idx := m.routingRand.Intn(len(instances))
		return instances[idx], strategy, nil
	case RoutingLeastConnections:
		best := instances[0]
		bestVal := best.ActiveRequests()
		for i := 1; i < len(instances); i++ {
			v := instances[i].ActiveRequests()
			if v < bestVal {
				best = instances[i]
				bestVal = v
			}
		}
		return best, strategy, nil
	case RoutingLeastQueue:
		best := instances[0]
		bestVal := best.QueueLength()
		for i := 1; i < len(instances); i++ {
			v := instances[i].QueueLength()
			if v < bestVal {
				best = instances[i]
				bestVal = v
			}
		}
		return best, strategy, nil
	case RoutingLeastCPU:
		best := instances[0]
		bestVal := best.CPUUtilizationAt(simTime)
		for i := 1; i < len(instances); i++ {
			v := instances[i].CPUUtilizationAt(simTime)
			if v < bestVal {
				best = instances[i]
				bestVal = v
			}
		}
		return best, strategy, nil
	case RoutingWeightedRR:
		total := 0
		cum := make([]int, len(instances))
		for i, inst := range instances {
			w := 1.0
			if pol != nil && pol.Weights != nil {
				if x, ok := pol.Weights[inst.ID()]; ok {
					w = x
				}
			}
			wi := weightedWheelSlots(w)
			total += wi
			cum[i] = total
		}
		if total <= 0 {
			// Explicit fallback: all weights effectively zero means use default RR.
			return m.selectRoundRobinLocked(serviceName, instances), RoutingRoundRobin, nil
		}
		cursor := m.weightedRoundRobinIdx[serviceName] % total
		m.weightedRoundRobinIdx[serviceName] = (cursor + 1) % total
		for i := range cum {
			if cursor < cum[i] {
				return instances[i], strategy, nil
			}
		}
		return instances[len(instances)-1], strategy, nil
	case RoutingSticky:
		if pol == nil || strings.TrimSpace(pol.StickyKeyFrom) == "" || req == nil {
			return m.selectRoundRobinLocked(serviceName, instances), RoutingRoundRobin, nil
		}
		key := strings.TrimSpace(pol.StickyKeyFrom)
		v, ok := req.Metadata[key]
		if !ok {
			return m.selectRoundRobinLocked(serviceName, instances), RoutingRoundRobin, nil
		}
		h := fnv.New64a()
		_, _ = h.Write([]byte(fmt.Sprint(v)))
		idx := int(h.Sum64() % uint64(len(instances)))
		return instances[idx], strategy, nil
	default:
		return nil, strategy, fmt.Errorf("unsupported routing strategy %q for service %s", strategy, serviceName)
	}
}

func weightedWheelSlots(w float64) int {
	if w <= 0 {
		return 0
	}
	// Preserve fractional proportions deterministically by using a fixed wheel scale.
	slots := int(w*weightedWheelScale + 0.5)
	if slots < 1 {
		slots = 1
	}
	return slots
}

func (m *Manager) selectRoundRobinLocked(serviceName string, instances []*ServiceInstance) *ServiceInstance {
	idx := m.roundRobinIdx[serviceName]
	if idx >= len(instances) {
		idx = 0
	}
	selected := instances[idx]
	m.roundRobinIdx[serviceName] = (idx + 1) % len(instances)
	return selected
}
