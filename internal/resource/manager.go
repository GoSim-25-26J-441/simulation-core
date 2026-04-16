package resource

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

const (
	// DefaultInstanceCPUCores is the default number of CPU cores allocated to a service instance
	DefaultInstanceCPUCores = 1.0
	// DefaultInstanceMemoryMB is the default amount of memory (in MB) allocated to a service instance
	DefaultInstanceMemoryMB = 512.0
	// DefaultMemoryDownsizeHeadroomMB is the minimum slack required between active
	// memory and a new lower memory limit when decreasing per-instance memory.
	DefaultMemoryDownsizeHeadroomMB = 16.0
	// DefaultDrainTimeout is the simulated-time budget for draining a replica when
	// no explicit timeout is provided to ScaleServiceWithOptions.
	DefaultDrainTimeout = time.Hour
)

// Manager tracks resource usage across hosts and service instances
type Manager struct {
	mu                   sync.RWMutex
	hosts                map[string]*Host
	instances            map[string]*ServiceInstance
	hostToInstances      map[string][]string           // host ID -> instance IDs
	roundRobinIdx        map[string]int                // service name -> last selected instance index
	weightedRoundRobinIdx map[string]int               // service name -> weighted rr cursor
	sortedServiceInstMap map[string][]*ServiceInstance // service name -> sorted instances (cached); scale-down only shrinks this
	serviceRouting       map[string]*config.RoutingPolicy
	servicePlacement     map[string]*config.PlacementPolicy
	endpointRouting      map[string]*config.RoutingPolicy // key: serviceID:endpointPath
	routingRand          *rand.Rand
	nextInstanceID       int                           // global counter for new instance IDs when scaling up
	// drainTimeout is the simulated-time budget for scale-down drains when callers
	// use ScaleService without explicit options (set per run from optimization config).
	drainTimeout time.Duration
	// lastSimTime tracks the latest simulation time seen from the workload (for sweeps).
	lastSimTime time.Time
	// brokerQueues holds FIFO broker state for kind:queue services (per broker + topic).
	brokerQueues *BrokerQueues
}

// NewManager creates a new resource manager
func NewManager() *Manager {
	return &Manager{
		hosts:                make(map[string]*Host),
		instances:            make(map[string]*ServiceInstance),
		hostToInstances:      make(map[string][]string),
		roundRobinIdx:        make(map[string]int),
		weightedRoundRobinIdx: make(map[string]int),
		sortedServiceInstMap: make(map[string][]*ServiceInstance),
		serviceRouting:       make(map[string]*config.RoutingPolicy),
		servicePlacement:     make(map[string]*config.PlacementPolicy),
		endpointRouting:      make(map[string]*config.RoutingPolicy),
		routingRand:          rand.New(rand.NewSource(1)),
		brokerQueues:         newBrokerQueues(),
	}
}

func hostMemoryCapacityMB(h *Host) float64 {
	if h == nil {
		return 0
	}
	gb := h.MemoryGB()
	if gb < 1 {
		gb = 1
	}
	return float64(gb) * 1024.0
}

func containsStringFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), target) {
			return true
		}
	}
	return false
}

func (m *Manager) serviceReplicasOnHostLocked(serviceID, hostID string) int {
	n := 0
	for _, iid := range m.hostToInstances[hostID] {
		inst, ok := m.instances[iid]
		if !ok || inst == nil {
			continue
		}
		if inst.ServiceName() == serviceID {
			n++
		}
	}
	return n
}

func (m *Manager) serviceReplicasByZoneLocked(serviceID string) map[string]int {
	out := map[string]int{}
	for _, inst := range m.instances {
		if inst == nil || inst.ServiceName() != serviceID {
			continue
		}
		host, ok := m.hosts[inst.HostID()]
		if !ok || host == nil {
			continue
		}
		out[host.Zone()]++
	}
	return out
}

// hostSatisfiesPlacementLocked checks placement constraints excluding capacity.
// Caller must hold m.mu.
func (m *Manager) hostSatisfiesPlacementLocked(hostID, serviceID string, placement *config.PlacementPolicy) bool {
	if placement == nil {
		return true
	}
	host, ok := m.hosts[hostID]
	if !ok || host == nil {
		return false
	}
	if len(placement.AffinityZones) > 0 && !containsStringFold(placement.AffinityZones, host.Zone()) {
		return false
	}
	if len(placement.RequiredZones) > 0 && !containsStringFold(placement.RequiredZones, host.Zone()) {
		return false
	}
	if len(placement.AntiAffinityZones) > 0 && containsStringFold(placement.AntiAffinityZones, host.Zone()) {
		return false
	}
	if len(placement.RequiredHostLabels) > 0 {
		labels := host.Labels()
		for k, v := range placement.RequiredHostLabels {
			if labels[k] != v {
				return false
			}
		}
	}
	if len(placement.AntiAffinityServices) > 0 {
		for _, iid := range m.hostToInstances[hostID] {
			inst, ok := m.instances[iid]
			if !ok || inst == nil {
				continue
			}
			instSvc := inst.ServiceName()
			for _, blockedSvc := range placement.AntiAffinityServices {
				blockedSvc = strings.TrimSpace(blockedSvc)
				if blockedSvc == "" {
					continue
				}
				if instSvc == blockedSvc {
					return false
				}
			}
		}
	}
	if placement.MaxReplicasPerHost > 0 && m.serviceReplicasOnHostLocked(serviceID, hostID) >= placement.MaxReplicasPerHost {
		return false
	}
	return true
}

