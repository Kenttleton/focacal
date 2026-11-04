package config

import "context"

type ctxKey struct{}

// WithUserConfig returns a new context carrying a fixed uc.
func WithUserConfig(ctx context.Context, uc *UserConfig) context.Context {
	return context.WithValue(ctx, ctxKey{}, func() *UserConfig { return uc })
}

// WithUserConfigFunc returns a new context whose UserConfig is resolved by get
// on every UserConfigFromContext call, so long-lived contexts follow live
// config changes instead of holding a stale pointer.
func WithUserConfigFunc(ctx context.Context, get func() *UserConfig) context.Context {
	return context.WithValue(ctx, ctxKey{}, get)
}

// UserConfigFromContext retrieves the UserConfig from ctx.
// Panics if no UserConfig was injected — inject via WithUserConfig at startup
// (OSS) or per-request (cloud).
func UserConfigFromContext(ctx context.Context) *UserConfig {
	get, ok := ctx.Value(ctxKey{}).(func() *UserConfig)
	if !ok || get == nil {
		panic("config: no UserConfig in context — inject via config.WithUserConfig before use")
	}
	uc := get()
	if uc == nil {
		panic("config: no UserConfig in context — inject via config.WithUserConfig before use")
	}
	return uc
}
