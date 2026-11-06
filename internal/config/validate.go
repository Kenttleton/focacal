package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Validate checks the whole config as a file load does and returns a single
// error listing every problem. Calendars use the loose validator: a dangling
// reference in a hand-edited file is a warning (Config.Warnings), not an
// error. Service.Patch validates strictly instead.
func (c *Config) Validate() error {
	return errors.Join(c.ValidateApp(), c.ValidatePlanner(), c.ValidateCalendarsLoose())
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

// ValidateCalendars is the strict calendars validator, used for API changes:
// structural errors, dangling references and cycles are all errors. Messages
// name ids and never echo values, since values can be secrets.
func (c *Config) ValidateCalendars() error { return c.ValidateCalendarsAllowing(nil) }

// ValidateCalendarsAllowing is ValidateCalendars except that the dangling
// references listed in existing (messages from DanglingRefs of the file
// before a patch) are tolerated: only new ones are errors. Structure and
// cycle errors stay strict.
func (c *Config) ValidateCalendarsAllowing(existing []string) error {
	errs := c.calendarStructureErrors()
	for _, m := range c.DanglingRefs() {
		if !slices.Contains(existing, m) {
			errs = append(errs, errors.New(m))
		}
	}
	errs = append(errs, c.cycleErrors()...)
	return errors.Join(errs...)
}

// ValidateCalendarsLoose is ValidateCalendars without the reference errors,
// used when loading files: a dangling reference is reported through
// Config.Warnings (see DanglingRefs) and skipped at runtime.
func (c *Config) ValidateCalendarsLoose() error {
	errs := c.calendarStructureErrors()
	errs = append(errs, c.cycleErrors()...)
	return errors.Join(errs...)
}

var (
	accountTypes     = []string{"google", "outlook", "caldav"}
	focacalKinds     = []string{"focus", "lunch", "break"}
	targetAuthValues = []string{"", "private", "public"}
)

// checkIDs reports case-insensitively duplicate ids of one list. The id shape
// (ValidID) is not checked here: hand-written ids that do not match keep
// loading. Service.Patch enforces it for ids a patch creates.
func checkIDs(kind string, ids []string, errs *[]error) {
	seen := map[string]bool{}
	for _, id := range ids {
		key := strings.ToLower(id)
		if seen[key] {
			*errs = append(*errs, fmt.Errorf("duplicate %s id %q", kind, id))
		}
		seen[key] = true
	}
}

// calendarStructureErrors covers everything that does not depend on another
// entry existing.
func (c *Config) calendarStructureErrors() []error {
	var errs []error

	ids := make([]string, len(c.Accounts))
	for i, a := range c.Accounts {
		ids[i] = a.ID
		if !slices.Contains(accountTypes, a.Type) {
			errs = append(errs, fmt.Errorf("account %q: unknown type (want google, outlook or caldav)", a.ID))
		}
	}
	checkIDs("account", ids, &errs)

	ids = make([]string, len(c.Calendars))
	for i, cal := range c.Calendars {
		ids[i] = cal.ID
		if strings.EqualFold(cal.ID, FocacalSource) {
			errs = append(errs, fmt.Errorf("calendar id %q is reserved for the built-in calendar", cal.ID))
		}
		hasURL := cal.URL != "" || cal.URLEnv != ""
		switch {
		case cal.Account != "" && hasURL:
			errs = append(errs, fmt.Errorf("calendar %q: set either account or url, not both", cal.ID))
		case cal.Account == "" && !hasURL:
			errs = append(errs, fmt.Errorf("calendar %q: needs an account or a url (url_env or url)", cal.ID))
		}
	}
	checkIDs("calendar", ids, &errs)

	cals := make(map[string]CalendarConfig, len(c.Calendars))
	for _, cal := range c.Calendars {
		cals[cal.ID] = cal
	}
	ids = make([]string, len(c.Destinations))
	for i, d := range c.Destinations {
		ids[i] = d.ID
		for _, in := range d.Inputs.Items {
			if len(in.Kinds) == 0 {
				continue
			}
			if in.Calendar != FocacalSource {
				errs = append(errs, fmt.Errorf("destination %q: kinds applies only to the focacal input", d.ID))
				continue
			}
			for _, k := range in.Kinds {
				if !slices.Contains(focacalKinds, k) {
					errs = append(errs, fmt.Errorf("destination %q: kinds must be focus, lunch or break", d.ID))
					break
				}
			}
		}

		t := d.Target
		switch {
		case t.Calendar != "" && t.Served:
			errs = append(errs, fmt.Errorf("destination %q: target must be a calendar or served, not both", d.ID))
		case t.Calendar == "" && !t.Served:
			errs = append(errs, fmt.Errorf("destination %q: target needs a calendar or served: true", d.ID))
		}
		if t.Calendar != "" {
			if cal, ok := cals[t.Calendar]; ok && cal.Account == "" && (cal.URL != "" || cal.URLEnv != "") {
				errs = append(errs, fmt.Errorf("destination %q: target calendar %q is a read-only url feed and cannot be written", d.ID, t.Calendar))
			}
		}
		if !slices.Contains(targetAuthValues, t.Auth) {
			errs = append(errs, fmt.Errorf("destination %q: target auth must be private or public", d.ID))
		}
		if t.Auth == "public" {
			errs = append(errs, fmt.Errorf("destination %q: auth public is not available yet", d.ID))
		}
		if t.Auth != "" && !t.Served {
			errs = append(errs, fmt.Errorf("destination %q: auth applies only to a served target", d.ID))
		}
	}
	checkIDs("destination", ids, &errs)
	return errs
}

// DanglingRefs lists references to entries that do not exist: a calendar
// naming a missing account, a destination input or target naming a missing
// calendar. Each message names ids only. The strict validator reports them as
// errors; a file load keeps them as Config.Warnings and the runtime skips the
// entries.
func (c *Config) DanglingRefs() []string {
	accounts := make(map[string]bool, len(c.Accounts))
	for _, a := range c.Accounts {
		accounts[a.ID] = true
	}
	cals := map[string]bool{FocacalSource: true}
	for _, cal := range c.Calendars {
		cals[cal.ID] = true
	}
	var out []string
	for _, cal := range c.Calendars {
		if cal.Account != "" && !accounts[cal.Account] {
			out = append(out, fmt.Sprintf("calendar %q: account %q does not exist", cal.ID, cal.Account))
		}
	}
	for _, d := range c.Destinations {
		for _, in := range d.Inputs.Items {
			if !cals[in.Calendar] {
				out = append(out, fmt.Sprintf("destination %q: input calendar %q does not exist", d.ID, in.Calendar))
			}
		}
		if d.Target.Calendar != "" && !cals[d.Target.Calendar] {
			out = append(out, fmt.Sprintf("destination %q: target calendar %q does not exist", d.ID, d.Target.Calendar))
		}
	}
	return out
}

type cycleEdge struct{ to, dest string }

// cycleErrors reports a cycle in the graph with an edge from each input
// calendar to the target calendar of its destination (inputs: all expanded;
// focacal has no incoming edge, a served target has no outgoing one). It
// reports the first cycle found, naming the destinations on it.
func (c *Config) cycleErrors() []error {
	edges := map[string][]cycleEdge{}
	var nodes []string
	for _, d := range c.Destinations {
		if d.Target.Calendar == "" {
			continue
		}
		for _, in := range c.ExpandInputs(d) {
			if in.Calendar == FocacalSource {
				continue
			}
			if _, ok := edges[in.Calendar]; !ok {
				nodes = append(nodes, in.Calendar)
			}
			edges[in.Calendar] = append(edges[in.Calendar], cycleEdge{to: d.Target.Calendar, dest: d.ID})
		}
	}
	slices.Sort(nodes)

	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var path []string // node ids on the DFS stack
	var via []string  // destination ids on the stack edges: via[i] leads path[i] to path[i+1]
	var found []string
	var visit func(n string) bool
	visit = func(n string) bool {
		state[n] = visiting
		path = append(path, n)
		for _, e := range edges[n] {
			switch state[e.to] {
			case visiting:
				start := slices.Index(path, e.to)
				found = append(slices.Clone(via[start:]), e.dest)
				return true
			case 0:
				via = append(via, e.dest)
				if visit(e.to) {
					return true
				}
				via = via[:len(via)-1]
			}
		}
		path = path[:len(path)-1]
		state[n] = done
		return false
	}
	for _, n := range nodes {
		if state[n] == 0 && visit(n) {
			break
		}
	}
	if found == nil {
		return nil
	}
	var uniq []string
	for _, id := range found {
		if !slices.Contains(uniq, id) {
			uniq = append(uniq, id)
		}
	}
	quoted := make([]string, len(uniq))
	for i, id := range uniq {
		quoted[i] = strconv.Quote(id)
	}
	return []error{fmt.Errorf("destinations form a cycle: %s (a destination's target calendar feeds back into its own inputs)", strings.Join(quoted, ", "))}
}