// pickHostForNewInstanceLocked returns a host ID that can fit another instance with the
// given reservation, or an error if none qualify. Hosts are tried in round-robin order
// (by existing instance count for this service) among those with capacity. Caller must hold m.mu (write lock).
func (m *Manager) pickHostForNewInstanceLocked(serviceID string, cpuCores, memoryMB float64) (string, error) {
	hostIDs := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		hostIDs = append(hostIDs, id)
	}
	sort.Strings(hostIDs)
	if len(hostIDs) == 0 {
		return "", fmt.Errorf("no hosts available")
	}
	placement := m.servicePlacement[serviceID]
	zoneLoad := m.serviceReplicasByZoneLocked(serviceID)
	type candidate struct {
		hostID    string
		matchPref int
		zoneLoad  int
		hostLoad  int
	}
	cands := make([]candidate, 0, len(hostIDs))
	placementRejected := 0
	capacityRejected := 0
	for _, hid := range hostIDs {
		if !m.hostSatisfiesPlacementLocked(hid, serviceID, placement) {
			placementRejected++
			continue
		}
		h := m.hosts[hid]
		var cpuSum, memSum float64
		for _, iid := range m.hostToInstances[hid] {
			inst, ok := m.instances[iid]
			if !ok || inst == nil {
				continue
			}
			cpuSum += inst.CPUCores()
			memSum += inst.MemoryMB()
		}
		capMem := hostMemoryCapacityMB(h)
		if cpuSum+cpuCores > float64(h.CPUCores())+1e-9 || memSum+memoryMB > capMem+1e-6 {
			capacityRejected++
			continue
		}
		matchPref := 0
		if placement != nil {
			if containsStringFold(placement.PreferredZones, h.Zone()) {
				matchPref++
			}
			if len(placement.PreferredHostLabels) > 0 {
				labels := h.Labels()
				for k, v := range placement.PreferredHostLabels {
					if labels[k] == v {
						matchPref++
					}
				}
			}
		}
		cands = append(cands, candidate{
			hostID:    hid,
			matchPref: matchPref,
			zoneLoad:  zoneLoad[h.Zone()],
			hostLoad:  m.serviceReplicasOnHostLocked(serviceID, hid),
		})
	}
	if len(cands) == 0 {
		if placementRejected > 0 && capacityRejected == 0 {
			return "", fmt.Errorf("no host satisfies placement constraints for service %s", serviceID)
		}
		if capacityRejected > 0 && placementRejected == 0 {
			return "", fmt.Errorf("no host has capacity for new instance (need %.2f CPU cores, %.2f MB memory)", cpuCores, memoryMB)
		}
		return "", fmt.Errorf("no host can place new instance for %s due to placement/capacity constraints", serviceID)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].matchPref != cands[j].matchPref {
			return cands[i].matchPref > cands[j].matchPref
		}
		if placement != nil && placement.SpreadAcrossZones && cands[i].zoneLoad != cands[j].zoneLoad {
			return cands[i].zoneLoad < cands[j].zoneLoad
		}
		if cands[i].hostLoad != cands[j].hostLoad {
			return cands[i].hostLoad < cands[j].hostLoad
		}
		return cands[i].hostID < cands[j].hostID
	})
	return cands[0].hostID, nil
}

// InitializeFromScenario initializes the resource manager from a scenario configuration
func (m *Manager) InitializeFromScenario(scenario *config.Scenario) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize hosts
	for _, hostConfig := range scenario.Hosts {
		memoryGB := hostConfig.MemoryGB
		if memoryGB <= 0 {
			memoryGB = 16
		}
		host := NewHost(hostConfig.ID, hostConfig.Cores, memoryGB)
		host.SetZone(hostConfig.Zone)
		host.SetLabels(hostConfig.Labels)
		m.hosts[hostConfig.ID] = host
	}

	if len(m.hosts) == 0 {
		return fmt.Errorf("no hosts defined")
	}

	// Initialize service instances with per-host reservation feasibility
	instanceID := 0
	for _, serviceConfig := range scenario.Services {
		m.serviceRouting[serviceConfig.ID] = serviceConfig.Routing
		m.servicePlacement[serviceConfig.ID] = serviceConfig.Placement
		for _, ep := range serviceConfig.Endpoints {
			m.endpointRouting[serviceConfig.ID+":"+ep.Path] = ep.Routing
		}
		cpuCores := serviceConfig.CPUCores
		if cpuCores == 0 {
			cpuCores = DefaultInstanceCPUCores
		}
		memoryMB := serviceConfig.MemoryMB
		if memoryMB == 0 {
			memoryMB = DefaultInstanceMemoryMB
		}

		for replica := 0; replica < serviceConfig.Replicas; replica++ {
			hostID, err := m.pickHostForNewInstanceLocked(serviceConfig.ID, cpuCores, memoryMB)
			if err != nil {
				return fmt.Errorf("cannot place service %s: %w (each instance needs %.2f CPU cores, %.2f MB memory)",
					serviceConfig.ID, err, cpuCores, memoryMB)
			}
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceConfig.ID, instanceID)
			instanceID++
			instance := NewServiceInstance(instanceIDStr, serviceConfig.ID, hostID, cpuCores, memoryMB)
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
			m.hostToInstances[hostID] = append(m.hostToInstances[hostID], instanceIDStr)
		}
	}
	m.nextInstanceID = instanceID

	// Build the sorted instance cache for each service
	m.rebuildSortedInstanceCache()

	return nil
}

