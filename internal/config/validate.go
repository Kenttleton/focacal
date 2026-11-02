package config

import (
	"errors"
	"fmt"
)

// Validate checks the whole config and returns a single error listing every problem.
func (c *Config) Validate() error {
	var errs []error

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

		if s.Type == "ics" {
			if _, err := s.ResolveICSURL(); err != nil {
				errs = append(errs, fmt.Errorf("ics source %q: ics_url is required", s.ID))
			}
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


