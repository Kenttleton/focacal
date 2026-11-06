package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preChangeExampleCalendars is config.example/calendars.yaml as shipped
// before the calendar model (commit 4c2fa4b).
const preChangeExampleCalendars = `# Calendars: sources (accounts read from), destinations and the OAuth redirect.
# Secrets do not belong here: use ics_url_env / credentials_file references.
# Relative credentials_file / token_file paths resolve under this directory.
# Edits apply live within about a second. Saving from the UI rewrites this file
# without comments, so keep notes in your copy of the sample, not the live file.

# Calendar sources — add one entry per Google or Outlook account.
sources:
  - id: "work-google" # unique slug
    type: "google"
    calendar_ids: [] # empty = primary calendar only
    # - id: "personal-outlook"
    #   type: "outlook"
    #   calendar_ids: []
    # - id: "team-feed"
    #   type: "ics"
    #   ics_url_env: "FOCACAL_ICS_TEAM_FEED" # feed URL lives in .env; if unset,
    #                                        # only this calendar fails to sync

# OAuth redirect base. Defaults shown; uncomment to change. Also configurable
# via the OAUTH_REDIRECT_BASE_URL env var.
# oauth:
#   redirect_base_url: "http://localhost:8080"
`

// migrateCal writes body as calendars.yaml, migrates, and returns the dir, the
// warnings logged and whether it migrated.
func migrateCal(t *testing.T, body string) (dir string, warns string, migrated bool) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	migrated, err := MigrateCalendarModelLog(dir, log)
	if err != nil {
		t.Fatalf("MigrateCalendarModel: %v", err)
	}
	return dir, buf.String(), migrated
}

func loadCal(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir after migration: %v\n%s", err, readFile(t, filepath.Join(dir, "calendars.yaml")))
	}
	return cfg
}

func calByID(cfg *Config, id string) *CalendarConfig {
	for i := range cfg.Calendars {
		if cfg.Calendars[i].ID == id {
			return &cfg.Calendars[i]
		}
	}
	return nil
}

func accByID(cfg *Config, id string) *AccountConfig {
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == id {
			return &cfg.Accounts[i]
		}
	}
	return nil
}

func destByID(cfg *Config, id string) *DestinationConfig {
	for i := range cfg.Destinations {
		if cfg.Destinations[i].ID == id {
			return &cfg.Destinations[i]
		}
	}
	return nil
}

