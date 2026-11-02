package config

import "github.com/spf13/viper"

// SetDefaults registers all default config values with viper.
// These are overridden by config.yaml, then by environment variables.
func SetDefaults() {
	// Working hours
	viper.SetDefault("working_hours.start", "09:00")
	viper.SetDefault("working_hours.end", "18:00")
	viper.SetDefault("working_hours.timezone", "UTC")

	// Lunch
	viper.SetDefault("lunch.preferred", "12:00")
	viper.SetDefault("lunch.window_start", "11:30")
	viper.SetDefault("lunch.window_end", "13:30")
	viper.SetDefault("lunch.duration_minutes", 60)

	// Focus
	viper.SetDefault("focus.style", "flexible")
	viper.SetDefault("focus.min_block_minutes", 30)
	viper.SetDefault("focus.max_block_minutes", 120)
	viper.SetDefault("focus.buffer_around_meetings", 10)
	viper.SetDefault("focus.pomodoro_work_minutes", 25)
	viper.SetDefault("focus.pomodoro_break_minutes", 5)

	// Scheduler agent — any OpenAI-compatible endpoint
	// Change base_url + model + api_key to use a different provider.
	viper.SetDefault("agent.base_url", "http://localhost:11434/v1")
	viper.SetDefault("agent.model", "qwen2.5:14b")
	viper.SetDefault("agent.api_key", "")
	viper.SetDefault("agent.timeout_secs", 120)
	viper.SetDefault("agent.max_tokens", 4096)

	// OSS auth — single static API key; auto-generated on first boot if empty
	viper.SetDefault("auth.enabled", true)
	viper.SetDefault("auth.api_key", "")

	// Servers
	viper.SetDefault("server.caldav_port", 5232)
	viper.SetDefault("server.rest_port", 5233)
	viper.SetDefault("server.mcp_port", 5234)
	viper.SetDefault("server.oauth_port", 8080)
	viper.SetDefault("server.bind_address", "0.0.0.0")
	viper.SetDefault("server.webhooks_enabled", false)

	// OAuth
	viper.SetDefault("oauth.redirect_base_url", "http://localhost:8080")
}
