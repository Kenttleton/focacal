package store

import "context"

type ctxKey struct{}

// WithStore returns a new context carrying s.
func WithStore(ctx context.Context, s Store) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext retrieves the Store from ctx.
// Panics if no Store was injected — this is a programmer error, not a
// runtime condition; callers must always inject via WithStore at startup
// (OSS) or per-request (cloud).
func FromContext(ctx context.Context) Store {
	s, ok := ctx.Value(ctxKey{}).(Store)
	if !ok || s == nil {
		panic("store: no Store in context — inject via store.WithStore before use")
	}
	return s
}
