package emitter

import "context"

// Emitter is implemented by each output sink (CalDAV, REST, write-back).
type Emitter interface {
	// Name returns a short identifier used in logs.
	Name() string

	// Notify triggers the emitter to push the current state to its consumers.
	// Called after every successful planner cycle.
	Notify(ctx context.Context) error
}