// GetBrokerQueue returns (or creates) broker state for a queue service topic.
func (m *Manager) GetBrokerQueue(brokerID, topic string, eff *config.QueueBehavior) *BrokerQueueShard {
	if m.brokerQueues == nil {
		m.brokerQueues = newBrokerQueues()
	}
	return m.brokerQueues.GetOrCreateShard(brokerID, topic, eff)
}

// GetBrokerTopicSubscriberShard returns (or creates) broker state for one topic subscriber group.
func (m *Manager) GetBrokerTopicSubscriberShard(brokerID, topicPath, consumerGroup string, topicEff *config.TopicBehavior, sub *config.TopicSubscriber) *BrokerQueueShard {
	return m.GetBrokerTopicSubscriberPartitionShard(brokerID, topicPath, 0, consumerGroup, topicEff, sub)
}

// GetBrokerTopicSubscriberPartitionShard returns (or creates) broker state for one topic partition+subscriber group.
func (m *Manager) GetBrokerTopicSubscriberPartitionShard(brokerID, topicPath string, partition int, consumerGroup string, topicEff *config.TopicBehavior, sub *config.TopicSubscriber) *BrokerQueueShard {
	if m.brokerQueues == nil {
		m.brokerQueues = newBrokerQueues()
	}
	return m.brokerQueues.GetOrCreateTopicSubscriberPartitionShard(brokerID, topicPath, partition, consumerGroup, topicEff, sub)
}

// BrokerQueues returns the broker registry (for metrics).
func (m *Manager) BrokerQueues() *BrokerQueues {
	if m.brokerQueues == nil {
		m.brokerQueues = newBrokerQueues()
	}
	return m.brokerQueues
}

// QueueBrokerHealthSnapshots returns queue shard runtime state for live/control snapshots.
func (m *Manager) QueueBrokerHealthSnapshots(now time.Time) []QueueBrokerHealthSnapshot {
	return m.BrokerQueues().QueueHealthSnapshots(now)
}

// TopicBrokerHealthSnapshots returns topic subscriber-group runtime state for live/control snapshots.
func (m *Manager) TopicBrokerHealthSnapshots(now time.Time) []TopicBrokerHealthSnapshot {
	return m.BrokerQueues().TopicHealthSnapshots(now)
}

// GetServiceInstance returns a service instance by ID
func (m *Manager) GetServiceInstance(instanceID string) (*ServiceInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	instance, ok := m.instances[instanceID]
	return instance, ok
}

// GetHost returns a host by ID
func (m *Manager) GetHost(hostID string) (*Host, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	host, ok := m.hosts[hostID]
	return host, ok
}

// GetInstancesForService returns all instances for a given service name
func (m *Manager) GetInstancesForService(serviceName string) []*ServiceInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getInstancesForServiceLocked(serviceName)
}

// getInstancesForServiceLocked returns instances for a service (assumes lock is held)
func (m *Manager) getInstancesForServiceLocked(serviceName string) []*ServiceInstance {
	instances := make([]*ServiceInstance, 0)
	for _, instance := range m.instances {
		if instance.ServiceName() == serviceName {
			instances = append(instances, instance)
		}
	}
	return instances
}

// rebuildSortedInstanceCache rebuilds the cache of sorted routable (active) instances per service.
// Assumes lock is already held by caller.
func (m *Manager) rebuildSortedInstanceCache() {
	next := make(map[string][]*ServiceInstance)
	for _, instance := range m.instances {
		if !instance.IsRoutable() {
			continue
		}
		sn := instance.ServiceName()
		next[sn] = append(next[sn], instance)
	}
	for sn, instances := range next {
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].ID() < instances[j].ID()
		})
		next[sn] = instances
		if idx := m.roundRobinIdx[sn]; len(instances) > 0 && idx >= len(instances) {
			m.roundRobinIdx[sn] = 0
		}
	}
	m.sortedServiceInstMap = next
}

// ScaleServiceOptions carries simulation-time context for scale-down draining.
type ScaleServiceOptions struct {
	// SimTime is the current simulation clock. When zero, time.Now() is used.
	SimTime time.Time
	// DrainTimeout is the simulated duration after which a draining replica may be
	// removed even if still busy. When <= 0, DefaultDrainTimeout is used.
	DrainTimeout time.Duration
}

