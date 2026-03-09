package resource

import (
	"fmt"
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
)

// Manager tracks resource usage across hosts and service instances
type Manager struct {
	mu                   sync.RWMutex
	hosts                map[string]*Host
	instances            map[string]*ServiceInstance
	hostToInstances      map[string][]string           // host ID -> instance IDs
	roundRobinIdx        map[string]int                // service name -> last selected instance index
	sortedServiceInstMap map[string][]*ServiceInstance // service name -> sorted instances (cached); scale-down only shrinks this
	nextInstanceID       int                           // global counter for new instance IDs when scaling up
}

// NewManager creates a new resource manager
func NewManager() *Manager {
	return &Manager{
		hosts:                make(map[string]*Host),
		instances:            make(map[string]*ServiceInstance),
		hostToInstances:      make(map[string][]string),
		roundRobinIdx:        make(map[string]int),
		sortedServiceInstMap: make(map[string][]*ServiceInstance),
	}
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
		m.hosts[hostConfig.ID] = host
	}

	// Initialize service instances
	instanceID := 0
	for _, serviceConfig := range scenario.Services {
		// Distribute replicas across available hosts
		hostIDs := make([]string, 0, len(m.hosts))
		for hostID := range m.hosts {
			hostIDs = append(hostIDs, hostID)
		}
		if len(hostIDs) == 0 {
			return fmt.Errorf("no hosts available for service %s", serviceConfig.ID)
		}

		for replica := 0; replica < serviceConfig.Replicas; replica++ {
			// Round-robin assignment of replicas to hosts
			hostID := hostIDs[replica%len(hostIDs)]
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceConfig.ID, instanceID)
			instanceID++

			// Use configured values if provided, otherwise use defaults
			cpuCores := serviceConfig.CPUCores
			if cpuCores == 0 {
				cpuCores = DefaultInstanceCPUCores
			}
			memoryMB := serviceConfig.MemoryMB
			if memoryMB == 0 {
				memoryMB = DefaultInstanceMemoryMB
			}

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

// SelectInstanceForService selects an instance for a service using round-robin selection
func (m *Manager) SelectInstanceForService(serviceName string) (*ServiceInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Use cached sorted instances if available
	instances, ok := m.sortedServiceInstMap[serviceName]
	if !ok || len(instances) == 0 {
		return nil, fmt.Errorf("no instances available for service %s", serviceName)
	}

	// Get the current index for this service (defaults to 0)
	idx := m.roundRobinIdx[serviceName]

	// Select the instance at the current index
	selectedInstance := instances[idx]

	// Update the index for next time (wrap around to 0 if we reach the end)
	m.roundRobinIdx[serviceName] = (idx + 1) % len(instances)

	return selectedInstance, nil
}

// rebuildSortedInstanceCache rebuilds the cache of sorted instances per service
// Assumes lock is already held by caller
func (m *Manager) rebuildSortedInstanceCache() {
	// Group instances by service
	serviceInstances := make(map[string][]*ServiceInstance)
	for _, instance := range m.instances {
		serviceName := instance.ServiceName()
		serviceInstances[serviceName] = append(serviceInstances[serviceName], instance)
	}

	// Sort and cache each service's instances
	for serviceName, instances := range serviceInstances {
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].ID() < instances[j].ID()
		})
		m.sortedServiceInstMap[serviceName] = instances
	}
}

// ScaleService changes the number of replicas for a service at runtime.
// Scale-up: adds new instances (round-robin across hosts) using same CPU/memory as existing instances.
// Scale-down: reduces the set of instances that receive new traffic (soft scale-down; existing instances remain for in-flight requests).
func (m *Manager) ScaleService(serviceID string, newReplicas int) error {
	if newReplicas < 1 {
		return fmt.Errorf("replicas must be at least 1, got %d", newReplicas)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	instances := m.getInstancesForServiceLocked(serviceID)
	if len(instances) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	sort.Slice(instances, func(i, j int) bool {
		return instances[i].ID() < instances[j].ID()
	})
	currentN := len(instances)

	if newReplicas > currentN {
		// Scale up: add new instances
		hostIDs := make([]string, 0, len(m.hosts))
		for hostID := range m.hosts {
			hostIDs = append(hostIDs, hostID)
		}
		if len(hostIDs) == 0 {
			return fmt.Errorf("no hosts available")
		}
		first := instances[0]
		cpuCores := first.CPUCores()
		memoryMB := first.MemoryMB()

		for i := 0; i < newReplicas-currentN; i++ {
			hostID := hostIDs[(currentN+i)%len(hostIDs)]
			instanceIDStr := fmt.Sprintf("%s-instance-%d", serviceID, m.nextInstanceID)
			m.nextInstanceID++

			instance := NewServiceInstance(instanceIDStr, serviceID, hostID, cpuCores, memoryMB)
			m.instances[instanceIDStr] = instance
			m.hosts[hostID].AddService(instanceIDStr)
			m.hostToInstances[hostID] = append(m.hostToInstances[hostID], instanceIDStr)
		}
		// Rebuild sorted cache for this service so new instances are included
		instances = m.getInstancesForServiceLocked(serviceID)
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].ID() < instances[j].ID()
		})
		m.sortedServiceInstMap[serviceID] = instances
	} else if newReplicas < currentN {
		// Soft scale down: only the first newReplicas instances receive new traffic
		m.sortedServiceInstMap[serviceID] = instances[:newReplicas]
		if m.roundRobinIdx[serviceID] >= newReplicas {
			m.roundRobinIdx[serviceID] = 0
		}
	}
	// else newReplicas == currentN: no-op

	return nil
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

// HostIDs returns a slice of all host IDs currently managed.
func (m *Manager) HostIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
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
		m.hosts[id] = host
	}

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
	if cpuCores < 0 || memoryMB < 0 {
		return fmt.Errorf("cpu_cores and memory_mb must be non-negative")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	instances := m.getInstancesForServiceLocked(serviceID)
	if len(instances) == 0 {
		return fmt.Errorf("service not found: %s", serviceID)
	}

	// First, check host capacity constraints for the proposed change.
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
	ids := make([]string, 0, len(m.sortedServiceInstMap))
	for id := range m.sortedServiceInstMap {
		ids = append(ids, id)
	}
	return ids
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
	m.updateHostCPUUtilizationWithData(host, instances)

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
	m.updateHostCPUUtilizationWithData(host, instances)
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
			return fmt.Errorf("host memory at capacity")
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

// updateHostCPUUtilizationWithData updates host CPU utilization by delegating to Host aggregation
func (m *Manager) updateHostCPUUtilizationWithData(host *Host, instances []*ServiceInstance) {
	sources := make([]InstanceUtilizationSource, len(instances))
	for i, inst := range instances {
		sources[i] = inst
	}
	host.UpdateCPUUtilization(sources)
}

// updateHostMemoryUtilizationWithData updates host memory utilization by delegating to Host aggregation
func (m *Manager) updateHostMemoryUtilizationWithData(host *Host, instances []*ServiceInstance) {
	sources := make([]InstanceUtilizationSource, len(instances))
	for i, inst := range instances {
		sources[i] = inst
	}
	host.UpdateMemoryUtilization(sources)
}
