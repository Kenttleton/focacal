package config

import (
	"fmt"
	"os"
	"time"
)

// FocacalSource is the built-in source id used for Focacal-generated events.
const FocacalSource = "focacal"

// Config is the top-level focacal configuration used by single-user OSS
// deployments. It is the union of GlobalConfig and UserConfig; both embed
// inline, so the yaml keys of the two must stay disjoint.
type Config struct {
	UserConfig   `yaml:",inline"`
	GlobalConfig `yaml:",inline"`
}

// UserConfig holds settings that belong to an individual user.
// In OSS single-user mode these come from the domain files.
// In the cloud these are loaded per-user from the database.
type UserConfig struct {
	WorkingHours WorkingHours        `yaml:"working_hours"`
	Lunch        LunchConfig         `yaml:"lunch"`
	Focus        FocusConfig         `yaml:"focus"`
	Sources      []SourceConfig      `yaml:"sources"`
	Destinations []DestinationConfig `yaml:"destinations"`
	Colors       map[string]string   `yaml:"colors"`
}

// GlobalConfig holds deployment-level settings shared across all users.
// Never stored per-user.
type GlobalConfig struct {
	Agent  AgentConfig  `yaml:"agent"`
	Server ServerConfig `yaml:"server"`
	OAuth  OAuthConfig  `yaml:"oauth"`
	Auth   AuthConfig   `yaml:"auth"`
}

// WorkingHours defines when the user is available.
type WorkingHours struct {
	Start    string `json:"start" yaml:"start"`       // "09:00"
	End      string `json:"end" yaml:"end"`           // "18:00"
	Timezone string `json:"timezone" yaml:"timezone"` // "America/New_York"
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
	Preferred       string `json:"preferred" yaml:"preferred"`               // "12:00"
	WindowStart     string `json:"window_start" yaml:"window_start"`         // "11:30"
	WindowEnd       string `json:"window_end" yaml:"window_end"`             // "13:30"
	DurationMinutes int    `json:"duration_minutes" yaml:"duration_minutes"` // 60
}

type FocusConfig struct {
	Style                string `json:"style" yaml:"style"`                                   // flexible | pomodoro | fixed
	MinBlockMinutes      int    `json:"min_block_minutes" yaml:"min_block_minutes"`           // 30
	MaxBlockMinutes      int    `json:"max_block_minutes" yaml:"max_block_minutes"`           // 120
	BufferAroundMeetings int    `json:"buffer_around_meetings" yaml:"buffer_around_meetings"` // 10
	PomodoroWorkMinutes  int    `json:"pomodoro_work_minutes" yaml:"pomodoro_work_minutes"`   // 25
	PomodoroBreakMinutes int    `json:"pomodoro_break_minutes" yaml:"pomodoro_break_minutes"` // 5
}

// AgentConfig points the scheduler at any OpenAI-compatible chat completions
// endpoint. Works with Ollama, OpenAI, Anthropic, Azure OpenAI, GitHub Models,
// Groq, and others.
type AgentConfig struct {
	BaseURL     string `json:"base_url" yaml:"base_url"`         // e.g. http://localhost:11434/v1
	Model       string `json:"model" yaml:"model"`               // e.g. qwen2.5:14b, gpt-4o, claude-sonnet-4-6
	APIKey      string `json:"api_key" yaml:"api_key"`           // leave empty for local/unauthenticated endpoints
	TimeoutSecs int    `json:"timeout_secs" yaml:"timeout_secs"` // 120
	MaxTokens   int    `json:"max_tokens" yaml:"max_tokens"`     // 4096
}

// SourceConfig describes a single calendar account (Google, Outlook, CalDAV, ICS).
type SourceConfig struct {
	ID              string         `json:"id" yaml:"id"`                                       // unique slug, e.g. "work-google"
	Label           string         `json:"label" yaml:"label,omitempty"`                       // free-text display name
	Type            string         `json:"type" yaml:"type"`                                   // google | outlook | caldav | ics
	CredentialsFile string         `json:"credentials_file" yaml:"credentials_file,omitempty"` // path inside container
	TokenFile       string         `json:"token_file" yaml:"token_file,omitempty"`             // path inside container (runtime)
	CalendarIDs     []string       `json:"calendar_ids" yaml:"calendar_ids,omitempty"`         // empty = all/primary
	Conflicts       ConflictConfig `json:"conflicts" yaml:"conflicts,omitempty"`

	// CalDAV-specific fields (type: caldav)
	CalDAVURL string `json:"caldav_url" yaml:"caldav_url,omitempty"` // e.g. https://caldav.icloud.com
	Username  string `json:"username" yaml:"username,omitempty"`     // Basic Auth (iCloud: Apple ID email)
	Password  string `json:"password" yaml:"password,omitempty"`     // Basic Auth password or app-specific password
	AuthType  string `json:"auth_type" yaml:"auth_type,omitempty"`   // basic (default) | bearer

	// ICS-specific fields (type: ics)
	ICSURL    string `json:"ics_url" yaml:"ics_url,omitempty"`         // secret feed URL
	ICSURLEnv string `json:"ics_url_env" yaml:"ics_url_env,omitempty"` // env var holding the feed URL
}

