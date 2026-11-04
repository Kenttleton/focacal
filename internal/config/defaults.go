package config

import "github.com/spf13/viper"

// applyDefaults registers all default config values with v. They are
// overridden by the domain files, then (for the running config only) by
// environment variables; see LoadStart.
func applyDefaults(v *viper.Viper) {
	// Working hours
	v.SetDefault("working_hours.start", "09:00")
	v.SetDefault("working_hours.end", "18:00")
	v.SetDefault("working_hours.timezone", "UTC")

	// Lunch
	v.SetDefault("lunch.preferred", "12:00")
	v.SetDefault("lunch.window_start", "11:30")
	v.SetDefault("lunch.window_end", "13:30")
	v.SetDefault("lunch.duration_minutes", 60)

	// Focus
	v.SetDefault("focus.style", "flexible")
	v.SetDefault("focus.min_block_minutes", 30)
	v.SetDefault("focus.max_block_minutes", 120)
	v.SetDefault("focus.buffer_around_meetings", 10)
	v.SetDefault("focus.pomodoro_work_minutes", 25)
	v.SetDefault("focus.pomodoro_break_minutes", 5)

	// Scheduler agent — any OpenAI-compatible endpoint
	// Change base_url + model + api_key to use a different provider.
	v.SetDefault("agent.base_url", "http://localhost:11434/v1")
	v.SetDefault("agent.model", "qwen2.5:14b")
	v.SetDefault("agent.api_key", "")
	v.SetDefault("agent.timeout_secs", 120)
	v.SetDefault("agent.max_tokens", 4096)

	// OSS auth — single static API key; auto-generated on first boot if empty
	v.SetDefault("auth.enabled", true)
	v.SetDefault("auth.api_key", "")

	// Servers
	v.SetDefault("server.caldav_port", 5232)
	v.SetDefault("server.rest_port", 5233)
	v.SetDefault("server.mcp_port", 5234)
	v.SetDefault("server.oauth_port", 8080)
	v.SetDefault("server.bind_address", "0.0.0.0")
	v.SetDefault("server.webhooks_enabled", false)

	// OAuth
	v.SetDefault("oauth.redirect_base_url", "http://localhost:8080")
}
