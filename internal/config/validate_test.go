package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	c := &Config{}
	c.WorkingHours = WorkingHours{Start: "09:00", End: "18:00", Timezone: "America/New_York"}
	c.Lunch = LunchConfig{Preferred: "12:00", WindowStart: "11:30", WindowEnd: "13:30", DurationMinutes: 60}
	c.Focus = FocusConfig{Style: "flexible", MinBlockMinutes: 30, MaxBlockMinutes: 120, BufferAroundMeetings: 10}
	c.Server = ServerConfig{CalDAVPort: 5232, RESTPort: 5233, MCPPort: 5234, OAuthPort: 8080}
	return c
}

func TestValidatePlanner(t *testing.T) {
	if err := validConfig().ValidatePlanner(); err != nil {
		t.Fatalf("baseline should pass: %v", err)
	}
	cases := []struct {
		name  string
		field string
		mut   func(*Config)
	}{
		{"start format", "working_hours.start", func(c *Config) { c.WorkingHours.Start = "9:00" }},
		{"start>=end", "working_hours", func(c *Config) { c.WorkingHours.Start = "18:00" }},
		{"tz empty", "working_hours.timezone", func(c *Config) { c.WorkingHours.Timezone = "" }},
		{"tz bogus", "working_hours.timezone", func(c *Config) { c.WorkingHours.Timezone = "Mars/Base" }},
		{"lunch dur 14", "lunch.duration_minutes", func(c *Config) { c.Lunch.DurationMinutes = 14 }},
		{"lunch dur 121", "lunch.duration_minutes", func(c *Config) { c.Lunch.DurationMinutes = 121 }},
		{"lunch preferred early", "lunch.preferred", func(c *Config) { c.Lunch.Preferred = "11:00" }},
		{"lunch preferred late", "lunch.preferred", func(c *Config) { c.Lunch.Preferred = "14:00" }},
		{"min 4", "focus.min_block_minutes", func(c *Config) { c.Focus.MinBlockMinutes = 4 }},
		{"min 121", "focus.min_block_minutes", func(c *Config) { c.Focus.MinBlockMinutes = 121 }},
		{"max 14", "focus.max_block_minutes", func(c *Config) { c.Focus.MaxBlockMinutes = 14 }},
		{"max 241", "focus.max_block_minutes", func(c *Config) { c.Focus.MaxBlockMinutes = 241 }},
		{"min>max", "focus.min_block_minutes", func(c *Config) { c.Focus.MinBlockMinutes = 60; c.Focus.MaxBlockMinutes = 30 }},
		{"buffer -1", "focus.buffer_around_meetings", func(c *Config) { c.Focus.BufferAroundMeetings = -1 }},
		{"buffer 61", "focus.buffer_around_meetings", func(c *Config) { c.Focus.BufferAroundMeetings = 61 }},
		{"style bogus", "focus.style", func(c *Config) { c.Focus.Style = "bogus" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mut(c)
			err := c.ValidatePlanner()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error %q does not name %q", err, tc.field)
			}
		})
	}
}

func TestValidateApp(t *testing.T) {
	if err := validConfig().ValidateApp(); err != nil {
		t.Fatalf("defaults should pass: %v", err)
	}
	cases := []struct {
		name  string
		field string
		mut   func(*Config)
	}{
		{"port 0", "server.rest_port", func(c *Config) { c.Server.RESTPort = 0 }},
		{"port 70000", "server.mcp_port", func(c *Config) { c.Server.MCPPort = 70000 }},
		{"duplicate", "server.oauth_port", func(c *Config) { c.Server.OAuthPort = 5232 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mut(c)
			err := c.ValidateApp()
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("want error naming %q, got %v", tc.field, err)
			}
		})
	}
}

func TestValidateCalendars(t *testing.T) {
	base := func() *Config {
		c := validConfig()
		c.Sources = []SourceConfig{{ID: "work", Type: "google"}}
		c.Destinations = []DestinationConfig{{ID: "work", CalendarID: "primary", Sources: []DestinationSource{{ID: "work"}}}}
		return c
	}
	if err := base().ValidateCalendars(); err != nil {
		t.Fatalf("baseline should pass: %v", err)
	}
	t.Run("inline secrets accepted", func(t *testing.T) {
		c := base()
		c.Sources = append(c.Sources, SourceConfig{ID: "f", Type: "ics", ICSURL: "https://x/y.ics"}, SourceConfig{ID: "d", Type: "caldav", Password: "pw"})
		if err := c.ValidateCalendars(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dest without source or type", func(t *testing.T) {
		c := base()
		c.Destinations = []DestinationConfig{{ID: "ghost", CalendarID: "x"}}
		if err := c.ValidateCalendars(); err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("deleted referenced source", func(t *testing.T) {
		c := base()
		c.Sources = nil
		if err := c.ValidateCalendars(); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("duplicate ids", func(t *testing.T) {
		c := base()
		c.Sources = append(c.Sources, SourceConfig{ID: "work", Type: "google"})
		if err := c.ValidateCalendars(); err == nil || !strings.Contains(err.Error(), "duplicate source id") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestValidateJoinsAll(t *testing.T) {
	c := validConfig()
	c.Server.RESTPort = 0
	c.Focus.Style = "bogus"
	c.Sources = []SourceConfig{{ID: "a", Type: "nope"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{"server.rest_port", "focus.style", "unknown type"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("missing %q in %v", s, err)
		}
	}
}
