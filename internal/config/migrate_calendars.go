package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// legacySource is the pre-calendar-model `sources` item. Decoding is lenient:
// keys it does not name (writeback, cross_busy, anything else a hand-edited
// file carries) are ignored.
type legacySource struct {
	ID              string         `yaml:"id"`
	Label           string         `yaml:"label"`
	Type            string         `yaml:"type"`
	CredentialsFile string         `yaml:"credentials_file"`
	TokenFile       string         `yaml:"token_file"`
	CalendarIDs     []string       `yaml:"calendar_ids"`
	Conflicts       ConflictConfig `yaml:"conflicts"`
	CalDAVURL       string         `yaml:"caldav_url"`
	Username        string         `yaml:"username"`
	Password        string         `yaml:"password"`
	AuthType        string         `yaml:"auth_type"`
	ICSURL          string         `yaml:"ics_url"`
	ICSURLEnv       string         `yaml:"ics_url_env"`
}

// legacyDestination is the pre-calendar-model `destinations` item.
type legacyDestination struct {
	ID              string                  `yaml:"id"`
	CalendarID      string                  `yaml:"calendar_id"`
	Type            string                  `yaml:"type"`
	CredentialsFile string                  `yaml:"credentials_file"`
	TokenFile       string                  `yaml:"token_file"`
	CalDAVURL       string                  `yaml:"caldav_url"`
	Username        string                  `yaml:"username"`
	Password        string                  `yaml:"password"`
	AuthType        string                  `yaml:"auth_type"`
	CalendarIDs     []string                `yaml:"calendar_ids"`
	Sources         []legacyDestinationItem `yaml:"sources"`
}

type legacyDestinationItem struct {
	ID         string           `yaml:"id"`
	CalendarID string           `yaml:"calendar_id"`
	Event      *EventVisibility `yaml:"event"`
}

// MigrateCalendarModel converts a legacy calendars.yaml in dir (top-level
// `sources`, neither `accounts` nor `calendars`) to accounts, calendars and
// destinations. See MigrateCalendarModelLog.
func MigrateCalendarModel(dir string) (migrated bool, err error) {
	return MigrateCalendarModelLog(dir, slog.Default())
}

// MigrateCalendarModelLog is MigrateCalendarModel with the logger for the
// warnings about legacy destinations that could not be carried over (they
// name the destination id only).
//
// Each source becomes an account (same id, name from label) with one calendar
// per calendar_ids entry; the first keeps the source id, later ones get
// <source>-<slug(remote id)>. An ics source becomes an account-less calendar.
// Legacy destinations are carried over best effort and a destination serving
// `focacal` is added when none exists. Other top-level keys (oauth) and the
// file's header comment are kept. The original is saved as calendars.yaml.bak
// (0600: it may hold inline secrets) before the file is replaced atomically.
// A failed write leaves the original in place and returns an error naming the
// file. A file already in the new shape is left alone, so a second run
// returns false.
func MigrateCalendarModelLog(dir string, log *slog.Logger) (migrated bool, err error) {
	if log == nil {
		log = slog.Default()
	}
	file := DomainCalendars.File()
	p := filepath.Join(dir, file)
	original, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("migrate: read %s: %w", file, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return false, fmt.Errorf("migrate: parse %s: %w", file, err)
	}
	root := rootMapping(&doc)
	if root == nil {
		return false, nil
	}
	si := findKey(root, "sources")
	if si < 0 || findKey(root, "accounts") >= 0 || findKey(root, "calendars") >= 0 {
		return false, nil
	}
	var sources []legacySource
	if err := root.Content[si].Decode(&sources); err != nil {
		return false, fmt.Errorf("migrate: %s: sources are not a list of mappings: %w", file, redactDecodeError(err, original))
	}
	var dests []legacyDestination
	if di := findKey(root, "destinations"); di >= 0 {
		if err := root.Content[di].Decode(&dests); err != nil {
			return false, fmt.Errorf("migrate: %s: destinations are not a list of mappings: %w", file, redactDecodeError(err, original))
		}
	}

	seen := map[string]bool{}
	for _, s := range sources {
		k := strings.ToLower(s.ID)
		if seen[k] {
			return false, fmt.Errorf("migrate: %s: source id %q appears more than once; nothing was changed. Rename or remove one and restart", file, s.ID)
		}
		seen[k] = true
	}

	m := newCalendarMigration(log)
	for _, s := range sources {
		m.reserve(s)
	}
	for _, s := range sources {
		m.addSource(s)
	}
	for _, d := range dests {
		m.addDestination(d)
	}
	m.addDefaultDestination()

	if err := m.rewrite(&doc, root, si); err != nil {
		return false, fmt.Errorf("migrate: encode %s: %w", file, err)
	}
	out, err := encodeYAML(&doc)
	if err != nil {
		return false, fmt.Errorf("migrate: encode %s: %w", file, err)
	}
	// The result must decode under the strict rules before anything is written.
	if err := decodeDomain(DomainCalendars, out, Defaults()); err != nil {
		return false, fmt.Errorf("migrate: converted %s does not load; nothing was changed: %w", file, err)
	}

	bak := p + ".bak"
	if err := os.WriteFile(bak, original, 0600); err != nil {
		return false, fmt.Errorf("migrate: write backup %s.bak: %w", file, err)
	}
	if err := os.Chmod(bak, 0600); err != nil {
		return false, fmt.Errorf("migrate: chmod backup %s.bak: %w", file, err)
	}
	err = NewWriter(p).Update(func([]byte) ([]byte, error) { return out, nil })
	if err != nil {
		return false, fmt.Errorf("migrate: write %s: %w", file, err)
	}
	return true, nil
}

