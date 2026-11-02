package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// FocacalSource is the built-in source id used for Focacal-generated events.
const FocacalSource = "focacal"

// Config is the top-level focacal configuration used by single-user OSS
// deployments. It is the union of GlobalConfig and UserConfig.
type Config struct {
	UserConfig   `mapstructure:",squash"`
	GlobalConfig `mapstructure:",squash"`
}

// UserConfig holds settings that belong to an individual user.
// In OSS single-user mode these come from config.yaml.
// In the cloud these are loaded per-user from the database.
type UserConfig struct {
	WorkingHours WorkingHours      `mapstructure:"working_hours"`
	Lunch        LunchConfig       `mapstructure:"lunch"`
	Focus        FocusConfig       `mapstructure:"focus"`
	Sources      []SourceConfig    `mapstructure:"sources"`
	Destinations []DestinationConfig `mapstructure:"destinations"`
	Colors       map[string]string `mapstructure:"colors"`
	Writeback    WritebackConfig   `mapstructure:"writeback"`
}

// GlobalConfig holds deployment-level settings shared across all users.
// Never stored per-user.
type GlobalConfig struct {
	Agent  AgentConfig  `mapstructure:"agent"`
	Server ServerConfig `mapstructure:"server"`
	OAuth  OAuthConfig  `mapstructure:"oauth"`
	Auth   AuthConfig   `mapstructure:"auth"`
}

// WorkingHours defines when the user is available.
type WorkingHours struct {
	Start    string `mapstructure:"start"`    // "09:00"
	End      string `mapstructure:"end"`      // "18:00"
	Timezone string `mapstructure:"timezone"` // "America/New_York"
}

// StartTime parses WorkingHours.Start as a time.Duration offset from midnight.
func (w WorkingHours) StartTime() (time.Duration, error) {
	return parseTOD(w.Start)
}

func (w WorkingHours) EndTime() (time.Duration, error) {
	return parseTOD(w.End)
}

func (w WorkingHours) Location() (*time.Location, error) {
	return time.LoadLocation(w.Timezone)
}

type LunchConfig struct {
	Preferred       string `mapstructure:"preferred"`        // "12:00"
	WindowStart     string `mapstructure:"window_start"`     // "11:30"
	WindowEnd       string `mapstructure:"window_end"`       // "13:30"
	DurationMinutes int    `mapstructure:"duration_minutes"` // 60
}

type FocusConfig struct {
	Style                string `mapstructure:"style"`                    // flexible | pomodoro | fixed
	MinBlockMinutes      int    `mapstructure:"min_block_minutes"`        // 30
	MaxBlockMinutes      int    `mapstructure:"max_block_minutes"`        // 120
	BufferAroundMeetings int    `mapstructure:"buffer_around_meetings"`   // 10
	PomodoroWorkMinutes  int    `mapstructure:"pomodoro_work_minutes"`    // 25
	PomodoroBreakMinutes int    `mapstructure:"pomodoro_break_minutes"`   // 5
}

// AgentConfig points the scheduler at any OpenAI-compatible chat completions
// endpoint. Works with Ollama, OpenAI, Anthropic, Azure OpenAI, GitHub Models,
// Groq, and others.
type AgentConfig struct {
	BaseURL     string `mapstructure:"base_url"`     // e.g. http://localhost:11434/v1
	Model       string `mapstructure:"model"`        // e.g. qwen2.5:14b, gpt-4o, claude-sonnet-4-6
	APIKey      string `mapstructure:"api_key"`      // leave empty for local/unauthenticated endpoints
	TimeoutSecs int    `mapstructure:"timeout_secs"` // 120
	MaxTokens   int    `mapstructure:"max_tokens"`   // 4096
}

// SourceConfig describes a single calendar account (Google, Outlook, CalDAV, ICS).
type SourceConfig struct {
	ID              string         `mapstructure:"id"`               // unique slug, e.g. "work-google"
	Label           string         `mapstructure:"label"`            // free-text display name
	Type            string         `mapstructure:"type"`             // google | outlook | caldav | ics
	Writeback       bool           `mapstructure:"writeback"`        // legacy; removed in Task 2
	CrossBusy       bool           `mapstructure:"cross_busy"`       // legacy; removed in Task 2
	CredentialsFile string         `mapstructure:"credentials_file"` // path inside container
	TokenFile       string         `mapstructure:"token_file"`       // path inside container (runtime)
	CalendarIDs     []string       `mapstructure:"calendar_ids"`     // empty = all/primary
	Conflicts       ConflictConfig `mapstructure:"conflicts"`

	// CalDAV-specific fields (type: caldav)
	CalDAVURL string `mapstructure:"caldav_url"`  // e.g. https://caldav.icloud.com
	Username  string `mapstructure:"username"`    // Basic Auth (iCloud: Apple ID email)
	Password  string `mapstructure:"password"`    // Basic Auth password or app-specific password
	AuthType  string `mapstructure:"auth_type"`   // basic (default) | bearer

	// ICS-specific fields (type: ics)
	ICSURL    string `mapstructure:"ics_url"`     // secret feed URL
	ICSURLEnv string `mapstructure:"ics_url_env"` // env var holding the feed URL
}

