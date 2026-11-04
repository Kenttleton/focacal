package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const legacyYAML = `# focacal configuration
# header comment

working_hours:
  start: "09:00"
  end: "18:00"
  timezone: "America/New_York" # IANA timezone name
lunch:
  preferred: "12:00" # ideal lunch start time
  window_start: "11:30"
  window_end: "13:30"
  duration_minutes: 45
focus:
  # Style options
  style: "pomodoro"
  min_block_minutes: 30
  max_block_minutes: 90
  buffer_around_meetings: 10
colors:
  focus: "#00aa00"
ollama:
  base_url: "http://host.docker.internal:11434" # unknown key, stays
# Calendar sources
sources:
  - id: "work-google" # unique slug
    type: "google"
    calendar_ids: []
destinations:
  - id: work-google
    calendar_id: primary
    sources:
      - id: work-google
server:
  caldav_port: 6232
  rest_port: 6233
  mcp_port: 6234
  oauth_port: 9080
oauth:
  redirect_base_url: "http://example.test:8080"
auth:
  enabled: true
  password_hash: $2a$10$hashhashhash
  session_token: $2a$10$tokentokentoken
  username: admin
`

func writeLegacy(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func topKeys(t *testing.T, src string) []string {
	t.Helper()
	var m yaml.Node
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Content) == 0 {
		return nil
	}
	var keys []string
	root := m.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		keys = append(keys, root.Content[i].Value)
	}
	return keys
}

func TestMigrateLegacySplitsFiles(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	// What the single file meant: every domain read from the same bytes.
	b := []byte(legacyYAML)
	want, err := loadFiles(map[Domain][]byte{DomainApp: b, DomainPlanner: b, DomainCalendars: b})
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacy(dir)
	if err != nil || !migrated {
		t.Fatalf("MigrateLegacy = %v, %v; want true, nil", migrated, err)
	}

	wantKeys := map[string][]string{
		"config.yaml":    {"ollama", "server", "oauth", "auth"},
		"planner.yaml":   {"working_hours", "lunch", "focus", "colors"},
		"calendars.yaml": {"sources", "destinations"},
	}
	for f, keys := range wantKeys {
		if got := topKeys(t, readFile(t, filepath.Join(dir, f))); !reflect.DeepEqual(got, keys) {
			t.Errorf("%s keys = %v, want %v", f, got, keys)
		}
	}

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir after migrate: %v", err)
	}
	for _, d := range Domains {
		if !reflect.DeepEqual(projection(got, d), projection(want, d)) {
			t.Errorf("domain %s values changed:\n got %#v\nwant %#v", d, projection(got, d), projection(want, d))
		}
	}

	planner := readFile(t, filepath.Join(dir, "planner.yaml"))
	legacyNote := "# Legacy: replaced by planner rules (Routine, Focus, Transition). Do not extend."
	for _, key := range []string{"lunch:", "focus:"} {
		i := strings.Index(planner, "\n"+key)
		if i < 0 || !strings.HasSuffix(planner[:i], legacyNote) {
			t.Errorf("planner.yaml: %s not preceded by legacy note:\n%s", key, planner)
		}
	}
	// Comments travel with their keys.
	for _, c := range []string{"# IANA timezone name", "# ideal lunch start time", "# Style options"} {
		if !strings.Contains(planner, c) {
			t.Errorf("planner.yaml lost comment %q:\n%s", c, planner)
		}
	}
	cal := readFile(t, filepath.Join(dir, "calendars.yaml"))
	for _, c := range []string{"# Calendar sources", "# unique slug"} {
		if !strings.Contains(cal, c) {
			t.Errorf("calendars.yaml lost comment %q:\n%s", c, cal)
		}
	}
	app := readFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(app, "# focacal configuration") {
		t.Errorf("config.yaml lost its header comment:\n%s", app)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("first run = %v, %v", ok, err)
	}
	before := snapshotFiles(t, dir)
	ok, err := MigrateLegacy(dir)
	if err != nil || ok {
		t.Fatalf("second run = %v, %v; want false, nil", ok, err)
	}
	assertFilesUnchanged(t, dir, before)
	// Snapshot covers domain files; check the rest of the dir too.
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"calendars.yaml", "config.yaml", "config.yaml.bak", "credentials.yaml", "planner.yaml"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("dir = %v, want %v", names, want)
	}
}

func TestMigrateNoopOnNewLayoutOrMissingFile(t *testing.T) {
	empty := t.TempDir()
	if ok, err := MigrateLegacy(empty); err != nil || ok {
		t.Fatalf("missing config.yaml = %v, %v", ok, err)
	}
	dir := writeTestDir(t)
	before := snapshotFiles(t, dir)
	if ok, err := MigrateLegacy(dir); err != nil || ok {
		t.Fatalf("new layout = %v, %v", ok, err)
	}
	assertFilesUnchanged(t, dir, before)
	if _, err := os.Stat(filepath.Join(dir, "config.yaml.bak")); !os.IsNotExist(err) {
		t.Errorf("no-op migration wrote a backup: %v", err)
	}
}

