package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
		"config.yaml":    {"ollama", "server", "auth"},
		"planner.yaml":   {"working_hours", "lunch", "focus", "colors"},
		"calendars.yaml": {"sources", "destinations", "oauth"},
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

func TestMigrateRefusesWhenPlannerExists(t *testing.T) {
	dir := writeLegacy(t, "working_hours:\n  start: \"08:00\"\n")
	if err := os.WriteFile(filepath.Join(dir, "planner.yaml"), []byte("working_hours:\n  start: \"10:00\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	ok, err := MigrateLegacy(dir)
	if err == nil || ok {
		t.Fatalf("MigrateLegacy = %v, %v; want an error", ok, err)
	}
	for _, want := range []string{"working_hours", "planner.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	assertFilesUnchanged(t, dir, before)
}

// The documented upgrade used to be "mv config.yaml config/" into a checkout
// whose config/ already holds the sample planner.yaml and calendars.yaml. The
// user's values must not be silently replaced by the samples (R13).
func TestMigrateRefusesLegacyNextToSampleFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"planner.yaml", "calendars.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "config.example", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	custom := strings.Replace(legacyYAML, `start: "09:00"`, `start: "07:00"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, dir)
	_, err := MigrateLegacy(dir)
	if err == nil {
		t.Fatal("want an error: config.yaml holds planner/calendars keys and the targets exist")
	}
	for _, want := range []string{"planner.yaml", "delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	assertFilesUnchanged(t, dir, before)
}

// A crash after some target files were written must not wedge the next start:
// target files equal to what the migration would write count as done.
func TestMigrateResumesAfterCrash(t *testing.T) {
	clean := writeLegacy(t, legacyYAML)
	if ok, err := MigrateLegacy(clean); err != nil || !ok {
		t.Fatalf("clean run = %v, %v", ok, err)
	}
	want := snapshotFiles(t, clean)

	for name, remove := range map[string][]string{
		"after calendars.yaml":    {"planner.yaml"},
		"after planner.yaml":      {"calendars.yaml"},
		"after both, before trim": nil,
		"after credentials only":  {"planner.yaml", "calendars.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for f, body := range want {
				mode := os.FileMode(0644)
				if f == CredentialsFile || f == "config.yaml.bak" {
					mode = 0600
				}
				if err := os.WriteFile(filepath.Join(dir, f), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			// Crash state: config.yaml not yet trimmed, .bak not yet written.
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacyYAML), 0644); err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(dir, "config.yaml.bak"))
			for _, f := range remove {
				os.Remove(filepath.Join(dir, f))
			}
			ok, err := MigrateLegacy(dir)
			if err != nil || !ok {
				t.Fatalf("rerun = %v, %v; want true, nil", ok, err)
			}
			if got := snapshotFiles(t, dir); !reflect.DeepEqual(got, want) {
				t.Errorf("resumed result differs from a clean migration:\n got %v\nwant %v", got, want)
			}
		})
	}
}

// Anchors defined under one domain's key and aliased under another's would
// leave a dangling alias in one of the new files. Refuse, write nothing.
func TestMigrateRejectsCrossDomainAnchors(t *testing.T) {
	dir := writeLegacy(t, "server:\n  bind_address: &b \"0.0.0.0\"\nworking_hours:\n  start: \"09:00\"\n  timezone: *b\n")
	before := snapshotFiles(t, dir)
	if _, err := MigrateLegacy(dir); err == nil {
		t.Fatal("want an error for an alias across domain files")
	}
	assertFilesUnchanged(t, dir, before)
}

// A failure before config.yaml is rewritten removes everything this run
// created and leaves config.yaml as it was.
func TestMigrateCleansUpOnFailure(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	// A directory where the backup should go makes the .bak write fail after
	// credentials, planner.yaml and calendars.yaml were written.
	if err := os.Mkdir(filepath.Join(dir, "config.yaml.bak"), 0755); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, dir)
	if _, err := MigrateLegacy(dir); err == nil {
		t.Fatal("want an error when the backup cannot be written")
	}
	if got := snapshotDir(t, dir); !reflect.DeepEqual(got, before) {
		t.Errorf("dir changed after failed migration:\nbefore %v\nafter  %v", before, got)
	}
}

// Once the config.yaml rewrite has started, credentials.yaml must survive a
// failure: the in-place fallback may already have truncated config.yaml.
func TestMigrateKeepsCredentialsWhenConfigWriteFails(t *testing.T) {
	dir := writeLegacy(t, legacyYAML)
	renameFile = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { renameFile = os.Rename })
	if err := os.Chmod(filepath.Join(dir, "config.yaml"), 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacy(dir); err == nil {
		t.Fatal("want an error when config.yaml cannot be rewritten")
	}
	if c, ok, _ := LoadCredentials(dir); !ok || c.PasswordHash != "$2a$10$hashhashhash" {
		t.Fatalf("credentials lost after a failed config.yaml rewrite: %+v %v", c, ok)
	}
	// After the cause is fixed the next start completes the migration.
	renameFile = os.Rename
	if err := os.Chmod(filepath.Join(dir, "config.yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("rerun = %v, %v", ok, err)
	}
	if keys := LegacyKeys(dir); len(keys) != 0 {
		t.Errorf("legacy keys left: %v", keys)
	}
}

// snapshotDir is snapshotFiles that also records subdirectories.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			out[e.Name()] = "<dir>"
			continue
		}
		out[e.Name()] = readFile(t, filepath.Join(dir, e.Name()))
	}
	return out
}

