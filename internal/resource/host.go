package resource

import (
	"sync"
)

// Host represents a physical or virtual host with capacity constraints
type Host struct {
	mu sync.RWMutex

	id       string
	cpuCores int
	memoryGB int

	// Current utilization (aggregated from all service instances)
	cpuUtilization    float64 // 0.0 to 1.0
	memoryUtilization float64 // 0.0 to 1.0

	// Service instances running on this host
	serviceInstances []string
}

// NewHost creates a new host
func NewHost(id string, cpuCores, memoryGB int) *Host {
	return &Host{
		id:               id,
		cpuCores:         cpuCores,
		memoryGB:         memoryGB,
		serviceInstances: make([]string, 0),
	}
}

// ID returns the host ID
func (h *Host) ID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.id
}

// CPUCores returns the number of CPU cores
func (h *Host) CPUCores() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuCores
}

// MemoryGB returns the memory in GB
func (h *Host) MemoryGB() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.memoryGB
}

// CPUUtilization returns CPU utilization (0.0 to 1.0)
func (h *Host) CPUUtilization() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuUtilization
}

// MemoryUtilization returns memory utilization (0.0 to 1.0)
func (h *Host) MemoryUtilization() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.memoryUtilization
}

// ServiceInstances returns the list of service instance IDs on this host
func (h *Host) ServiceInstances() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	instances := make([]string, len(h.serviceInstances))
	copy(instances, h.serviceInstances)
	return instances
}

// AddService adds a service instance to this host
func (h *Host) AddService(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serviceInstances = append(h.serviceInstances, instanceID)
}

// RemoveService removes a service instance from this host
func (h *Host) RemoveService(instanceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, id := range h.serviceInstances {
		if id == instanceID {
			h.serviceInstances = append(h.serviceInstances[:i], h.serviceInstances[i+1:]...)
			break
		}
	}
}

// UpdateCPUUtilization recalculates CPU utilization based on service instances
// This should be called after resource allocations change
func (h *Host) UpdateCPUUtilization() {
	// For now, we'll track this at the manager level
	// This method is a placeholder for future aggregation logic
	// The actual calculation happens in the Manager when allocating resources
}

// UpdateMemoryUtilization recalculates memory utilization based on service instances
func (h *Host) UpdateMemoryUtilization() {
	// For now, we'll track this at the manager level
	// This method is a placeholder for future aggregation logic
}

// SetCPUUtilization sets the CPU utilization (called by Manager)
func (h *Host) SetCPUUtilization(util float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if util < 0.0 {
		util = 0.0
	}
	if util > 1.0 {
		util = 1.0
	}
	h.cpuUtilization = util
}

// SetMemoryUtilization sets the memory utilization (called by Manager)
func (h *Host) SetMemoryUtilization(util float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if util < 0.0 {
		util = 0.0
	}
	if util > 1.0 {
		util = 1.0
	}
	h.memoryUtilization = util
}

// HasCapacity checks if the host has capacity for additional resources
func (h *Host) HasCapacity() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cpuUtilization < 1.0 && h.memoryUtilization < 1.0
}