func TestMigrateSingleCalendarSource(t *testing.T) {
	dir, _, migrated := migrateCal(t, `sources:
  - id: work
    label: Work Google
    type: google
    credentials_file: creds.json
    token_file: tok.json
    writeback: true
    cross_busy: false
    conflicts:
      tentative: false
oauth:
  redirect_base_url: "http://example.test:8080"
`)
	if !migrated {
		t.Fatal("migrated = false")
	}
	if _, err := os.Stat(filepath.Join(dir, "calendars.yaml.bak")); err != nil {
		t.Errorf("no .bak: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "calendars.yaml.bak")), "sources:") {
		t.Error(".bak is not the original")
	}
	cfg := loadCal(t, dir)
	a := accByID(cfg, "work")
	if a == nil || a.Name != "Work Google" || a.Type != "google" || a.CredentialsFile != "creds.json" || a.TokenFile != "tok.json" {
		t.Errorf("account = %+v", a)
	}
	c := calByID(cfg, "work")
	if c == nil || c.Account != "work" || c.RemoteID != "primary" || c.Name != "Work Google" || !c.Plans() {
		t.Fatalf("calendar = %+v", c)
	}
	if c.Plan == nil {
		t.Error("plan is not written explicitly")
	}
	if c.Conflicts.Tentative == nil || *c.Conflicts.Tentative {
		t.Errorf("conflicts did not move: %+v", c.Conflicts)
	}
	if cfg.OAuth.RedirectBaseURL != "http://example.test:8080" {
		t.Errorf("oauth key lost: %+v", cfg.OAuth)
	}
	out := readFile(t, filepath.Join(dir, "calendars.yaml"))
	if strings.Contains(out, "sources:") || strings.Contains(out, "writeback") || strings.Contains(out, "label:") {
		t.Errorf("legacy keys remain:\n%s", out)
	}
}

func TestMigrateSourceNameFallsBackToID(t *testing.T) {
	dir, _, _ := migrateCal(t, "sources:\n  - id: Odd_ID\n    type: outlook\n")
	cfg := loadCal(t, dir)
	if a := accByID(cfg, "Odd_ID"); a == nil || a.Name != "Odd_ID" {
		t.Errorf("account = %+v (id kept exactly, name falls back to id)", a)
	}
	if c := calByID(cfg, "Odd_ID"); c == nil || c.RemoteID != "primary" {
		t.Errorf("calendar = %+v", c)
	}
}

func TestMigrateMultiCalendarSource(t *testing.T) {
	dir, _, _ := migrateCal(t, `sources:
  - id: work
    type: google
    calendar_ids: [primary, team@x.com, team@x-com, "team@x.com2"]
    conflicts:
      free: true
`)
	cfg := loadCal(t, dir)
	want := map[string]string{
		"work":              "primary",
		"work-team-x-com":   "team@x.com",
		"work-team-x-com-2": "team@x-com",
		"work-team-x-com2":  "team@x.com2",
	}
	if len(cfg.Calendars) != len(want) {
		t.Fatalf("calendars = %+v", cfg.Calendars)
	}
	for id, remote := range want {
		c := calByID(cfg, id)
		if c == nil || c.Account != "work" || c.RemoteID != remote || !c.Plans() {
			t.Errorf("calendar %s = %+v, want remote %s", id, c, remote)
		}
		if c != nil && (c.Conflicts.Free == nil || !*c.Conflicts.Free) {
			t.Errorf("calendar %s lost conflicts", id)
		}
	}
	if len(cfg.Accounts) != 1 {
		t.Errorf("accounts = %+v", cfg.Accounts)
	}
}

func TestMigrateSingleExplicitCalendarID(t *testing.T) {
	dir, _, _ := migrateCal(t, "sources:\n  - id: work\n    type: google\n    calendar_ids: [team@x.com]\n")
	cfg := loadCal(t, dir)
	if c := calByID(cfg, "work"); c == nil || c.RemoteID != "team@x.com" {
		t.Errorf("calendar = %+v", c)
	}
}

func TestMigrateGeneratedIDsAvoidOtherSources(t *testing.T) {
	dir, _, _ := migrateCal(t, `sources:
  - id: a
    type: google
    calendar_ids: [primary, b]
  - id: a-b
    type: google
`)
	cfg := loadCal(t, dir)
	if c := calByID(cfg, "a-b"); c == nil || c.Account != "a-b" {
		t.Errorf("source id a-b was displaced: %+v", cfg.Calendars)
	}
	if len(cfg.Calendars) != 3 {
		t.Errorf("calendars = %+v", cfg.Calendars)
	}
}

func TestMigrateICSSource(t *testing.T) {
	dir, _, _ := migrateCal(t, `sources:
  - id: team-feed
    label: Team
    type: ics
    ics_url_env: FOCACAL_ICS_TEAM
  - id: inline
    type: ics
    ics_url: https://example.test/secret.ics
`)
	cfg := loadCal(t, dir)
	if len(cfg.Accounts) != 0 {
		t.Errorf("ics produced accounts: %+v", cfg.Accounts)
	}
	c := calByID(cfg, "team-feed")
	if c == nil || c.Account != "" || c.URLEnv != "FOCACAL_ICS_TEAM" || c.URL != "" || c.Name != "Team" || c.RemoteID != "" {
		t.Errorf("calendar = %+v", c)
	}
	c = calByID(cfg, "inline")
	if c == nil || c.URL != "https://example.test/secret.ics" {
		t.Errorf("inline calendar = %+v", c)
	}
}

func TestMigrateCalDAVEmptyListKeepsDefault(t *testing.T) {
	dir, _, _ := migrateCal(t, `sources:
  - id: nc
    type: caldav
    caldav_url: https://dav.example.test
    username: me
    password: pw
    auth_type: basic
    calendar_ids: []
`)
	cfg := loadCal(t, dir)
	a := accByID(cfg, "nc")
	if a == nil || a.CalDAVURL != "https://dav.example.test" || a.Username != "me" || a.Password != "pw" || a.AuthType != "basic" {
		t.Errorf("account = %+v", a)
	}
	if c := calByID(cfg, "nc"); c == nil || c.RemoteID != "" || c.Account != "nc" {
		t.Errorf("calendar = %+v (want no remote_id)", c)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "calendars.yaml")), "remote_id") {
		t.Error("remote_id written for caldav with an empty list")
	}
}