// ConflictConfig holds per-source switches for what counts as a conflict.
// Nil means the documented default.
type ConflictConfig struct {
	Declined *bool `mapstructure:"declined"`
	Tentative *bool `mapstructure:"tentative"`
	Free     *bool `mapstructure:"free"`
	AllDay   *bool `mapstructure:"all_day"`
}

func (c ConflictConfig) CountsDeclined() bool {
	if c.Declined == nil {
		return false
	}
	return *c.Declined
}

func (c ConflictConfig) CountsTentative() bool {
	if c.Tentative == nil {
		return true
	}
	return *c.Tentative
}

func (c ConflictConfig) CountsFree() bool {
	if c.Free == nil {
		return false
	}
	return *c.Free
}

func (c ConflictConfig) CountsAllDay() bool {
	if c.AllDay == nil {
		return false
	}
	return *c.AllDay
}

// DestinationConfig describes an output destination calendar.
type DestinationConfig struct {
	ID              string              `mapstructure:"id"`
	CalendarID      string              `mapstructure:"calendar_id"`
	Type            string              `mapstructure:"type"`             // export-only connection type
	CredentialsFile string              `mapstructure:"credentials_file"`
	TokenFile       string              `mapstructure:"token_file"`
	CalDAVURL       string              `mapstructure:"caldav_url"`
	Username        string              `mapstructure:"username"`
	Password        string              `mapstructure:"password"`
	AuthType        string              `mapstructure:"auth_type"`
	ICSURL          string              `mapstructure:"ics_url"`
	ICSURLEnv       string              `mapstructure:"ics_url_env"`
	CalendarIDs     []string            `mapstructure:"calendar_ids"`
	Sources         []DestinationSource `mapstructure:"sources"`
}

// DestinationSource refers to an input calendar (or focacal) shown in a destination.
type DestinationSource struct {
	ID     string          `mapstructure:"id"`
	CalendarID string       `mapstructure:"calendar_id"`
	Event  EventVisibility `mapstructure:"event"`
}

// EventVisibility holds title/body visibility for a destination source entry.
type EventVisibility struct {
	Title FieldVisibility `mapstructure:"title"`
	Body  FieldVisibility `mapstructure:"body"`
}

// FieldVisibility controls whether and how a field is shown.
type FieldVisibility struct {
	Visibility bool   `mapstructure:"visibility"`
	Rename     string `mapstructure:"rename"`
}

// WritebackConfig controls how planned blocks appear on source calendars.
type WritebackConfig struct {
	CrossBusyTitle string         `mapstructure:"cross_busy_title"` // "Meeting"
	Focus          BlockWriteback `mapstructure:"focus"`
	Lunch          BlockWriteback `mapstructure:"lunch"`
	Break          BlockWriteback `mapstructure:"break"`
}

type BlockWriteback struct {
	Enabled     bool   `mapstructure:"enabled"`
	Title       string `mapstructure:"title"`
	ShowAs      string `mapstructure:"show_as"` // busy | free | tentative
	Description string `mapstructure:"description"`
}

// EffectiveCalendarIDs returns the declared list, or the type default when empty.
func (s SourceConfig) EffectiveCalendarIDs() []string {
	if len(s.CalendarIDs) > 0 {
		out := make([]string, len(s.CalendarIDs))
		copy(out, s.CalendarIDs)
		return out
	}
	switch s.Type {
	case "google", "outlook":
		return []string{"primary"}
	case "ics":
		return []string{"default"}
	}
	return nil
}

// ResolveICSURL returns the feed URL, preferring the env var when set and non-empty.
func (s SourceConfig) ResolveICSURL() (string, error) {
	if s.ICSURLEnv != "" {
		if v := os.Getenv(s.ICSURLEnv); v != "" {
			return v, nil
		}
	}
	if s.ICSURL != "" {
		return s.ICSURL, nil
	}
	return "", fmt.Errorf("ics_url is required")
}

type ServerConfig struct {
	CalDAVPort      int  `mapstructure:"caldav_port"`  // 5232
	RESTPort        int  `mapstructure:"rest_port"`    // 5233
	MCPPort         int  `mapstructure:"mcp_port"`     // 5234
	OAuthPort       int  `mapstructure:"oauth_port"`   // 8080
	BindAddress     string `mapstructure:"bind_address"` // 0.0.0.0
	WebhooksEnabled bool   `mapstructure:"webhooks_enabled"`
}

type OAuthConfig struct {
	RedirectBaseURL string `mapstructure:"redirect_base_url"` // http://localhost:8080
}

// AuthConfig controls the lightweight OSS authentication layer.
// Set enabled=false for trusted LAN deployments that need no protection.
type AuthConfig struct {
	Enabled bool   `mapstructure:"enabled"` // default true
	APIKey  string `mapstructure:"api_key"` // auto-generated on first boot if empty
}

// Load reads and unmarshals config from viper.
func Load() (*Config, error) {
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parseTOD(s string) (time.Duration, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("invalid time-of-day %q: %w", s, err)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}