package config

// Defaults returns the built-in config values. Domain files are decoded over
// them (a key absent from every file keeps its default), then, for the
// running config only, ApplyEnv overrides them; see LoadStart.
func Defaults() *Config {
	return &Config{
		UserConfig: UserConfig{
			WorkingHours: WorkingHours{Start: "09:00", End: "18:00", Timezone: "UTC"},
			Lunch:        LunchConfig{Preferred: "12:00", WindowStart: "11:30", WindowEnd: "13:30", DurationMinutes: 60},
			Focus: FocusConfig{
				Style:                "flexible",
				MinBlockMinutes:      30,
				MaxBlockMinutes:      120,
				BufferAroundMeetings: 10,
				PomodoroWorkMinutes:  25,
				PomodoroBreakMinutes: 5,
			},
		},
		GlobalConfig: GlobalConfig{
			// Scheduler agent: any OpenAI-compatible endpoint. Change
			// base_url + model + api_key to use a different provider.
			Agent: AgentConfig{BaseURL: "http://localhost:11434/v1", Model: "qwen2.5:14b", TimeoutSecs: 120, MaxTokens: 4096},
			// OSS auth: single static API key; auto-generated on first boot if empty.
			Auth: AuthConfig{Enabled: true},
			Server: ServerConfig{
				CalDAVPort:  5232,
				RESTPort:    5233,
				MCPPort:     5234,
				OAuthPort:   8080,
				BindAddress: "0.0.0.0",
			},
			OAuth: OAuthConfig{RedirectBaseURL: "http://localhost:8080"},
		},
	}
}