func TestMigrateDestinationsBestEffort(t *testing.T) {
	dir, warns, _ := migrateCal(t, `sources:
  - id: work
    type: google
    calendar_ids: [primary, team@x.com]
destinations:
  - id: work
    calendar_id: team@x.com
    sources:
      - id: work
        calendar_id: team@x.com
      - id: work
        calendar_id: primary
        event:
          title: {visibility: true}
          body: {visibility: false}
      - id: focacal
  - id: out-google
    type: google
    credentials_file: out.json
    calendar_id: out@x.com
    sources:
      - id: work
  - id: out-outlook
    type: outlook
  - id: out-dav
    type: caldav
    caldav_url: https://dav.example.test
    username: u
    password: p
  - id: out-ics
    type: ics
    ics_url: https://example.test/SECRET-FEED
`)
	cfg := loadCal(t, dir)

	d := destByID(cfg, "work")
	if d == nil || d.Target.Calendar != "work-team-x-com" || d.Target.Served {
		t.Fatalf("matched destination = %+v", d)
	}
	// The input naming the destination's own target is dropped (a cycle).
	if len(d.Inputs.Items) != 2 || d.Inputs.Items[0].Calendar != "work" ||
		!d.Inputs.Items[0].Event.Title.Visibility || d.Inputs.Items[1].Calendar != "focacal" {
		t.Errorf("inputs = %+v", d.Inputs)
	}
	if len(cfg.Accounts) != 4 {
		t.Errorf("accounts = %+v (work plus three destination accounts)", cfg.Accounts)
	}

	d = destByID(cfg, "out-google")
	if d == nil {
		t.Fatalf("out-google missing: %+v", cfg.Destinations)
	}
	c := calByID(cfg, d.Target.Calendar)
	if c == nil || c.Account != "out-google" || c.RemoteID != "out@x.com" {
		t.Errorf("out-google target calendar = %+v", c)
	}
	if a := accByID(cfg, "out-google"); a == nil || a.CredentialsFile != "out.json" || a.Type != "google" {
		t.Errorf("out-google account = %+v", a)
	}
	if len(d.Inputs.Items) != 1 || d.Inputs.Items[0].Calendar != "work" {
		t.Errorf("out-google inputs = %+v (source work, no calendar_id: first calendar)", d.Inputs)
	}
	if d = destByID(cfg, "out-outlook"); d == nil || calByID(cfg, d.Target.Calendar).RemoteID != "primary" {
		t.Errorf("out-outlook = %+v", d)
	}
	if d = destByID(cfg, "out-dav"); d == nil || calByID(cfg, d.Target.Calendar).RemoteID != "" {
		t.Errorf("out-dav = %+v", d)
	}
	if destByID(cfg, "out-ics") != nil {
		t.Error("ics destination was carried over")
	}
	if !strings.Contains(warns, "out-ics") || !strings.Contains(warns, "WARN") {
		t.Errorf("warnings = %q, want one naming out-ics", warns)
	}
	if strings.Contains(warns, "SECRET-FEED") {
		t.Errorf("warning leaks a secret: %q", warns)
	}
	if destByID(cfg, "focacal") == nil {
		t.Error("default focacal destination not added")
	}
}

func TestMigrateAddsDefaultDestination(t *testing.T) {
	dir, _, _ := migrateCal(t, "sources:\n  - id: work\n    type: google\n")
	d := destByID(loadCal(t, dir), "focacal")
	if d == nil || d.Name != "Focacal" || !d.Inputs.All || !d.Target.Served || d.Target.Auth != "private" {
		t.Errorf("focacal destination = %+v", d)
	}
}

func TestMigrateKeepsExistingFocacalDestination(t *testing.T) {
	dir, _, _ := migrateCal(t, `sources:
  - id: work
    type: google
destinations:
  - id: focacal
    type: google
    sources: [{id: work}]
`)
	cfg := loadCal(t, dir)
	n := 0
	for _, d := range cfg.Destinations {
		if d.ID == "focacal" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("focacal destinations = %d, want 1: %+v", n, cfg.Destinations)
	}
}

func TestMigrateIdempotentAndAtomic(t *testing.T) {
	dir, _, _ := migrateCal(t, "sources:\n  - id: work\n    type: google\n")
	p := filepath.Join(dir, "calendars.yaml")
	before := readFile(t, p)
	bak := readFile(t, p+".bak")
	migrated, err := MigrateCalendarModel(dir)
	if err != nil || migrated {
		t.Fatalf("second run = %v, %v; want false, nil", migrated, err)
	}
	if readFile(t, p) != before || readFile(t, p+".bak") != bak {
		t.Error("second run wrote")
	}
}

func TestMigrateNoFileOrNothingToDo(t *testing.T) {
	dir := t.TempDir()
	if m, err := MigrateCalendarModel(dir); m || err != nil {
		t.Errorf("no file: %v %v", m, err)
	}
	// Already the new shape.
	_, _, migrated := migrateCal(t, "accounts: []\ncalendars: []\n")
	if migrated {
		t.Error("migrated a new-shape file")
	}
}

func TestMigrateWriteFailureKeepsOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "calendars.yaml")
	body := "sources:\n  - id: work\n    type: google\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755)
	migrated, err := MigrateCalendarModel(dir)
	if err == nil || migrated {
		t.Fatalf("got %v, %v; want an error", migrated, err)
	}
	if !strings.Contains(err.Error(), "calendars.yaml") {
		t.Errorf("error does not name the file: %v", err)
	}
	if readFile(t, p) != body {
		t.Error("original changed")
	}
}

func TestMigrateRenameFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "calendars.yaml")
	body := "sources:\n  - id: work\n    type: google\n"
	os.WriteFile(p, []byte(body), 0644)
	old := renameFile
	renameFile = func(a, b string) error { return os.ErrPermission }
	defer func() { renameFile = old }()
	// The writer falls back to an in-place rewrite, so this still succeeds
	// and yields a loadable file.
	if _, err := MigrateCalendarModel(dir); err != nil {
		t.Fatal(err)
	}
	loadCal(t, dir)
}

func TestMigratePreChangeExampleCalendars(t *testing.T) {
	dir, _, migrated := migrateCal(t, preChangeExampleCalendars)
	if !migrated {
		t.Fatal("not migrated")
	}
	cfg := loadCal(t, dir)
	if calByID(cfg, "work-google") == nil || accByID(cfg, "work-google") == nil || destByID(cfg, "focacal") == nil {
		t.Errorf("accounts=%+v calendars=%+v destinations=%+v", cfg.Accounts, cfg.Calendars, cfg.Destinations)
	}
}
