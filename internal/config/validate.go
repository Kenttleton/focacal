package config

import (
	"errors"
	"fmt"
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

// ValidatePlanner checks working hours, lunch and focus settings. Bounds match
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

	return errors.Join(errs...)
}

// ValidateCalendars checks accounts, calendars and destinations. Task 2
// replaces this skeleton with the full validator.
func (c *Config) ValidateCalendars() error {
	var errs []error
	dupes := func(kind string, ids []string) {
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				errs = append(errs, fmt.Errorf("duplicate %s id %q", kind, id))
			}
			seen[id] = true
		}
	}
	var ids []string
	for _, a := range c.Accounts {
		ids = append(ids, a.ID)
		switch a.Type {
		case "google", "outlook", "caldav":
		default:
			errs = append(errs, fmt.Errorf("account %q: unknown type", a.ID))
		}
	}
	dupes("account", ids)
	ids = nil
	for _, cal := range c.Calendars {
		ids = append(ids, cal.ID)
		if cal.Account == "" && cal.URL == "" && cal.URLEnv == "" {
			errs = append(errs, fmt.Errorf("calendar %q: needs an account or a url", cal.ID))
		}
	}
	dupes("calendar", ids)
	ids = nil
	for _, d := range c.Destinations {
		ids = append(ids, d.ID)
	}
	dupes("destination", ids)

	accounts := map[string]bool{}
	for _, a := range c.Accounts {
		accounts[a.ID] = true
	}
	cals := map[string]bool{FocacalSource: true}
	for _, cal := range c.Calendars {
		cals[cal.ID] = true
		if cal.Account != "" && !accounts[cal.Account] {
			errs = append(errs, fmt.Errorf("calendar %q: no account %q", cal.ID, cal.Account))
		}
	}
	for _, d := range c.Destinations {
		for _, in := range d.Inputs.Items {
			if !cals[in.Calendar] {
				errs = append(errs, fmt.Errorf("destination %q: no calendar %q", d.ID, in.Calendar))
			}
		}
		if d.Target.Calendar != "" && !cals[d.Target.Calendar] {
			errs = append(errs, fmt.Errorf("destination %q: no calendar %q", d.ID, d.Target.Calendar))
		}
	}
	return errors.Join(errs...)
}