// ConflictConfig holds per-source switches for what counts as a conflict.
// Nil means the documented default.
type ConflictConfig struct {
	Declined  *bool `json:"declined" yaml:"declined,omitempty"`
	Tentative *bool `json:"tentative" yaml:"tentative,omitempty"`
	Free      *bool `json:"free" yaml:"free,omitempty"`
	AllDay    *bool `json:"all_day" yaml:"all_day,omitempty"`
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
	ID              string              `json:"id" yaml:"id"`
	CalendarID      string              `json:"calendar_id" yaml:"calendar_id,omitempty"`
	Type            string              `json:"type" yaml:"type"` // export-only connection type
	CredentialsFile string              `json:"credentials_file" yaml:"credentials_file,omitempty"`
	TokenFile       string              `json:"token_file" yaml:"token_file,omitempty"`
	CalDAVURL       string              `json:"caldav_url" yaml:"caldav_url,omitempty"`
	Username        string              `json:"username" yaml:"username,omitempty"`
	Password        string              `json:"password" yaml:"password,omitempty"`
	AuthType        string              `json:"auth_type" yaml:"auth_type,omitempty"`
	ICSURL          string              `json:"ics_url" yaml:"ics_url,omitempty"`
	ICSURLEnv       string              `json:"ics_url_env" yaml:"ics_url_env,omitempty"`
	CalendarIDs     []string            `json:"calendar_ids" yaml:"calendar_ids,omitempty"`
	Sources         []DestinationSource `json:"sources" yaml:"sources,omitempty"`
}

// DestinationSource refers to an input calendar (or focacal) shown in a destination.
type DestinationSource struct {
	ID         string          `json:"id" yaml:"id"`
	CalendarID string          `json:"calendar_id" yaml:"calendar_id,omitempty"`
	Event      EventVisibility `json:"event" yaml:"event"`
}

// EventVisibility holds title/body visibility for a destination source entry.
type EventVisibility struct {
	Title FieldVisibility `json:"title" yaml:"title"`
	Body  FieldVisibility `json:"body" yaml:"body"`
}

// FieldVisibility controls whether and how a field is shown.
type FieldVisibility struct {
	Visibility bool   `json:"visibility" yaml:"visibility"`
	Rename     string `json:"rename" yaml:"rename,omitempty"`
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
	if s.ICSURLEnv != "" {
		return "", fmt.Errorf("environment variable %s (ics_url_env) is not set", s.ICSURLEnv)
	}
	return "", fmt.Errorf("ics_url_env (or ics_url) is required")
}

type ServerConfig struct {
	CalDAVPort      int    `json:"caldav_port" yaml:"caldav_port"`   // 5232
	RESTPort        int    `json:"rest_port" yaml:"rest_port"`       // 5233
	MCPPort         int    `json:"mcp_port" yaml:"mcp_port"`         // 5234
	OAuthPort       int    `json:"oauth_port" yaml:"oauth_port"`     // 8080
	BindAddress     string `json:"bind_address" yaml:"bind_address"` // 0.0.0.0
	WebhooksEnabled bool   `json:"webhooks_enabled" yaml:"webhooks_enabled"`
}

type OAuthConfig struct {
	RedirectBaseURL string `json:"redirect_base_url" yaml:"redirect_base_url"` // http://localhost:8080
}

// AuthConfig controls the lightweight OSS authentication layer.
// Set enabled=false for trusted LAN deployments that need no protection.
type AuthConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"` // default true
	APIKey  string `json:"api_key" yaml:"api_key"` // legacy static key (backward compat)
	// Login credentials (username, password_hash) live in credentials.yaml;
	// see LoadCredentials.
}

func parseTOD(s string) (time.Duration, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("invalid time-of-day %q: %w", s, err)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}
