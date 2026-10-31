package bus

import "context"

// Dispatcher is the bus abstraction used by handlers, MCP tools, and emitters.
//
// OSS deployments inject the real *Bus at startup.
// Cloud deployments inject a scoped implementation per request that
// stamps messages with the authenticated user's ID internally — callers
// never see or pass a userID.
type Dispatcher interface {
	Publish(msg Message)
	PublishImmediate(msg Message)
	Subscribe(h Handler)
}

// Compile-time check: *Bus must satisfy Dispatcher.
var _ Dispatcher = (*Bus)(nil)

type ctxKey struct{}

// WithDispatcher returns a new context carrying d.
func WithDispatcher(ctx context.Context, d Dispatcher) context.Context {
	return context.WithValue(ctx, ctxKey{}, d)
}

// FromContext retrieves the Dispatcher from ctx.
// Panics if no Dispatcher was injected — this is a programmer error.
func FromContext(ctx context.Context) Dispatcher {
	d, ok := ctx.Value(ctxKey{}).(Dispatcher)
	if !ok || d == nil {
		panic("bus: no Dispatcher in context — inject via bus.WithDispatcher before use")
	}
	return d
}