// ScaleService changes the number of replicas for a service at runtime.
// Scale-up: adds new active instances. Scale-down: marks surplus active instances as
// draining; they stop receiving new traffic and are removed once idle or after the
// drain timeout (see ProcessDrainingInstances).
func (m *Manager) ScaleService(serviceID string, newReplicas int) error {
	return m.ScaleServiceWithOptions(serviceID, newReplicas, ScaleServiceOptions{})
}

// ScaleServiceWithOptions is like ScaleService but supplies simulation time and drain budget.
func (m *Manager) ScaleServiceWithOptions(serviceID string, newReplicas int, opts ScaleServiceOptions) error {
	if newReplicas < 1 {
		return fmt.Errorf("replicas must be at least 1, got %d", newReplicas)
	}

	simTime := opts.SimTime
	if simTime.IsZero() {
		simTime = time.Now()
	}
	drainBudget := m.effectiveDrainTimeout(opts)
	deadline := simTime.Add(drainBudget)

	m.mu.Lock()
	defer m.mu.Unlock()

	allInst := m.getInstancesForServiceLocked(serviceID)
	if len(allInst) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	sort.Slice(allInst, func(i, j int) bool {
		return allInst[i].ID() < allInst[j].ID()
	})

	var activeInst []*ServiceInstance
	for _, inst := range allInst {
		if inst.Lifecycle() == InstanceActive {
			activeInst = append(activeInst, inst)
		}
	}
	activeN := len(activeInst)

	if newReplicas > activeN {
		if len(m.hosts) == 0 {
			return fmt.Errorf("no hosts available")
		}
		template := activeInst[0]
		if template == nil {
			// Only draining instances exist — clone template from any instance.
			template = allInst[0]
		}
		cpuCores := template.CPUCores()
		memoryMB := template.MemoryMB()

		for i := 0; i < newReplicas-activeN; i++ {
			hostID, err := m.pickHostForNewInstanceLocked(serviceID, cpuCores, memoryMB)
			if err != nil {
				return err
			}
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceID, m.nextInstanceID)
			m.nextInstanceID++

			instance := NewServiceInstance(instanceIDStr, serviceID, hostID, cpuCores, memoryMB)
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
			m.hostToInstances[hostID] = append(m.hostToInstances[hostID], instanceIDStr)
		}
		m.rebuildSortedInstanceCache()
	} else if newReplicas < activeN {
		// Drain the last (activeN - newReplicas) active instances by stable ID order.
		toDrain := activeInst[newReplicas:]
		for _, inst := range toDrain {
			inst.SetDraining(deadline)
		}
		m.rebuildSortedInstanceCache()
	}
	// else newReplicas == activeN: no-op

	return nil
}

// ProcessDrainingInstances removes draining instances that are idle or past their
// simulated drain deadline. For hard timeouts with queued work, it returns the
// request IDs that were waiting in those instance queues so callers can fail them.
func (m *Manager) ProcessDrainingInstances(simTime time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !simTime.IsZero() && simTime.After(m.lastSimTime) {
		m.lastSimTime = simTime
	}

	var removeIDs []string
	var evictIDs []string
	for id, inst := range m.instances {
		if inst.Lifecycle() != InstanceDraining {
			continue
		}
		deadline := inst.DrainDeadline()
		idle := inst.ActiveRequests() == 0 && inst.QueueLength() == 0
		timedOut := !deadline.IsZero() && !simTime.Before(deadline)
		switch {
		case idle:
			removeIDs = append(removeIDs, id)
		case timedOut && !idle:
			// Hard timeout: sync host accounting before dropping the instance record.
			evictIDs = append(evictIDs, id)
		}
	}
	var droppedReqIDs []string
	for _, id := range evictIDs {
		droppedReqIDs = append(droppedReqIDs, m.removeInstanceLocked(id, simTime, true)...)
	}
	for _, id := range removeIDs {
		m.removeInstanceLocked(id, simTime, false)
	}
	if len(removeIDs)+len(evictIDs) > 0 {
		m.rebuildSortedInstanceCache()
	}
	return droppedReqIDs
}

// NoteSimTime records the latest simulation time from the workload (best-effort).
func (m *Manager) NoteSimTime(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !t.IsZero() && t.After(m.lastSimTime) {
		m.lastSimTime = t
	}
}

// LastSimTime returns the last observed simulation time, or zero if unknown.
func (m *Manager) LastSimTime() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastSimTime
}

func (m *Manager) removeInstanceLocked(instanceID string, simTime time.Time, evictAccounting bool) []string {
	inst, ok := m.instances[instanceID]
	if !ok {
		return nil
	}
	hostID := inst.HostID()
	host, hostOk := m.hosts[hostID]

	var dropped []string
	if evictAccounting {
		if simTime.IsZero() {
			simTime = time.Now()
		}
		dropped = inst.EvictResourceState(simTime)
		if hostOk {
			instances := m.collectInstancesForHost(hostID)
			m.updateHostCPUUtilizationWithData(host, instances, simTime)
			m.updateHostMemoryUtilizationWithData(host, instances)
		}
	}

	delete(m.instances, instanceID)

	if ids, ok := m.hostToInstances[hostID]; ok {
		out := ids[:0]
		for _, x := range ids {
			if x != instanceID {
				out = append(out, x)
			}
		}
		m.hostToInstances[hostID] = out
	}
	if h, ok := m.hosts[hostID]; ok {
		h.RemoveService(instanceID)
	}
	return dropped
}

