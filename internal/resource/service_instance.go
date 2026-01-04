package resource

import (
	"sync"
	"time"
)

// ServiceInstance represents a service instance with resource tracking
type ServiceInstance struct {
	mu sync.RWMutex

	id          string
	serviceName string
	hostID      string

	// Resource allocation
	cpuCores float64 // Allocated CPU cores
	memoryMB float64 // Allocated memory in MB

	// Current usage
	activeCPUTimeMs float64 // Total CPU time currently in use (ms)
	activeMemoryMB  float64 // Memory currently in use (MB)
	activeRequests  int     // Number of active requests

	// Queue
	requestQueue []string // Queue of request IDs waiting to be processed

	// Timestamps
	lastUpdate time.Time
}

// NewServiceInstance creates a new service instance
func NewServiceInstance(id, serviceName, hostID string, cpuCores, memoryMB float64) *ServiceInstance {
	return &ServiceInstance{
		id:           id,
		serviceName:  serviceName,
		hostID:       hostID,
		cpuCores:     cpuCores,
		memoryMB:     memoryMB,
		requestQueue: make([]string, 0),
		lastUpdate:   time.Now(),
	}
}

// ID returns the instance ID
func (s *ServiceInstance) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// ServiceName returns the service name
func (s *ServiceInstance) ServiceName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serviceName
}

// HostID returns the host ID
func (s *ServiceInstance) HostID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostID
}

// CPUCores returns the allocated CPU cores
func (s *ServiceInstance) CPUCores() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cpuCores
}

// MemoryMB returns the allocated memory in MB
func (s *ServiceInstance) MemoryMB() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memoryMB
}

// CPUUtilization returns CPU utilization (0.0 to 1.0)
func (s *ServiceInstance) CPUUtilization() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cpuCores == 0 {
		return 0.0
	}
	// Utilization = active CPU time per second / allocated cores
	// activeCPUTimeMs is in milliseconds, convert to cores per second
	utilization := (s.activeCPUTimeMs / 1000.0) / s.cpuCores
	if utilization > 1.0 {
		return 1.0
	}
	return utilization
}

// MemoryUtilization returns memory utilization (0.0 to 1.0)
func (s *ServiceInstance) MemoryUtilization() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.memoryMB == 0 {
		return 0.0
	}
	return s.activeMemoryMB / s.memoryMB
}

// ActiveRequests returns the number of active requests
func (s *ServiceInstance) ActiveRequests() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeRequests
}

// AllocateCPU allocates CPU time for a request
func (s *ServiceInstance) AllocateCPU(cpuTimeMs float64, simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCPUTimeMs += cpuTimeMs
	s.activeRequests++
	s.lastUpdate = simTime
}

// ReleaseCPU releases CPU time for a request
func (s *ServiceInstance) ReleaseCPU(cpuTimeMs float64, simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeCPUTimeMs >= cpuTimeMs {
		s.activeCPUTimeMs -= cpuTimeMs
	}
	if s.activeRequests > 0 {
		s.activeRequests--
	}
	s.lastUpdate = simTime
}

// AllocateMemory allocates memory
func (s *ServiceInstance) AllocateMemory(memoryMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeMemoryMB += memoryMB
}

// ReleaseMemory releases memory
func (s *ServiceInstance) ReleaseMemory(memoryMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeMemoryMB >= memoryMB {
		s.activeMemoryMB -= memoryMB
	}
}

// EnqueueRequest adds a request to the queue
func (s *ServiceInstance) EnqueueRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestQueue = append(s.requestQueue, requestID)
}

// DequeueRequest removes and returns the next request from the queue
func (s *ServiceInstance) DequeueRequest() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requestQueue) == 0 {
		return "", false
	}
	requestID := s.requestQueue[0]
	// Clear the reference to the dequeued element to avoid retaining it in the backing array.
	s.requestQueue[0] = ""
	if len(s.requestQueue) == 1 {
		// When the queue becomes empty, release the backing array.
		s.requestQueue = nil
	} else {
		s.requestQueue = s.requestQueue[1:]
	}
	return requestID, true
}

// QueueLength returns the current queue length
func (s *ServiceInstance) QueueLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.requestQueue)
}

// HasCapacity checks if the instance has capacity for a new request
func (s *ServiceInstance) HasCapacity() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// If no CPU cores are allocated, this instance cannot process requests.
	if s.cpuCores <= 0 {
		return false
	}

	// Simple check: if CPU utilization is below 100%, we have capacity.
	// We compute utilization directly from the guarded fields to avoid
	// calling CPUUtilization() (which also acquires a lock) while holding
	// the read lock.
	utilization := s.activeCPUTimeMs / s.cpuCores
	return utilization < 1.0
}

// ActiveMemoryMB returns the active memory usage in MB (for internal use)
func (s *ServiceInstance) ActiveMemoryMB() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeMemoryMB
}