// The example config/ shipped in the repo must load as-is, need no
// migration, and hold no login secrets.
func TestRepoConfigDirIsCleanNewLayout(t *testing.T) {
	src := filepath.Join("..", "..", "config.example")
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
	// Tracked files, not the working tree: a local run may leave a
	// (gitignored) credentials.yaml in config/.
	out, err := exec.Command("git", "ls-files", src).Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	var tracked []string
	for _, f := range strings.Fields(string(out)) {
		if filepath.Ext(f) == ".yaml" {
			tracked = append(tracked, filepath.Base(f))
		}
	}
	if want := []string{"calendars.yaml", "config.yaml", "planner.yaml"}; !reflect.DeepEqual(tracked, want) {
		t.Errorf("tracked config.example/ yaml files = %v, want %v (never credentials.yaml or *.bak)", tracked, want)
	}
}

// config.yaml written by the previous version holds agent and oauth. Next to
// the shipped sample planner.yaml and calendars.yaml they move into those
// files instead of refusing.
func TestMigrate_NewLayoutConfigWithAgentKey(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"planner.yaml", "calendars.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "config.example", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := `server:
  caldav_port: 5232
  rest_port: 5233
  mcp_port: 5234
  oauth_port: 8080
agent:
  base_url: "http://ollama:11434/v1"
  model: "llama3"
  timeout_secs: 90
  max_tokens: 2048
oauth:
  redirect_base_url: "http://localhost:8080"
auth:
  enabled: true
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v; want true, nil", ok, err)
	}
	if got := topKeys(t, readFile(t, filepath.Join(dir, "config.yaml"))); !reflect.DeepEqual(got, []string{"server", "auth"}) {
		t.Errorf("config.yaml keys = %v, want [server auth]", got)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "planner.yaml")), "agent:") {
		t.Error("planner.yaml lacks agent")
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "calendars.yaml")), "oauth:") {
		t.Error("calendars.yaml lacks oauth")
	}
	c, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if c.Agent.Model != "llama3" || c.Agent.TimeoutSecs != 90 || c.Agent.BaseURL != "http://ollama:11434/v1" {
		t.Errorf("agent = %+v", c.Agent)
	}
	if keys := LegacyKeys(dir); len(keys) > 0 {
		t.Errorf("LegacyKeys = %v, want none", keys)
	}
	if ok, err := MigrateLegacy(dir); err != nil || ok {
		t.Errorf("second run = %v, %v; want false, nil", ok, err)
	}
}

func copySamples(t *testing.T, dir string) {
	t.Helper()
	for _, f := range []string{"planner.yaml", "calendars.yaml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "config.example", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrate_CustomOAuthNextToSampleFiles(t *testing.T) {
	dir := t.TempDir()
	copySamples(t, dir)
	old := "server:\n  caldav_port: 5232\n  rest_port: 5233\n  mcp_port: 5234\n  oauth_port: 8080\noauth:\n  redirect_base_url: \"https://cal.example.com\"\nauth:\n  enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v; want true, nil", ok, err)
	}
	c, err := LoadDir(dir)
	if err != nil || c.OAuth.RedirectBaseURL != "https://cal.example.com" {
		t.Fatalf("LoadDir = %+v, %v", c.OAuth, err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "calendars.yaml")), "https://cal.example.com") {
		t.Error("custom value not in calendars.yaml")
	}
}

func TestMigrate_RefusesDifferingAgentOrOAuth(t *testing.T) {
	cases := map[string]struct{ file, target, legacy string }{
		"agent": {"planner.yaml", "agent:\n  model: \"a\"\n", "agent:\n  model: \"b\"\n"},
		"oauth": {"calendars.yaml", "oauth:\n  redirect_base_url: \"http://a.test\"\n", "oauth:\n  redirect_base_url: \"http://b.test\"\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeLegacy(t, tc.legacy+"auth:\n  enabled: true\n")
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.target), 0644); err != nil {
				t.Fatal(err)
			}
			before := snapshotFiles(t, dir)
			if ok, err := MigrateLegacy(dir); err == nil || ok {
				t.Fatalf("MigrateLegacy = %v, %v; want an error", ok, err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

func TestMigrate_MergesIntoPopulatedTarget(t *testing.T) {
	dir := writeLegacy(t, "agent:\n  model: \"m\"\n  timeout_secs: 30\n  max_tokens: 100\n  base_url: \"http://x/v1\"\nauth:\n  enabled: true\n")
	existing := "# mine\nworking_hours:\n  start: \"08:00\"\n  end: \"16:00\"\n  timezone: \"UTC\" # keep\n"
	if err := os.WriteFile(filepath.Join(dir, "planner.yaml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := MigrateLegacy(dir); err != nil || !ok {
		t.Fatalf("MigrateLegacy = %v, %v", ok, err)
	}
	p := readFile(t, filepath.Join(dir, "planner.yaml"))
	for _, want := range []string{"# mine", "# keep", `"08:00"`, "agent:", "timeout_secs: 30"} {
		if !strings.Contains(p, want) {
			t.Errorf("planner.yaml lacks %q:\n%s", want, p)
		}
	}
	c, err := LoadDir(dir)
	if err != nil || c.WorkingHours.Start != "08:00" || c.Agent.Model != "m" {
		t.Fatalf("LoadDir = %+v, %v", c, err)
	}
}
