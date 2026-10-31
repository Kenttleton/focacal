package config

import "context"

type ctxKey struct{}

// WithUserConfig returns a new context carrying uc.
func WithUserConfig(ctx context.Context, uc *UserConfig) context.Context {
	return context.WithValue(ctx, ctxKey{}, uc)
}

// UserConfigFromContext retrieves the UserConfig from ctx.
// Panics if no UserConfig was injected — inject via WithUserConfig at startup
// (OSS) or per-request (cloud).
func UserConfigFromContext(ctx context.Context) *UserConfig {
	uc, ok := ctx.Value(ctxKey{}).(*UserConfig)
	if !ok || uc == nil {
		panic("config: no UserConfig in context — inject via config.WithUserConfig before use")
	}
	return uc
}
