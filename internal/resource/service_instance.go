package resource

import (
	"math"
	"sync"
	"time"
)

// InstanceLifecycle describes whether an instance accepts new work.
type InstanceLifecycle int

const (
	// InstanceActive is the normal state: instance participates in load balancing.
	InstanceActive InstanceLifecycle = iota
	// InstanceDraining means no new traffic is routed here; the instance is
	// removed once idle (or after a simulated-time drain deadline).
	InstanceDraining
)

// ServiceInstance represents a service instance with resource tracking
type ServiceInstance struct {
	mu sync.RWMutex

	id          string
	serviceName string
	hostID      string

	lifecycle InstanceLifecycle
	// drainDeadline is simulated-time after which the manager may force-remove
	// this instance even if still busy. Zero means not draining.
	drainDeadline time.Time

	// Resource allocation
	cpuCores float64 // Allocated CPU cores
	memoryMB float64 // Allocated memory in MB

	// Current usage with time-window tracking
	cpuUsageWindow   time.Duration // Time window for CPU utilization calculation (default 1 second)
	cpuUsageInWindow float64       // CPU time consumed in the current time window (ms)
	windowStartTime  time.Time     // Start of the current measurement window

	activeMemoryMB float64 // Memory currently in use (MB)
	activeRequests int     // Number of active requests

	// Queue
	requestQueue []string // Queue of request IDs waiting to be processed

	// cpuNextFree is simulation time when the next CPU work may begin (end of the last
	// reserved [cpuStart, cpuEnd) interval). Zero means no backlog from prior reservations.
	cpuNextFree time.Time

	// Timestamps
	lastUpdate time.Time
}

// NewServiceInstance creates a new service instance
func NewServiceInstance(id, serviceName, hostID string, cpuCores, memoryMB float64) *ServiceInstance {
	now := time.Now()
	return &ServiceInstance{
		id:               id,
		serviceName:      serviceName,
		hostID:           hostID,
		lifecycle:        InstanceActive,
		cpuCores:         cpuCores,
		memoryMB:         memoryMB,
		cpuUsageWindow:   1 * time.Second, // Default 1-second window for utilization calculation
		cpuUsageInWindow: 0,
		windowStartTime:  now,
		requestQueue:     make([]string, 0),
		lastUpdate:       now,
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

// Lifecycle returns active vs draining state.
func (s *ServiceInstance) Lifecycle() InstanceLifecycle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecycle
}

// IsRoutable returns true if this instance should receive new requests.
func (s *ServiceInstance) IsRoutable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lifecycle == InstanceActive
}

// DrainDeadline returns the simulated-time deadline for forced removal when draining.
func (s *ServiceInstance) DrainDeadline() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.drainDeadline
}

// SetDraining marks the instance as draining with a simulated-time deadline.
func (s *ServiceInstance) SetDraining(deadline time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycle = InstanceDraining
	s.drainDeadline = deadline
}

// SetCPUCores updates the allocated CPU cores for this instance.
func (s *ServiceInstance) SetCPUCores(cores float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cores < 0 {
		cores = 0
	}
	s.cpuCores = cores
}

// SetMemoryMB updates the allocated memory (MB) for this instance.
func (s *ServiceInstance) SetMemoryMB(memoryMB float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if memoryMB < 0 {
		memoryMB = 0
	}
	s.memoryMB = memoryMB
}

// CPUUtilization returns CPU utilization (0.0 to 1.0) based on a sliding time window,
// evaluated at wall-clock now. Prefer CPUUtilizationAt in discrete-event simulation.
func (s *ServiceInstance) CPUUtilization() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cpuUtilizationAtLocked(time.Now())
}

// CPUUtilizationAt returns utilization using the same sliding window as AllocateCPU, evaluated
// at at (simulation time). Capacity and gauge metrics in the simulator must use this clock, not
// time.Now(), or fast-forward runs treat CPU as idle while sim time is heavily loaded.
func (s *ServiceInstance) CPUUtilizationAt(at time.Time) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cpuUtilizationAtLocked(at)
}

