package config

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// calendarsConfig returns a valid config holding one google account with two
// calendars, one feed and one served destination.
func calendarsConfig() *Config {
	c := validConfig()
	c.Accounts = []AccountConfig{{ID: "g", Type: "google", Password: "SECRETPW"}}
	c.Calendars = []CalendarConfig{
		{ID: "work", Account: "g"},
		{ID: "home", Account: "g", RemoteID: "home"},
		{ID: "feed", URL: "https://secret.example/SECRETURL.ics"},
	}
	c.Destinations = []DestinationConfig{{
		ID:     "out",
		Inputs: InputList{Items: []DestinationInput{{Calendar: "work"}, {Calendar: FocacalSource}}},
		Target: DestinationTarget{Served: true},
	}}
	c.Resolve()
	return c
}

func TestValidateCalendarsRules(t *testing.T) {
	if err := calendarsConfig().ValidateCalendars(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	cases := map[string]struct {
		mut  func(*Config)
		want string // substring that must appear (an id or a phrase)
	}{
		"duplicate account id": {func(c *Config) {
			c.Accounts = append(c.Accounts, AccountConfig{ID: "g", Type: "google"})
		}, `"g"`},
		"duplicate calendar id ignoring case": {func(c *Config) {
			c.Calendars = append(c.Calendars, CalendarConfig{ID: "WORK", Account: "g"})
		}, "WORK"},
		"duplicate destination id": {func(c *Config) {
			c.Destinations = append(c.Destinations, c.Destinations[0])
		}, `"out"`},
		"account type": {func(c *Config) {
			c.Accounts[0].Type = "nope"
		}, `"g"`},
		"ics is not an account type": {func(c *Config) {
			c.Accounts[0].Type = "ics"
		}, `"g"`},
		"calendar with account and url": {func(c *Config) {
			c.Calendars[2].Account = "g"
		}, `"feed"`},
		"calendar with neither": {func(c *Config) {
			c.Calendars = append(c.Calendars, CalendarConfig{ID: "bare"})
		}, `"bare"`},
		"calendar missing account": {func(c *Config) {
			c.Calendars[0].Account = "ghost"
		}, `"ghost"`},
		"reserved calendar id": {func(c *Config) {
			c.Calendars = append(c.Calendars, CalendarConfig{ID: "focacal", URL: "https://x/y.ics"})
		}, "focacal"},
		"input missing calendar": {func(c *Config) {
			c.Destinations[0].Inputs.Items = append(c.Destinations[0].Inputs.Items, DestinationInput{Calendar: "ghost"})
		}, `"ghost"`},
		"bad kind": {func(c *Config) {
			c.Destinations[0].Inputs.Items[1].Kinds = []string{"focus", "nap"}
		}, `"out"`},
		"kinds on a calendar input": {func(c *Config) {
			c.Destinations[0].Inputs.Items[0].Kinds = []string{"focus"}
		}, `"out"`},
		"target both": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Calendar: "home", Served: true}
		}, `"out"`},
		"target neither": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{}
		}, `"out"`},
		"target missing calendar": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Calendar: "ghost"}
		}, `"ghost"`},
		"url-only target is not writable": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Calendar: "feed"}
		}, `"feed"`},
		"public auth": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Served: true, Auth: "public"}
		}, "not available yet"},
		"unknown auth": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Served: true, Auth: "basic"}
		}, `"out"`},
		"auth without served": {func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Calendar: "home", Auth: "private"}
		}, `"out"`},
		"input equals target": {func(c *Config) {
			c.Destinations[0].Inputs.Items = []DestinationInput{{Calendar: "home"}}
			c.Destinations[0].Target = DestinationTarget{Calendar: "home"}
		}, `"out"`},
		"two destination chain": {func(c *Config) {
			c.Destinations = []DestinationConfig{
				{ID: "ab", Inputs: InputList{Items: []DestinationInput{{Calendar: "work"}}}, Target: DestinationTarget{Calendar: "home"}},
				{ID: "ba", Inputs: InputList{Items: []DestinationInput{{Calendar: "home"}}}, Target: DestinationTarget{Calendar: "work"}},
			}
		}, `"ab"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := calendarsConfig()
			tc.mut(c)
			c.Resolve()
			err := c.ValidateCalendars()
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			for _, secret := range []string{"SECRETPW", "SECRETURL", "secret.example"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks %s: %v", secret, err)
				}
			}
		})
	}
}

func TestValidateCalendarsAcceptsValidShapes(t *testing.T) {
	cases := map[string]func(*Config){
		"account without remote_id": func(c *Config) { c.Calendars[0].RemoteID = "" },
		"input focacal with kinds": func(c *Config) {
			c.Destinations[0].Inputs.Items[1].Kinds = []string{"focus", "lunch", "break"}
		},
		"plan false target": func(c *Config) {
			c.Calendars[1].Plan = boolPtr(false)
			c.Destinations[0].Target = DestinationTarget{Calendar: "home"}
		},
		"explicit private auth": func(c *Config) {
			c.Destinations[0].Target = DestinationTarget{Served: true, Auth: "private"}
		},
		"all inputs with calendar target": func(c *Config) {
			c.Destinations[0].Inputs = InputList{All: true}
			c.Destinations[0].Target = DestinationTarget{Calendar: "home"}
		},
		"chain without a cycle": func(c *Config) {
			c.Destinations = []DestinationConfig{
				{ID: "ab", Inputs: InputList{Items: []DestinationInput{{Calendar: "work"}}}, Target: DestinationTarget{Calendar: "home"}},
				{ID: "bc", Inputs: InputList{Items: []DestinationInput{{Calendar: "home"}}}, Target: DestinationTarget{Served: true}},
			}
		},
		"focacal feeding a calendar": func(c *Config) {
			c.Destinations[0].Inputs = InputList{Items: []DestinationInput{{Calendar: FocacalSource}}}
			c.Destinations[0].Target = DestinationTarget{Calendar: "work"}
		},
		"empty config": func(c *Config) {
			c.Accounts, c.Calendars, c.Destinations = nil, nil, nil
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := calendarsConfig()
			mut(c)
			c.Resolve()
			if err := c.ValidateCalendars(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCycleReportsDestinationIDs(t *testing.T) {
	c := calendarsConfig()
	c.Destinations = []DestinationConfig{
		{ID: "ab", Inputs: InputList{Items: []DestinationInput{{Calendar: "work"}}}, Target: DestinationTarget{Calendar: "home"}},
		{ID: "ba", Inputs: InputList{Items: []DestinationInput{{Calendar: "home"}}}, Target: DestinationTarget{Calendar: "work"}},
		{ID: "side", Inputs: InputList{Items: []DestinationInput{{Calendar: "feed"}}}, Target: DestinationTarget{Served: true}},
	}
	err := c.ValidateCalendars()
	if err == nil || !strings.Contains(err.Error(), `"ab"`) || !strings.Contains(err.Error(), `"ba"`) {
		t.Fatalf("err = %v, want both cycle destinations", err)
	}
	if strings.Contains(err.Error(), "side") {
		t.Errorf("a destination off the cycle is named: %v", err)
	}
}

func TestAllToTwoCalendarTargetsIsACycle(t *testing.T) {
	c := calendarsConfig()
	c.Destinations = []DestinationConfig{
		{ID: "to-work", Inputs: InputList{All: true}, Target: DestinationTarget{Calendar: "work"}},
		{ID: "to-home", Inputs: InputList{All: true}, Target: DestinationTarget{Calendar: "home"}},
	}
	if err := c.ValidateCalendars(); err == nil {
		t.Fatal("two all-destinations into calendars mirror each other and must be rejected")
	}
}

// A hand-edited file with dangling references loads with warnings (ids only)
// and the dangling entries are skipped at runtime; the API (strict) refuses.
const danglingYAML = `accounts:
  - id: g
    type: google
calendars:
  - id: work
    account: g
  - id: orphan
    account: gone
destinations:
  - id: out
    inputs:
      - calendar: work
      - calendar: missing
    target:
      served: true
  - id: inactive
    inputs: all
    target:
      calendar: nowhere
`

func TestDanglingReferencesLoadWithWarning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(danglingYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
	started, running, err := LoadStart(dir)
	if err != nil {
		t.Fatalf("LoadStart: %v", err)
	}
	for name, c := range map[string]*Config{"started": started, "running": running} {
		if len(c.Warnings) != 3 {
			t.Errorf("%s: warnings = %q, want 3", name, c.Warnings)
		}
		joined := strings.Join(c.Warnings, "\n")
		for _, id := range []string{"orphan", "gone", "missing", "inactive", "nowhere"} {
			if !strings.Contains(joined, id) {
				t.Errorf("%s: warnings do not name %q: %s", name, id, joined)
			}
		}
		if len(c.Sources) != 1 || c.Sources[0].ID != "work" {
			t.Errorf("%s: orphan calendar not skipped: %+v", name, c.Sources)
		}
		if got := c.ExpandInputs(c.Destinations[0]); len(got) != 1 || got[0].Calendar != "work" {
			t.Errorf("%s: missing input not skipped: %+v", name, got)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
	if !bytes.Equal(before, after) {
		t.Error("a hand-edited file was rewritten on load")
	}
	if _, err := LoadDir(dir); err != nil {
		t.Errorf("LoadDir: %v", err)
	}
}

func TestDanglingReferencesRejectedByPatch(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	for name, body := range map[string]string{
		"calendar account": `{"calendars":{"x":{"account":"ghost"}}}`,
		"input":            `{"destinations":{"out":{"inputs":[{"calendar":"ghost"}]}}}`,
		"target":           `{"destinations":{"d2":{"inputs":"all","target":{"calendar":"ghost"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Patch(DomainCalendars, []byte(body))
			if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "ghost") {
				t.Fatalf("err = %v, want ErrValidation naming ghost", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

func TestLiveLogsNewWarningsOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "calendars.yaml", danglingYAML)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	l, err := NewLive(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "orphan") {
		t.Errorf("initial warnings not logged: %q", buf.String())
	}
	buf.Reset()
	if _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("unchanged warnings logged again: %q", buf.String())
	}
	writeFile(t, dir, "calendars.yaml", danglingYAML+"  - id: third\n    inputs: all\n    target:\n      calendar: also-gone\n")
	if _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "third") || strings.Contains(out, "orphan") {
		t.Errorf("only the new warning should be logged: %q", out)
	}
}

func TestDecodeRejectsLegacySourcesKey(t *testing.T) {
	in := "sources:\n  - id: work\n    type: ics\n    ics_url: https://x/LEAKME.ics\n"
	err := decodeDomain(DomainCalendars, []byte(in), Defaults())
	if err == nil {
		t.Fatal("legacy sources key must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sources") || !strings.Contains(msg, "restart") {
		t.Errorf("error should name the key and say to restart: %v", err)
	}
	if strings.Contains(msg, "LEAKME") || strings.Contains(msg, "work") {
		t.Errorf("error echoes values: %v", err)
	}
	// Other domains still ignore a stray sources key.
	if err := decodeDomain(DomainApp, []byte(in), Defaults()); err != nil {
		t.Errorf("app domain must ignore stray keys: %v", err)
	}
}

func TestDecodeAcceptsLabelAlias(t *testing.T) {
	in := `accounts:
  - id: g
    label: Work account
    type: google
calendars:
  - id: work
    label: Work
    account: g
destinations:
  - id: out
    label: Output
    inputs: all
    target: {served: true}
`
	c := Defaults()
	if err := decodeDomain(DomainCalendars, []byte(in), c); err != nil {
		t.Fatal(err)
	}
	if c.Accounts[0].Name != "Work account" || c.Calendars[0].Name != "Work" || c.Destinations[0].Name != "Output" {
		t.Errorf("label not read as name: %+v", c.UserConfig)
	}
	out, err := marshalDomain(DomainCalendars, c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "label") || !strings.Contains(string(out), "name: Work") {
		t.Errorf("save must write name only:\n%s", out)
	}
	// name wins when both are present.
	both := "calendars:\n  - id: a\n    url_env: FOCACAL_A\n    name: New\n    label: Old\n"
	c = Defaults()
	if err := decodeDomain(DomainCalendars, []byte(both), c); err != nil {
		t.Fatal(err)
	}
	if c.Calendars[0].Name != "New" {
		t.Errorf("name = %q, want New", c.Calendars[0].Name)
	}
}

// Hand-written ids that do not match the id shape keep loading; the API
// refuses to create them but edits around them still work.
func TestNonConformingIDsLoadButCannotBeCreated(t *testing.T) {
	dir := t.TempDir()
	body := "calendars:\n  - id: Work\n    url_env: FOCACAL_W\n  - id: \"a:b\"\n    url_env: FOCACAL_AB\n"
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadStart(dir); err != nil {
		t.Fatalf("hand-written ids must load: %v", err)
	}
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	for _, p := range []string{
		`{"calendars":{"Fresh":{"url_env":"FOCACAL_F"}}}`,
		`{"calendars":{"x:y":{"url_env":"FOCACAL_F"}}}`,
	} {
		if _, err := svc.Patch(DomainCalendars, []byte(p)); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", p, err)
		}
	}
	assertFilesUnchanged(t, dir, before)
	// An unrelated edit to a file holding the odd ids succeeds and keeps them.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"ok-new":{"url_env":"FOCACAL_OK"}}}`)); err != nil {
		t.Fatalf("unrelated create: %v", err)
	}
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"Work":{"name":"Renamed"}}}`)); err != nil {
		t.Fatalf("edit of an existing odd id: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
	if !strings.Contains(string(b), "id: Work") || !strings.Contains(string(b), "a:b") {
		t.Errorf("odd ids lost:\n%s", b)
	}
}
