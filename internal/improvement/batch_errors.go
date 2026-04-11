package improvement

import "errors"

// ErrBatchBudgetExhausted is returned when max_evaluations would be exceeded.
var ErrBatchBudgetExhausted = errors.New("batch evaluation budget exhausted")
