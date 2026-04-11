package resource

import "errors"

// ErrHostMemoryCapacity is returned when a request cannot allocate memory on a host
// because host memory utilization would exceed capacity. Callers may treat this as an
// expected admission failure rather than a simulation bug.
var ErrHostMemoryCapacity = errors.New("host memory at capacity")