// cpuUtilizationAtLocked calculates CPU utilization at currentTime. Caller must hold s.mu (RLock or Lock).
func (s *ServiceInstance) cpuUtilizationAtLocked(currentTime time.Time) float64 {
	if s.cpuCores == 0 {
		return 0.0
	}

	windowEnd := s.windowStartTime.Add(s.cpuUsageWindow)
	if currentTime.After(windowEnd) || currentTime.Equal(windowEnd) {
		return 0.0
	}

	windowDurationMs := float64(s.cpuUsageWindow.Milliseconds())
	if windowDurationMs == 0 {
		return 0.0
	}

	utilization := (s.cpuUsageInWindow / windowDurationMs) / s.cpuCores
	if utilization > 1.0 {
		return 1.0
	}
	if utilization < 0.0 {
		return 0.0
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

	// Check if we need to start a new window
	windowEnd := s.windowStartTime.Add(s.cpuUsageWindow)
	if simTime.After(windowEnd) || simTime.Equal(windowEnd) {
		// Start a new window
		s.windowStartTime = simTime
		s.cpuUsageInWindow = 0
	}

	// Add CPU time to the current window
	s.cpuUsageInWindow += cpuTimeMs

	s.activeRequests++
	s.lastUpdate = simTime
}

// ReleaseCPU releases CPU time for a request
func (s *ServiceInstance) ReleaseCPU(cpuTimeMs float64, simTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Note: We don't subtract from cpuUsageInWindow because the CPU time was
	// already consumed during processing. The window automatically resets
	// when we move past the window end time, implementing time-based decay.

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

// HasCapacity checks capacity at wall-clock now. Prefer HasCapacityAt for simulation.
func (s *ServiceInstance) HasCapacity() bool {
	return s.HasCapacityAt(time.Now())
}

// HasCapacityAt returns true if there is no CPU scheduler backlog at sim time at
// (next CPU work can start at or before at). Prefer this for placement heuristics;
// admission no longer gates RequestStart on this value.
func (s *ServiceInstance) HasCapacityAt(at time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cpuCores <= 0 {
		return false
	}
	if s.cpuNextFree.IsZero() {
		return true
	}
	return !s.cpuNextFree.After(at)
}

// ReserveCPUWork schedules one FIFO CPU service interval for cpuDemandMs of work.
// Service duration in simulation time is cpuDemandMs / max(cpuCores, ε) (single logical
// server with rate proportional to cores). Commits cpuNextFree to cpuEnd.
func (s *ServiceInstance) ReserveCPUWork(arrivalTime time.Time, cpuDemandMs float64) (cpuStart, cpuEnd time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cores := s.cpuCores
	if cores < 1e-9 {
		cores = 1e-9
	}
	durMs := cpuDemandMs / cores
	if durMs < 0 {
		durMs = 0
	}
	dur := time.Duration(math.Round(durMs * float64(time.Millisecond)))
	cpuStart = arrivalTime
	if !s.cpuNextFree.IsZero() && s.cpuNextFree.After(cpuStart) {
		cpuStart = s.cpuNextFree
	}
	cpuEnd = cpuStart.Add(dur)
	s.cpuNextFree = cpuEnd
	return cpuStart, cpuEnd
}

// RollbackCPUTailReservation reverts the last reservation if cpuEnd is still the tail
// of the schedule (cpuNextFree == cpuEnd). Used when memory allocation fails after ReserveCPUWork.
func (s *ServiceInstance) RollbackCPUTailReservation(cpuStart, cpuEnd time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cpuNextFree.IsZero() || !s.cpuNextFree.Equal(cpuEnd) {
		return
	}
	s.cpuNextFree = cpuStart
}

// ActiveMemoryMB returns the active memory usage in MB (for internal use)
func (s *ServiceInstance) ActiveMemoryMB() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeMemoryMB
}

// EvictResourceState clears in-flight and queued accounting so a draining replica can be
// removed on a hard drain timeout without leaving host utilization inconsistent.
// It returns request IDs that were waiting in the instance queue so callers can mark
// those requests failed. In-flight work is zeroed for host aggregation; completion
// handlers may still run and will no-op against a removed instance.
func (s *ServiceInstance) EvictResourceState(simTime time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dropped []string
	for _, id := range s.requestQueue {
		if id != "" {
			dropped = append(dropped, id)
		}
	}
	s.activeRequests = 0
	s.activeMemoryMB = 0
	s.requestQueue = nil
	s.cpuUsageInWindow = 0
	s.windowStartTime = simTime
	s.cpuNextFree = time.Time{}
	s.lastUpdate = simTime
	return dropped
}
