package config

// Resolve derives c.Sources: one SourceConfig per calendar, with the
// connection fields of its account (or ICS fields for a url calendar). The
// rest of the program reads Sources. A calendar naming a missing account is
// left out; validation reports it separately.
func (c *Config) Resolve() { c.Sources = c.resolved() }

// resolved builds the sources from Accounts and Calendars without touching c.
func (c *Config) resolved() []SourceConfig {
	accounts := make(map[string]AccountConfig, len(c.Accounts))
	for _, a := range c.Accounts {
		accounts[a.ID] = a
	}
	var out []SourceConfig
	for _, cal := range c.Calendars {
		src := SourceConfig{
			ID:        cal.ID,
			Label:     cal.Name,
			Conflicts: cal.Conflicts,
			Plan:      cal.Plans(),
		}
		switch {
		case cal.Account != "":
			a, ok := accounts[cal.Account]
			if !ok {
				continue
			}
			src.Type = a.Type
			src.AccountID = a.ID
			src.CredentialsFile = a.CredentialsFile
			src.TokenFile = a.TokenFile
			src.CalDAVURL = a.CalDAVURL
			src.Username = a.Username
			src.Password = a.Password
			src.AuthType = a.AuthType
			if cal.RemoteID != "" {
				src.CalendarIDs = []string{cal.RemoteID}
			}
		case cal.URL != "" || cal.URLEnv != "":
			src.Type = "ics"
			src.ICSURL = cal.URL
			src.ICSURLEnv = cal.URLEnv
		default:
			continue
		}
		out = append(out, src)
	}
	return out
}

// TokenKey is the key a source's OAuth token is stored under: its account id,
// so calendars of one account share a token; the source id when it has none.
func (s SourceConfig) TokenKey() string {
	if s.AccountID != "" {
		return s.AccountID
	}
	return s.ID
}

// PlanSourceIDs returns the ids of the resolved sources the planner
// schedules around. It lives on UserConfig so request-scoped readers that only
// hold a UserConfig (the MCP tools) can use it; Config inherits it.
func (c *UserConfig) PlanSourceIDs() map[string]bool {
	out := make(map[string]bool, len(c.Sources))
	for _, s := range c.Sources {
		if s.Plan {
			out[s.ID] = true
		}
	}
	return out
}

// ExpandInputs returns the inputs of d with "all" expanded: every resolvable
// calendar except d's own target calendar, then focacal, each with the zero
// (full) visibility. An explicit list drops inputs naming an unknown calendar
// (a dangling reference, reported as a warning by the loose validator).
func (c *Config) ExpandInputs(d DestinationConfig) []DestinationInput {
	if d.Inputs.All {
		var out []DestinationInput
		for _, s := range c.resolved() {
			if s.ID != d.Target.Calendar {
				out = append(out, DestinationInput{Calendar: s.ID})
			}
		}
		return append(out, DestinationInput{Calendar: FocacalSource})
	}
	known := make(map[string]bool, len(c.Calendars)+1)
	known[FocacalSource] = true
	for _, s := range c.resolved() {
		known[s.ID] = true
	}
	var out []DestinationInput
	for _, in := range d.Inputs.Items {
		if known[in.Calendar] {
			out = append(out, in)
		}
	}
	return out
}
