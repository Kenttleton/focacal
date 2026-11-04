package config

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Validate checks the whole config and returns a single error listing every problem.
func (c *Config) Validate() error {
	return errors.Join(c.ValidateApp(), c.ValidatePlanner(), c.ValidateCalendars())
}

// ValidateApp checks deployment-level settings: the four server ports must be
// in 1..65535 and mutually distinct.
func (c *Config) ValidateApp() error {
	var errs []error
	ports := []struct {
		name string
		val  int
	}{
		{"server.caldav_port", c.Server.CalDAVPort},
		{"server.rest_port", c.Server.RESTPort},
		{"server.mcp_port", c.Server.MCPPort},
		{"server.oauth_port", c.Server.OAuthPort},
	}
	seen := make(map[int]string)
	for _, p := range ports {
		if p.val < 1 || p.val > 65535 {
			errs = append(errs, fmt.Errorf("%s: %d is out of range 1-65535", p.name, p.val))
			continue
		}
		if prev, ok := seen[p.val]; ok {
			errs = append(errs, fmt.Errorf("%s: port %d is already used by %s", p.name, p.val, prev))
			continue
		}
		seen[p.val] = p.name
	}
	return errors.Join(errs...)
}

// parseHHMM strictly parses a zero-padded 24-hour "HH:MM" string into minutes
// since midnight.
func parseHHMM(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("must be HH:MM, got %q", s)
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("must be HH:MM, got %q", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func inRange(errs *[]error, field string, v, lo, hi int) {
	if v < lo || v > hi {
		*errs = append(*errs, fmt.Errorf("%s: %d is out of range %d-%d", field, v, lo, hi))
	}
}

// ValidatePlanner checks working hours, lunch, focus and agent settings. Bounds match
// the web UI.
func (c *Config) ValidatePlanner() error {
	var errs []error

	wh := c.WorkingHours
	start, serr := parseHHMM(wh.Start)
	if serr != nil {
		errs = append(errs, fmt.Errorf("working_hours.start: %w", serr))
	}
	end, eerr := parseHHMM(wh.End)
	if eerr != nil {
		errs = append(errs, fmt.Errorf("working_hours.end: %w", eerr))
	}
	if serr == nil && eerr == nil && start >= end {
		errs = append(errs, fmt.Errorf("working_hours: start %q must be before end %q", wh.Start, wh.End))
	}
	if wh.Timezone == "" {
		errs = append(errs, fmt.Errorf("working_hours.timezone: is required"))
	} else if _, err := time.LoadLocation(wh.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("working_hours.timezone: unknown timezone %q", wh.Timezone))
	}

	l := c.Lunch
	inRange(&errs, "lunch.duration_minutes", l.DurationMinutes, 15, 120)
	pref, perr := parseHHMM(l.Preferred)
	if perr != nil {
		errs = append(errs, fmt.Errorf("lunch.preferred: %w", perr))
	}
	ws, wserr := parseHHMM(l.WindowStart)
	if wserr != nil {
		errs = append(errs, fmt.Errorf("lunch.window_start: %w", wserr))
	}
	we, weerr := parseHHMM(l.WindowEnd)
	if weerr != nil {
		errs = append(errs, fmt.Errorf("lunch.window_end: %w", weerr))
	}
	if wserr == nil && weerr == nil {
		if ws >= we {
			errs = append(errs, fmt.Errorf("lunch.window_start: %q must be before window_end %q", l.WindowStart, l.WindowEnd))
		} else if perr == nil && (pref < ws || pref > we) {
			errs = append(errs, fmt.Errorf("lunch.preferred: %q must be within window %s-%s", l.Preferred, l.WindowStart, l.WindowEnd))
		}
	}

	f := c.Focus
	switch f.Style {
	case "flexible", "pomodoro", "fixed":
	default:
		errs = append(errs, fmt.Errorf("focus.style: %q must be one of flexible, pomodoro, fixed", f.Style))
	}
	inRange(&errs, "focus.min_block_minutes", f.MinBlockMinutes, 5, 120)
	inRange(&errs, "focus.max_block_minutes", f.MaxBlockMinutes, 15, 240)
	if f.MinBlockMinutes > f.MaxBlockMinutes {
		errs = append(errs, fmt.Errorf("focus.min_block_minutes: %d must not exceed max_block_minutes %d", f.MinBlockMinutes, f.MaxBlockMinutes))
	}
	inRange(&errs, "focus.buffer_around_meetings", f.BufferAroundMeetings, 0, 60)

	a := c.Agent
	inRange(&errs, "agent.timeout_secs", a.TimeoutSecs, 1, 3600)
	if a.MaxTokens < 1 {
		errs = append(errs, fmt.Errorf("agent.max_tokens: %d must be at least 1", a.MaxTokens))
	}

	return errors.Join(errs...)
}

// ValidateCalendars checks the OAuth redirect, sources and destinations.
func (c *Config) ValidateCalendars() error {
	var errs []error

	if u, err := url.Parse(c.OAuth.RedirectBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("oauth.redirect_base_url: %q must be an absolute http(s) URL", c.OAuth.RedirectBaseURL))
	}

	// Source ids must be unique.
	seenSource := make(map[string]bool)
	for _, s := range c.Sources {
		if seenSource[s.ID] {
			errs = append(errs, fmt.Errorf("duplicate source id %q", s.ID))
		}
		seenSource[s.ID] = true

		switch s.Type {
		case "google", "outlook", "caldav", "ics":
		default:
			errs = append(errs, fmt.Errorf("unknown type %q", s.Type))
		}

		// An ics source needs a reference or an inline URL. Whether the
		// referenced variable is set is not checked here (R14): an unset
		// variable is a startup warning (UnresolvedEnvRefs) and a sync-time
		// error for that source only.
		if s.Type == "ics" && s.ICSURL == "" && s.ICSURLEnv == "" {
			errs = append(errs, fmt.Errorf("ics source %q: ics_url_env (or ics_url) is required", s.ID))
		}
	}

	// Destinations.
	seenDest := make(map[string]bool)
	for _, d := range c.Destinations {
		sourceExists := false
		var src *SourceConfig
		for i := range c.Sources {
			if c.Sources[i].ID == d.ID {
				sourceExists = true
				src = &c.Sources[i]
			}
		}

		if !sourceExists {
			if d.Type == "" {
				errs = append(errs, fmt.Errorf("destination %q: no source with id %q", d.ID, d.ID))
			} else {
				switch d.Type {
				case "caldav":
					if d.CalDAVURL == "" {
						errs = append(errs, fmt.Errorf("destination %q: caldav_url is required", d.ID))
					}
				case "ics":
					if d.ICSURL == "" && d.ICSURLEnv == "" {
						errs = append(errs, fmt.Errorf("destination %q: ics_url is required", d.ID))
					}
				}
			}
		} else if src != nil {
			ids := src.EffectiveCalendarIDs()
			if !containsString(ids, d.CalendarID) {
				errs = append(errs, fmt.Errorf("calendar_id %q is not in calendar_ids", d.CalendarID))
			}
		}

		pair := d.ID + "|" + d.CalendarID
		if seenDest[pair] {
			errs = append(errs, fmt.Errorf("duplicate destination pair %q/%q", d.ID, d.CalendarID))
		}
		seenDest[pair] = true

		for _, ds := range d.Sources {
			if ds.ID == FocacalSource {
				continue
			}
			found := false
			for _, s := range c.Sources {
				if s.ID == ds.ID {
					found = true
					break
				}
			}
			if !found {
				errs = append(errs, fmt.Errorf("destination %q: unknown source %q", d.ID, ds.ID))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