// calendarMigration accumulates the converted file.
type calendarMigration struct {
	log          *slog.Logger
	accounts     []AccountConfig
	calendars    []CalendarConfig
	destinations []DestinationConfig
	accountIDs   map[string]bool // lowercase
	calendarIDs  map[string]bool // lowercase
	destIDs      map[string]bool // lowercase
	// byCalendars maps an old source id to its calendars in order.
	bySource map[string][]CalendarConfig
	icsIDs   map[string]bool
}

func newCalendarMigration(log *slog.Logger) *calendarMigration {
	return &calendarMigration{
		log:         log,
		accountIDs:  map[string]bool{},
		calendarIDs: map[string]bool{"focacal": true}, // reserved
		destIDs:     map[string]bool{},
		bySource:    map[string][]CalendarConfig{},
		icsIDs:      map[string]bool{},
	}
}

// reserve claims a source id for its account and first calendar, so ids
// generated for earlier sources never take it.
func (m *calendarMigration) reserve(s legacySource) {
	id := strings.ToLower(s.ID)
	m.calendarIDs[id] = true
	if s.Type != "ics" {
		m.accountIDs[id] = true
	}
}

func (m *calendarMigration) uniqueCalendarID(name string) string {
	id := UniqueID(name, func(c string) bool { return m.calendarIDs[strings.ToLower(c)] })
	m.calendarIDs[id] = true
	return id
}