// ActiveReplicas returns the number of replicas currently in rotation for the service (for GET config).
func (m *Manager) ActiveReplicas(serviceID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	instances, ok := m.sortedServiceInstMap[serviceID]
	if !ok {
		return 0
	}
	return len(instances)
}

// HostCount returns the number of hosts currently managed.
func (m *Manager) HostCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.hosts)
}

// MaxHostCPUUtilization returns the maximum CPU utilization across all hosts.
// If there are no hosts, it returns 0.
func (m *Manager) MaxHostCPUUtilization() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	maxUtil := 0.0
	for _, host := range m.hosts {
		if util := host.CPUUtilization(); util > maxUtil {
			maxUtil = util
		}
	}
	return maxUtil
}

// MaxHostMemoryUtilization returns the maximum memory utilization across all hosts.
// If there are no hosts, it returns 0.
func (m *Manager) MaxHostMemoryUtilization() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	maxUtil := 0.0
	for _, host := range m.hosts {
		if util := host.MemoryUtilization(); util > maxUtil {
			maxUtil = util
		}
	}
	return maxUtil
}

// SetScaleDownDrainTimeout configures the default simulated drain budget used by
// ScaleService (without explicit ScaleServiceOptions). Non-positive values are ignored.
func (m *Manager) SetScaleDownDrainTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d > 0 {
		m.drainTimeout = d
	}
}

func (m *Manager) effectiveDrainTimeout(opts ScaleServiceOptions) time.Duration {
	if opts.DrainTimeout > 0 {
		return opts.DrainTimeout
	}
	if m.drainTimeout > 0 {
		return m.drainTimeout
	}
	return DefaultDrainTimeout
}

// HostIDs returns a slice of all host IDs currently managed.
func (m *Manager) HostIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ScaleOutHosts increases the number of hosts up to targetCount by adding new
// hosts with the same capacity as an existing host. If targetCount is less
// than or equal to the current host count, this is a no-op.
func (m *Manager) ScaleOutHosts(targetCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := len(m.hosts)
	if targetCount <= current {
		return nil
	}
	if current == 0 {
		return fmt.Errorf("cannot scale out hosts: no existing hosts to copy capacity from")
	}

	// Pick an arbitrary existing host as the template for new hosts.
	var template *Host
	for _, h := range m.hosts {
		template = h
		break
	}
	if template == nil {
		return fmt.Errorf("cannot scale out hosts: template host not found")
	}

	nextIndex := current + 1
	for len(m.hosts) < targetCount {
		id := fmt.Sprintf("host-auto-%d", nextIndex)
		nextIndex++
		if _, exists := m.hosts[id]; exists {
			continue
		}
		host := NewHost(id, template.CPUCores(), template.MemoryGB())
		host.SetZone(template.Zone())
		host.SetLabels(template.Labels())
		m.hosts[id] = host
	}

	return nil
}

// ScaleOutHostsForService scales out hosts using a template that best matches the
// service placement constraints/preferences.
func (m *Manager) ScaleOutHostsForService(serviceID string, targetCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := len(m.hosts)
	if targetCount <= current {
		return nil
	}
	if current == 0 {
		return fmt.Errorf("cannot scale out hosts: no existing hosts to copy capacity from")
	}
	placement := m.servicePlacement[serviceID]
	var template *Host
	var templateID string
	hostIDs := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		hostIDs = append(hostIDs, id)
	}
	sort.Strings(hostIDs)
	bestScore := -1
	for _, hid := range hostIDs {
		h := m.hosts[hid]
		if h == nil {
			continue
		}
		score := 0
		if placement == nil || m.hostSatisfiesPlacementLocked(hid, serviceID, placement) {
			score += 2
		}
		if placement != nil {
			if containsStringFold(placement.PreferredZones, h.Zone()) {
				score++
			}
			labels := h.Labels()
			for k, v := range placement.PreferredHostLabels {
				if labels[k] == v {
					score++
				}
			}
		}
		if score > bestScore {
			bestScore = score
			template = h
			templateID = hid
		}
	}
	if template == nil {
		return fmt.Errorf("cannot scale out hosts: template host not found")
	}
	nextIndex := current + 1
	for len(m.hosts) < targetCount {
		id := fmt.Sprintf("host-auto-%d", nextIndex)
		nextIndex++
		if _, exists := m.hosts[id]; exists {
			continue
		}
		host := NewHost(id, template.CPUCores(), template.MemoryGB())
		host.SetZone(template.Zone())
		host.SetLabels(template.Labels())
		m.hosts[id] = host
	}
	_ = templateID
	return nil
}

// IncreaseHostCapacity increases CPU cores and/or memory (GB) for all hosts.
// Non-positive deltas are ignored for the respective dimension.
func (m *Manager) IncreaseHostCapacity(cpuDelta, memoryGBDelta int) {
	if cpuDelta <= 0 && memoryGBDelta <= 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, host := range m.hosts {
		if cpuDelta > 0 {
			newCores := host.CPUCores() + cpuDelta
			host.SetCPUCores(newCores)
		}
		if memoryGBDelta > 0 {
			newMem := host.MemoryGB() + memoryGBDelta
			host.SetMemoryGB(newMem)
		}
	}
}

