package interaction

import (
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// CallMode represents the mode of a downstream call
type CallMode string

const (
	// CallModeSync represents a synchronous call (blocks until completion)
	CallModeSync CallMode = "sync"
	// CallModeAsync represents an asynchronous call (fire-and-forget)
	CallModeAsync CallMode = "async"
)

// CallSemantics manages the semantics of service calls
type CallSemantics struct {
	mode CallMode
}

// NewCallSemantics creates new call semantics with the specified mode
func NewCallSemantics(mode CallMode) *CallSemantics {
	return &CallSemantics{
		mode: mode,
	}
}

// IsSync returns true if the call is synchronous
func (cs *CallSemantics) IsSync() bool {
	return cs.mode == CallModeSync
}

// IsAsync returns true if the call is asynchronous
func (cs *CallSemantics) IsAsync() bool {
	return cs.mode == CallModeAsync
}

// Mode returns the call mode
func (cs *CallSemantics) Mode() CallMode {
	return cs.mode
}

// CallContext represents the context of a downstream call
type CallContext struct {
	ParentRequest   *models.Request
	DownstreamCall  ResolvedCall
	CallTime        time.Time
	Semantics       *CallSemantics
	CompletionTime  time.Time // For sync calls, when the downstream call completes
	Blocking        bool      // Whether this call blocks the parent request
}

// NewCallContext creates a new call context
func NewCallContext(parentRequest *models.Request, downstreamCall ResolvedCall, callTime time.Time, semantics *CallSemantics) *CallContext {
	return &CallContext{
		ParentRequest:  parentRequest,
		DownstreamCall: downstreamCall,
		CallTime:       callTime,
		Semantics:      semantics,
		Blocking:       semantics.IsSync(),
	}
}

// SetCompletionTime sets the completion time for the call
func (cc *CallContext) SetCompletionTime(completionTime time.Time) {
	cc.CompletionTime = completionTime
}

// Duration returns the duration of the call
func (cc *CallContext) Duration() time.Duration {
	if cc.CompletionTime.IsZero() {
		return 0
	}
	return cc.CompletionTime.Sub(cc.CallTime)
}