func (m *calendarMigration) uniqueAccountID(name string) string {
	id := UniqueID(name, func(c string) bool { return m.accountIDs[strings.ToLower(c)] })
	m.accountIDs[id] = true
	return id
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func planned(b bool) *bool { return &b }

func (m *calendarMigration) addSource(s legacySource) {
	name := orDefault(s.Label, s.ID)
	if s.Type == "ics" {
		m.icsIDs[s.ID] = true
		c := CalendarConfig{ID: s.ID, Name: name, URL: s.ICSURL, URLEnv: s.ICSURLEnv,
			Plan: planned(true), Conflicts: s.Conflicts}
		m.calendars = append(m.calendars, c)
		m.bySource[s.ID] = []CalendarConfig{c}
		return
	}
	m.accounts = append(m.accounts, AccountConfig{
		ID: s.ID, Name: name, Type: s.Type,
		CredentialsFile: s.CredentialsFile, TokenFile: s.TokenFile,
		CalDAVURL: s.CalDAVURL, Username: s.Username, Password: s.Password, AuthType: s.AuthType,
	})
	var remotes []string
	for _, r := range s.CalendarIDs {
		if strings.TrimSpace(r) != "" {
			remotes = append(remotes, r)
		}
	}
	if len(remotes) == 0 {
		// Keep the old default: primary for google and outlook, and for
		// caldav no remote_id (the connector default).
		def := ""
		if s.Type == "google" || s.Type == "outlook" {
			def = "primary"
		}
		remotes = []string{def}
	}
	for i, r := range remotes {
		c := CalendarConfig{ID: s.ID, Name: name, Account: s.ID, RemoteID: r,
			Plan: planned(true), Conflicts: s.Conflicts}
		if i > 0 {
			c.ID = m.uniqueCalendarID(s.ID + "-" + r)
			c.Name = fmt.Sprintf("%s (%s)", name, r)
		}
		m.calendars = append(m.calendars, c)
		m.bySource[s.ID] = append(m.bySource[s.ID], c)
	}
}

// resolveCalendar returns the calendar id a legacy (source id, calendar_id)
// pair refers to: the source's calendar with that remote id, else its first.
func (m *calendarMigration) resolveCalendar(source, remote string) (string, bool) {
	cs := m.bySource[source]
	if len(cs) == 0 {
		return "", false
	}
	if remote != "" {
		for _, c := range cs {
			if c.RemoteID == remote {
				return c.ID, true
			}
		}
	}
	return cs[0].ID, true
}

func (m *calendarMigration) addDestination(d legacyDestination) {
	remote := d.CalendarID
	if remote == "" && len(d.CalendarIDs) > 0 {
		remote = d.CalendarIDs[0]
	}
	var target string
	if _, isSource := m.bySource[d.ID]; isSource {
		if m.icsIDs[d.ID] {
			m.dropDestination(d.ID)
			return
		}
		target, _ = m.resolveCalendar(d.ID, remote)
	} else {
		switch d.Type {
		case "google", "outlook", "caldav":
		default:
			m.dropDestination(d.ID)
			return
		}
		accID := m.uniqueAccountID(d.ID)
		m.accounts = append(m.accounts, AccountConfig{
			ID: accID, Name: d.ID, Type: d.Type,
			CredentialsFile: d.CredentialsFile, TokenFile: d.TokenFile,
			CalDAVURL: d.CalDAVURL, Username: d.Username, Password: d.Password, AuthType: d.AuthType,
		})
		if remote == "" && d.Type != "caldav" {
			remote = "primary"
		}
		// A calendar that exists to be written to: the planner does not
		// schedule around it.
		cal := CalendarConfig{ID: m.uniqueCalendarID(d.ID), Name: d.ID, Account: accID, RemoteID: remote, Plan: planned(false)}
		m.calendars = append(m.calendars, cal)
		target = cal.ID
	}

	dest := DestinationConfig{ID: d.ID, Name: d.ID, Target: DestinationTarget{Calendar: target}}
	if m.destIDs[strings.ToLower(dest.ID)] {
		dest.ID = UniqueID(d.ID, func(c string) bool { return m.destIDs[strings.ToLower(c)] })
		m.log.Warn("migrate: legacy destination id is a duplicate and was renamed", "destination", d.ID)
	}
	m.destIDs[strings.ToLower(dest.ID)] = true
	for _, in := range d.Sources {
		if in.ID == "focacal" {
			dest.Inputs.Items = append(dest.Inputs.Items, DestinationInput{Calendar: "focacal", Event: in.Event})
			continue
		}
		cal, ok := m.resolveCalendar(in.ID, in.CalendarID)
		if !ok {
			m.log.Warn("migrate: legacy destination input names an unknown source and was dropped", "destination", d.ID)
			continue
		}
		if cal == target {
			// A destination that reads the calendar it writes is a cycle.
			m.log.Warn("migrate: legacy destination input that names the destination's own target was dropped", "destination", d.ID)
			continue
		}
		dest.Inputs.Items = append(dest.Inputs.Items, DestinationInput{Calendar: cal, Event: in.Event})
	}
	if len(dest.Inputs.Items) == 0 {
		// Least privilege: never widen to source calendars the user did not list.
		if len(d.Sources) > 0 {
			m.log.Warn("migrate: every input of a legacy destination was dropped; it now takes only focacal", "destination", d.ID)
		}
		dest.Inputs.Items = []DestinationInput{{Calendar: "focacal"}}
	}
	m.destinations = append(m.destinations, dest)
}

func (m *calendarMigration) dropDestination(id string) {
	m.log.Warn("migrate: legacy destination cannot be carried over and was dropped", "destination", id)
}

func (m *calendarMigration) addDefaultDestination() {
	for _, d := range m.destinations {
		if strings.EqualFold(d.ID, "focacal") {
			return
		}
	}
	m.destinations = append(m.destinations, DestinationConfig{
		ID: "focacal", Name: "Focacal", Inputs: InputList{All: true},
		Target: DestinationTarget{Served: true, Auth: "private"},
	})
}

// rewrite replaces the sources key (at value index si) with accounts and
// calendars, and the destinations key with the new destinations, keeping the
// other keys and the header comment.
func (m *calendarMigration) rewrite(doc, root *yaml.Node, si int) error {
	keyHead := root.Content[si-1].HeadComment
	var fresh []*yaml.Node
	add := func(key string, v any) error {
		var n yaml.Node
		if err := n.Encode(v); err != nil {
			return err
		}
		fresh = append(fresh, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
		return nil
	}
	if len(m.accounts) > 0 {
		if err := add("accounts", m.accounts); err != nil {
			return err
		}
	}
	if err := add("calendars", m.calendars); err != nil {
		return err
	}
	if err := add("destinations", m.destinations); err != nil {
		return err
	}
	// The old key's head comment is stale ("Calendar sources") unless the key
	// was first, where it may be the file header.
	if si == 1 {
		fresh[0].HeadComment = keyHead
	} else {
		fresh[0].HeadComment = "Accounts, calendars and destinations (converted from the legacy sources and destinations)."
	}

	var content []*yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		switch root.Content[i].Value {
		case "sources":
			content = append(content, fresh...)
		case "destinations":
		default:
			content = append(content, root.Content[i], root.Content[i+1])
		}
	}
	root.Content = content
	return nil
}