// ScaleInHosts reduces the number of hosts to targetCount by removing hosts that
// have no service instances. Prefer removing auto-added hosts (host-auto-*)
// first, in reverse order of creation, so scenario-defined hosts are kept.
// Does not remove a host that has instances; returns an error if targetCount
// cannot be reached without doing so. Does not scale below 1 host.
func (m *Manager) ScaleInHosts(targetCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := len(m.hosts)
	if targetCount >= current {
		return nil
	}
	if targetCount < 1 {
		targetCount = 1
	}
	if current <= 1 {
		return nil
	}

	// Collect empty host IDs (no instances on that host). Hosts added by ScaleOutHosts
	// may not appear in hostToInstances, so iterate over m.hosts and check instance count.
	var autoEmpty, otherEmpty []string
	for hostID := range m.hosts {
		instanceIDs := m.hostToInstances[hostID]
		if len(instanceIDs) > 0 {
			continue
		}
		if strings.HasPrefix(hostID, "host-auto-") {
			autoEmpty = append(autoEmpty, hostID)
		} else {
			otherEmpty = append(otherEmpty, hostID)
		}
	}
	// Sort auto-empty by N descending (remove highest host-auto-N first).
	sort.Slice(autoEmpty, func(i, j int) bool {
		ni, errI := strconv.Atoi(strings.TrimPrefix(autoEmpty[i], "host-auto-"))
		nj, errJ := strconv.Atoi(strings.TrimPrefix(autoEmpty[j], "host-auto-"))
		if errI != nil {
			ni = 0
		}
		if errJ != nil {
			nj = 0
		}
		return ni > nj
	})
	autoEmpty = append(autoEmpty, otherEmpty...)
	candidates := autoEmpty
	toRemove := current - targetCount
	if toRemove <= 0 {
		return nil
	}
	if len(candidates) < toRemove {
		return fmt.Errorf("cannot scale in to %d hosts: only %d empty host(s) available", targetCount, len(candidates))
	}
	for i := 0; i < toRemove; i++ {
		hostID := candidates[i]
		delete(m.hosts, hostID)
		delete(m.hostToInstances, hostID)
	}
	return nil
}

// DecreaseHostCapacity reduces CPU cores and/or memory (GB) for all hosts by
// the given deltas. Negative deltas are applied; minimum 1 core and 1 GB per
// host. Returns an error if the new capacity would be below current allocated
// usage on any host.
func (m *Manager) DecreaseHostCapacity(cpuDelta, memoryGBDelta int) error {
	if cpuDelta >= 0 && memoryGBDelta >= 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for hostID, host := range m.hosts {
		instances := m.collectInstancesForHost(hostID)
		var allocCPU float64
		var allocMemMB float64
		for _, inst := range instances {
			allocCPU += inst.CPUCores()
			allocMemMB += inst.MemoryMB()
		}
		newCores := host.CPUCores() + cpuDelta
		if newCores < 1 {
			newCores = 1
		}
		newMemGB := host.MemoryGB() + memoryGBDelta
		if newMemGB < 1 {
			newMemGB = 1
		}
		if allocCPU > float64(newCores)+1e-9 {
			return fmt.Errorf("cannot decrease host capacity: host %s allocated %.2f CPU cores would exceed new capacity %d", hostID, allocCPU, newCores)
		}
		capacityMB := float64(newMemGB) * 1024.0
		if allocMemMB > capacityMB+1e-6 {
			return fmt.Errorf("cannot decrease host capacity: host %s allocated %.2f MB would exceed new memory capacity %.2f MB", hostID, allocMemMB, capacityMB)
		}
	}

	for _, host := range m.hosts {
		if cpuDelta < 0 {
			newCores := host.CPUCores() + cpuDelta
			if newCores < 1 {
				newCores = 1
			}
			host.SetCPUCores(newCores)
		}
		if memoryGBDelta < 0 {
			newMem := host.MemoryGB() + memoryGBDelta
			if newMem < 1 {
				newMem = 1
			}
			host.SetMemoryGB(newMem)
		}
	}
	return nil
}

// UpdateServiceResources updates per-instance CPU cores and memory (MB) for all
// instances of a given service. Passing 0 for a field leaves it unchanged.
func (m *Manager) UpdateServiceResources(serviceID string, cpuCores, memoryMB float64) error {
	return m.UpdateServiceResourcesWithHeadroom(serviceID, cpuCores, memoryMB, 0)
}

