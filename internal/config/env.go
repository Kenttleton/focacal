package config

import (
	"errors"
	"fmt"
	"strconv"
)

// ApplyEnv overrides c from the environment: FOCACAL_<KEY> with the dots of
// the config key written as underscores (FOCACAL_SERVER_REST_PORT), plus
// OAUTH_REDIRECT_BASE_URL. getenv returns "" for an unset variable, which
// leaves the value alone. An int or bool that does not parse is an error
// naming the variable (never its value, which may be a secret); the file
// value is kept for it. Only the running config gets this; see LoadStart.
func ApplyEnv(c *Config, getenv func(string) string) error {
	var errs []error
	str := func(name string, dst *string) {
		if v := getenv(name); v != "" {
			*dst = v
		}
	}
	num := func(name string, dst *int) {
		if v := getenv(name); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			} else {
				errs = append(errs, fmt.Errorf("%s: not an integer (value not shown)", name))
			}
		}
	}
	flag := func(name string, dst *bool) {
		if v := getenv(name); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			} else {
				errs = append(errs, fmt.Errorf("%s: not a boolean (value not shown)", name))
			}
		}
	}

	str("FOCACAL_WORKING_HOURS_START", &c.WorkingHours.Start)
	str("FOCACAL_WORKING_HOURS_END", &c.WorkingHours.End)
	str("FOCACAL_WORKING_HOURS_TIMEZONE", &c.WorkingHours.Timezone)

	str("FOCACAL_LUNCH_PREFERRED", &c.Lunch.Preferred)
	str("FOCACAL_LUNCH_WINDOW_START", &c.Lunch.WindowStart)
	str("FOCACAL_LUNCH_WINDOW_END", &c.Lunch.WindowEnd)
	num("FOCACAL_LUNCH_DURATION_MINUTES", &c.Lunch.DurationMinutes)

	str("FOCACAL_FOCUS_STYLE", &c.Focus.Style)
	num("FOCACAL_FOCUS_MIN_BLOCK_MINUTES", &c.Focus.MinBlockMinutes)
	num("FOCACAL_FOCUS_MAX_BLOCK_MINUTES", &c.Focus.MaxBlockMinutes)
	num("FOCACAL_FOCUS_BUFFER_AROUND_MEETINGS", &c.Focus.BufferAroundMeetings)
	num("FOCACAL_FOCUS_POMODORO_WORK_MINUTES", &c.Focus.PomodoroWorkMinutes)
	num("FOCACAL_FOCUS_POMODORO_BREAK_MINUTES", &c.Focus.PomodoroBreakMinutes)

	str("FOCACAL_AGENT_BASE_URL", &c.Agent.BaseURL)
	str("FOCACAL_AGENT_MODEL", &c.Agent.Model)
	str("FOCACAL_AGENT_API_KEY", &c.Agent.APIKey)
	num("FOCACAL_AGENT_TIMEOUT_SECS", &c.Agent.TimeoutSecs)
	num("FOCACAL_AGENT_MAX_TOKENS", &c.Agent.MaxTokens)

	flag("FOCACAL_AUTH_ENABLED", &c.Auth.Enabled)
	str("FOCACAL_AUTH_API_KEY", &c.Auth.APIKey)

	num("FOCACAL_SERVER_CALDAV_PORT", &c.Server.CalDAVPort)
	num("FOCACAL_SERVER_REST_PORT", &c.Server.RESTPort)
	num("FOCACAL_SERVER_MCP_PORT", &c.Server.MCPPort)
	num("FOCACAL_SERVER_OAUTH_PORT", &c.Server.OAuthPort)
	str("FOCACAL_SERVER_BIND_ADDRESS", &c.Server.BindAddress)
	flag("FOCACAL_SERVER_WEBHOOKS_ENABLED", &c.Server.WebhooksEnabled)

	str("FOCACAL_OAUTH_REDIRECT_BASE_URL", &c.OAuth.RedirectBaseURL)
	str("OAUTH_REDIRECT_BASE_URL", &c.OAuth.RedirectBaseURL) // wins over the prefixed form, as before
	return errors.Join(errs...)
}