func TestMigrateKeepsBackupAndUnknownKeys(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v", ok, err)
	}
	bakPath := filepath.Join(dir, "config.yaml.bak")
	if got := readFile(t, bakPath); got != legacyYAML {
		t.Errorf("backup differs from the original:\n%s", got)
	}
	st, err := os.Stat(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("backup mode = %o, want 0600 (it holds the password hash)", st.Mode().Perm())
	}
	app := readFile(t, filepath.Join(dir, "config.yaml"))
	if !strings.Contains(app, "ollama:") || !strings.Contains(app, "# unknown key, stays") {
		t.Errorf("unknown key or its comment dropped:\n%s", app)
	}
	for _, gone := range []string{"password_hash", "session_token", "username", "working_hours", "sources"} {
		if strings.Contains(app, gone) {
			t.Errorf("config.yaml still contains %q:\n%s", gone, app)
		}
	}
	if !strings.Contains(app, "enabled: true") {
		t.Errorf("auth.enabled dropped:\n%s", app)
	}
}

func TestMigrateMovesCredentials(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v", ok, err)
	}
	c, ok, err := LoadCredentials(dir)
	if err != nil || !ok {
		t.Fatalf("LoadCredentials = %v, %v", ok, err)
	}
	if c.Username != "admin" || c.PasswordHash != "$2a$10$hashhashhash" {
		t.Errorf("credentials = %+v", c)
	}
	st, err := os.Stat(filepath.Join(dir, CredentialsFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Errorf("credentials mode = %o, want 0600", st.Mode().Perm())
	}
	if strings.Contains(readFile(t, filepath.Join(dir, CredentialsFile)), "token") {
		t.Errorf("session_token copied into credentials.yaml")
	}
}

func TestMigrateKeepsExistingCredentials(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	existing := Credentials{Username: "kent", PasswordHash: "$2a$10$newer"}
	if err := SetupCredentials(dir, existing); err != nil {
		t.Fatal(err)
	}
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v", ok, err)
	}
	c, _, err := LoadCredentials(dir)
	if err != nil || c != existing {
		t.Errorf("credentials = %+v, %v; want %+v untouched", c, err, existing)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "config.yaml")), "password_hash") {
		t.Errorf("stale hash left in config.yaml")
	}
}

func TestMigrateCredentialsOnly(t *testing.T) {
	// A config.yaml with login credentials but no planner/calendar keys must
	// still move them, or setup would reopen on upgrade.
	dir := writeLegacy(t, "server:\n  rest_port: 5233\nauth:\n  enabled: true\n  username: admin\n  password_hash: $2a$10$h\n")
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v", ok, err)
	}
	if c, ok, _ := LoadCredentials(dir); !ok || c.PasswordHash != "$2a$10$h" {
		t.Errorf("credentials = %+v, %v", c, ok)
	}
	for _, f := range []string{"planner.yaml", "calendars.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s created for a credentials-only migration", f)
		}
	}
	if ok, err := MigrateLegacy(dir); err != nil || ok {
		t.Errorf("second run = %v, %v", ok, err)
	}
}

func TestMigrateRefusesToOverwriteCalendars(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte("sources: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	if _, err := MigrateLegacy(dir); err == nil {
		t.Fatal("want an error when calendars.yaml exists and config.yaml still has sources")
	}
	assertFilesUnchanged(t, dir, before)
}

func TestMigrateSkipsWhenPlannerExists(t *testing.T) {
	dir := writeLegacy(t, "working_hours:\n  start: \"08:00\"\n")
	if err := os.WriteFile(filepath.Join(dir, "planner.yaml"), []byte("working_hours:\n  start: \"10:00\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	if ok, err := MigrateLegacy(dir); err != nil || ok {
		t.Fatalf("MigrateLegacy = %v, %v; want false, nil", ok, err)
	}
	assertFilesUnchanged(t, dir, before)
	if got := LegacyKeys(dir); !reflect.DeepEqual(got, []string{"working_hours"}) {
		t.Errorf("LegacyKeys = %v", got)
	}
}

// The example config/ shipped in the repo must load as-is, need no
// migration, and hold no login secrets.
func TestRepoConfigDirIsCleanNewLayout(t *testing.T) {
	src := filepath.Join("..", "..", "config")
	if _, err := LoadDir(src); err != nil {
		t.Fatalf("LoadDir(config/): %v", err)
	}
	dir := t.TempDir()
	for _, d := range Domains {
		b, err := os.ReadFile(filepath.Join(src, d.File()))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"password_hash", "session_token", "$2a$"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("%s contains %q", d.File(), secret)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, d.File()), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := MigrateLegacy(dir); err != nil || ok {
		t.Errorf("MigrateLegacy(config/) = %v, %v; want no-op", ok, err)
	}
	if keys := LegacyKeys(dir); len(keys) > 0 {
		t.Errorf("config/config.yaml holds domain keys %v", keys)
	}
	if _, err := os.Stat(filepath.Join(src, CredentialsFile)); !os.IsNotExist(err) {
		t.Errorf("config/credentials.yaml must not be in the repo")
	}
}