// UpdateServiceResourcesWithHeadroom is like UpdateServiceResources; memoryHeadroomMB
// is the minimum slack required when decreasing memory (when <= 0, DefaultMemoryDownsizeHeadroomMB is used).
func (m *Manager) UpdateServiceResourcesWithHeadroom(serviceID string, cpuCores, memoryMB, memoryHeadroomMB float64) error {
	if cpuCores < 0 || memoryMB < 0 {
		return fmt.Errorf("cpu_cores and memory_mb must be non-negative")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	instances := m.getInstancesForServiceLocked(serviceID)
	if len(instances) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	headroom := memoryHeadroomMB
	if headroom <= 0 {
		headroom = DefaultMemoryDownsizeHeadroomMB
	}

	// Validate downsize against live usage before host capacity checks.
	for _, inst := range instances {
		if cpuCores > 0 && cpuCores+1e-9 < inst.CPUCores() {
			if inst.ActiveRequests() != 0 || inst.QueueLength() != 0 {
				return fmt.Errorf("cannot decrease CPU for instance %s while it has active requests or queued work", inst.ID())
			}
		}
		if memoryMB > 0 && memoryMB+1e-9 < inst.MemoryMB() {
			if memoryMB+1e-9 < inst.ActiveMemoryMB()+headroom {
				return fmt.Errorf("cannot decrease memory for instance %s: proposed %.2f MB is below active usage %.2f MB plus headroom %.2f MB",
					inst.ID(), memoryMB, inst.ActiveMemoryMB(), headroom)
			}
		}
	}

	// Check host capacity constraints for the proposed change.
	for hostID, host := range m.hosts {
		hostInstances := m.collectInstancesForHost(hostID)
		if len(hostInstances) == 0 {
			continue
		}

		totalCPU := 0.0
		totalMemMB := 0.0
		for _, inst := range hostInstances {
			cpu := inst.CPUCores()
			mem := inst.MemoryMB()
			if inst.ServiceName() == serviceID {
				if cpuCores > 0 {
					cpu = cpuCores
				}
				if memoryMB > 0 {
					mem = memoryMB
				}
			}
			totalCPU += cpu
			totalMemMB += mem
		}

		// Enforce CPU capacity if host has a finite number of cores.
		if cores := host.CPUCores(); cores > 0 && totalCPU > float64(cores)+1e-9 {
			return fmt.Errorf("host CPU capacity exceeded on %s: requested %.2f cores, capacity %d", hostID, totalCPU, cores)
		}

		// Enforce memory capacity if host has finite memory configured.
		if memGB := host.MemoryGB(); memGB > 0 {
			capacityMB := float64(memGB) * 1024.0
			if totalMemMB > capacityMB+1e-6 {
				return fmt.Errorf("host memory capacity exceeded on %s: requested %.2f MB, capacity %.2f MB", hostID, totalMemMB, capacityMB)
			}
		}
	}

	// Apply the update now that capacity checks have passed.
	for _, inst := range instances {
		if cpuCores > 0 {
			inst.SetCPUCores(cpuCores)
		}
		if memoryMB > 0 {
			inst.SetMemoryMB(memoryMB)
		}
	}

	// Host utilization will be recomputed on next allocation/release; no need
	// to force an update here for correctness of the simulator.
	return nil
}

// ListServiceIDs returns all service IDs that have at least one instance (for GET config).
func (m *Manager) ListServiceIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, inst := range m.instances {
		seen[inst.ServiceName()] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ReserveCPUWork reserves the next FIFO CPU interval on the instance (see ServiceInstance.ReserveCPUWork).
func (m *Manager) ReserveCPUWork(instanceID string, arrivalTime time.Time, cpuDemandMs float64) (cpuStart, cpuEnd time.Time, err error) {
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return time.Time{}, time.Time{}, fmt.Errorf("instance not found: %s", instanceID)
	}
	cpuStart, cpuEnd = instance.ReserveCPUWork(arrivalTime, cpuDemandMs)
	return cpuStart, cpuEnd, nil
}

// ReserveDBWork reserves a datastore connection slot for IO duration after CPU completes.
func (m *Manager) ReserveDBWork(instanceID string, arrivalAfterCPU time.Time, ioDurMs float64, maxSlots int) (ioStart, ioEnd time.Time, slotIdx int, waitMs float64, err error) {
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return time.Time{}, time.Time{}, -1, 0, fmt.Errorf("instance not found: %s", instanceID)
	}
	ioStart, ioEnd, slotIdx, waitMs = instance.ReserveDBWork(arrivalAfterCPU, ioDurMs, maxSlots)
	return ioStart, ioEnd, slotIdx, waitMs, nil
}

// ReleaseDBConnection decrements DB pool in-flight accounting when IO completes.
func (m *Manager) ReleaseDBConnection(instanceID string) {
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok || instance == nil {
		return
	}
	instance.ReleaseDBConnection()
}

// RollbackCPUTailReservation reverts a CPU reservation when allocation fails (tail slot only).
func (m *Manager) RollbackCPUTailReservation(instanceID string, cpuStart, cpuEnd time.Time) {
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	m.mu.Unlock()
	if !ok {
		return
	}
	instance.RollbackCPUTailReservation(cpuStart, cpuEnd)
}

// AllocateCPU allocates CPU resources for a request
func (m *Manager) AllocateCPU(instanceID string, cpuTimeMs float64, simTime time.Time) error {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	// Get host ID while we have the instance reference
	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("host not found: %s", hostID)
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Now perform operations without holding Manager lock
	// Allocate CPU on instance
	instance.AllocateCPU(cpuTimeMs, simTime)

	// Update host utilization (aggregate from all instances on this host)
	m.updateHostCPUUtilizationWithData(host, instances, simTime)

	return nil
}

// ReleaseCPU releases CPU resources for a request
func (m *Manager) ReleaseCPU(instanceID string, cpuTimeMs float64, simTime time.Time) {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Release CPU and update utilization without holding Manager lock
	instance.ReleaseCPU(cpuTimeMs, simTime)
	m.updateHostCPUUtilizationWithData(host, instances, simTime)
}

// AllocateMemory allocates memory resources
func (m *Manager) AllocateMemory(instanceID string, memoryMB float64) error {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("host not found: %s", hostID)
	}

	// Check host memory capacity (skip check if host has unlimited memory, i.e., 0 GB configured)
	// Note: This check is best-effort. We read host memory utilization without holding the Manager
	// lock for the entire allocation operation to avoid lock hierarchy issues. This means another
	// goroutine could allocate memory between our check and allocation, potentially causing
	// over-allocation. This is an acceptable trade-off for better concurrency.
	hostMemoryGB := host.MemoryGB()
	if hostMemoryGB > 0 {
		hostMemUtil := host.MemoryUtilization()
		if hostMemUtil+(memoryMB/1024.0)/float64(hostMemoryGB) > 1.0 {
			m.mu.Unlock()
			return ErrHostMemoryCapacity
		}
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Allocate memory and update utilization without holding Manager lock
	instance.AllocateMemory(memoryMB)
	m.updateHostMemoryUtilizationWithData(host, instances)

	return nil
}

// ReleaseMemory releases memory resources
func (m *Manager) ReleaseMemory(instanceID string, memoryMB float64) {
	// Collect references while holding Manager lock
	m.mu.Lock()
	instance, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return
	}

	hostID := instance.HostID()
	host, ok := m.hosts[hostID]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Get all instances on this host for utilization calculation
	instances := m.collectInstancesForHost(hostID)
	m.mu.Unlock()

	// Release memory and update utilization without holding Manager lock
	instance.ReleaseMemory(memoryMB)
	m.updateHostMemoryUtilizationWithData(host, instances)
}

// EnqueueRequest adds a request to the instance queue
func (m *Manager) EnqueueRequest(instanceID string, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return fmt.Errorf("instance not found: %s", instanceID)
	}

	instance.EnqueueRequest(requestID)
	return nil
}

// DequeueRequest removes a request from the instance queue
func (m *Manager) DequeueRequest(instanceID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return "", false
	}

	return instance.DequeueRequest()
}

// GetQueueLength returns the queue length for an instance
func (m *Manager) GetQueueLength(instanceID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return 0
	}

	return instance.QueueLength()
}

// GetHostUtilization returns CPU and memory utilization for a host
func (m *Manager) GetHostUtilization(hostID string) (cpuUtil, memUtil float64, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	host, ok := m.hosts[hostID]
	if !ok {
		return 0, 0, false
	}

	return host.CPUUtilization(), host.MemoryUtilization(), true
}

// GetInstanceUtilization returns CPU and memory utilization for an instance
func (m *Manager) GetInstanceUtilization(instanceID string) (cpuUtil, memUtil float64, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instance, ok := m.instances[instanceID]
	if !ok {
		return 0, 0, false
	}

	return instance.CPUUtilization(), instance.MemoryUtilization(), true
}

// GetAllHosts returns all hosts
func (m *Manager) GetAllHosts() []*Host {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hosts := make([]*Host, 0, len(m.hosts))
	for _, host := range m.hosts {
		hosts = append(hosts, host)
	}
	return hosts
}

// GetAllInstances returns all service instances
func (m *Manager) GetAllInstances() []*ServiceInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instances := make([]*ServiceInstance, 0, len(m.instances))
	for _, instance := range m.instances {
		instances = append(instances, instance)
	}
	return instances
}

// collectInstancesForHost gathers all ServiceInstance pointers for a given host.
// This helper method assumes the Manager lock is already held by the caller.
func (m *Manager) collectInstancesForHost(hostID string) []*ServiceInstance {
	instanceIDs := m.hostToInstances[hostID]
	instances := make([]*ServiceInstance, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		if inst, ok := m.instances[id]; ok {
			instances = append(instances, inst)
		}
	}
	return instances
}

// updateHostCPUUtilizationWithData updates host CPU utilization by delegating to Host aggregation.
func (m *Manager) updateHostCPUUtilizationWithData(host *Host, instances []*ServiceInstance, simTime time.Time) {
	if simTime.IsZero() {
		simTime = time.Now()
	}
	host.UpdateCPUUtilizationFromInstancesAt(simTime, instances)
}

// updateHostMemoryUtilizationWithData updates host memory utilization by delegating to Host aggregation
func (m *Manager) updateHostMemoryUtilizationWithData(host *Host, instances []*ServiceInstance) {
	sources := make([]InstanceUtilizationSource, len(instances))
	for i, inst := range instances {
		sources[i] = inst
	}
	host.UpdateMemoryUtilization(sources)
}
