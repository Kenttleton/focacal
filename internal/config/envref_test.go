package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unresolvedCalendarsYAML has an ics source whose env var is not set (the
// usual state when .env never reached the container).
const unresolvedCalendarsYAML = `sources:
  - id: work
    type: google
  - id: team
    type: ics
    ics_url_env: FOCACAL_TEST_UNSET_FEED
`

func writeUnresolvedDir(t *testing.T) string {
	t.Helper()
	os.Unsetenv("FOCACAL_TEST_UNSET_FEED")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(unresolvedCalendarsYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// R14: an unresolved ics_url_env is a warning, never a load failure.
func TestLoadStartUnresolvedEnvRefWarns(t *testing.T) {
	dir := writeUnresolvedDir(t)
	started, running, err := LoadStart(dir)
	if err != nil {
		t.Fatalf("LoadStart failed on an unresolved env ref: %v", err)
	}
	for name, c := range map[string]*Config{"started": started, "running": running} {
		refs := c.UnresolvedEnvRefs()
		if len(refs) != 1 || refs[0].Kind != "source" || refs[0].ID != "team" || refs[0].Env != "FOCACAL_TEST_UNSET_FEED" {
			t.Errorf("%s: UnresolvedEnvRefs = %+v", name, refs)
		}
	}

	t.Setenv("FOCACAL_TEST_UNSET_FEED", "https://secret.example/feed.ics")
	_, running, err = LoadStart(dir)
	if err != nil {
		t.Fatal(err)
	}
	if refs := running.UnresolvedEnvRefs(); len(refs) != 0 {
		t.Errorf("resolved ref still reported: %+v", refs)
	}
}

func TestUnresolvedEnvRefsCoversDestinations(t *testing.T) {
	os.Unsetenv("FOCACAL_TEST_UNSET_OUT")
	c := validConfig()
	c.Destinations = []DestinationConfig{{ID: "out", CalendarID: "x", Type: "ics", ICSURLEnv: "FOCACAL_TEST_UNSET_OUT"}}
	refs := c.UnresolvedEnvRefs()
	if len(refs) != 1 || refs[0].Kind != "destination" || refs[0].ID != "out|x" || refs[0].Env != "FOCACAL_TEST_UNSET_OUT" {
		t.Errorf("UnresolvedEnvRefs = %+v", refs)
	}
	if err := c.ValidateCalendars(); err != nil {
		t.Errorf("unresolved destination ref must not fail validation: %v", err)
	}
}

func TestICSSourceNeedsAReference(t *testing.T) {
	c := validConfig()
	c.Sources = []SourceConfig{{ID: "bare", Type: "ics"}}
	err := c.ValidateCalendars()
	if err == nil || !strings.Contains(err.Error(), "bare") {
		t.Fatalf("ics source without ics_url or ics_url_env: err = %v", err)
	}
	c.Sources = []SourceConfig{{ID: "ref", Type: "ics", ICSURLEnv: "FOCACAL_TEST_UNSET_FEED"}}
	os.Unsetenv("FOCACAL_TEST_UNSET_FEED")
	if err := c.ValidateCalendars(); err != nil {
		t.Errorf("unresolved reference must validate: %v", err)
	}
}

func TestPatchWithUnresolvedEnvRefPresent(t *testing.T) {
	dir := writeUnresolvedDir(t)
	svc, _ := newTestService(t, dir)
	if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"work":{"label":"Work"}}}`)); err != nil {
		t.Fatalf("unrelated PATCH failed: %v", err)
	}
	// A new source referencing a variable that is not set yet is accepted:
	// the user may add it to .env and restart afterwards.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"later":{"type":"ics","ics_url_env":"FOCACAL_TEST_UNSET_LATER"}}}`)); err != nil {
		t.Fatalf("new source with an unset env ref: %v", err)
	}
	// Neither a reference nor an inline URL is still invalid.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"bare":{"type":"ics"}}}`)); !errors.Is(err, ErrValidation) {
		t.Fatalf("ics source without reference: err = %v, want ErrValidation", err)
	}
}

func TestResolveICSURLNamesUnsetVariable(t *testing.T) {
	os.Unsetenv("FOCACAL_TEST_UNSET_FEED")
	_, err := SourceConfig{ID: "team", Type: "ics", ICSURLEnv: "FOCACAL_TEST_UNSET_FEED"}.ResolveICSURL()
	if err == nil || !strings.Contains(err.Error(), "FOCACAL_TEST_UNSET_FEED") {
		t.Fatalf("err = %v, want it to name the variable", err)
	}
	// An inline fallback still works when the variable is unset.
	u, err := SourceConfig{ICSURLEnv: "FOCACAL_TEST_UNSET_FEED", ICSURL: "https://x/y.ics"}.ResolveICSURL()
	if err != nil || u != "https://x/y.ics" {
		t.Fatalf("fallback = %q, %v", u, err)
	}
}

func TestPatchRejectsPipeInIDs(t *testing.T) {
	for name, body := range map[string]string{
		"source id": `{"sources":{"a|b":{"type":"google"}}}`,
		// Export-only destinations validate on their own, so only the "|"
		// rule stops these.
		"destination pipes":   `{"destinations":{"x|y|z":{"type":"caldav","caldav_url":"https://d.example/"}}}`,
		"destination cal id":  `{"destinations":{"x|y|z":{"type":"caldav","caldav_url":"https://d.example/","calendar_id":"y|z"}}}`,
		"destination no pipe": `{"destinations":{"work":{"sources":[{"id":"work"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			if _, err := svc.Patch(DomainCalendars, []byte(body)); !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

func TestCalDAVURLUserinfoIsASecret(t *testing.T) {
	dir := t.TempDir()
	body := `sources:
  - id: dav
    type: caldav
    caldav_url: https://me:SECRETPW@dav.example/cal/
destinations:
  - id: out
    calendar_id: x
    type: caldav
    caldav_url: https://me:SECRETPW2@dav.example/out/
`
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, _ := newTestService(t, dir)
	v, err := svc.Get(DomainCalendars)
	if err != nil {
		t.Fatal(err)
	}
	m := asMap(t, v)
	src := obj(t, m, "values", "sources", "dav")
	if src["caldav_url"] != "https://dav.example/cal/" || src["has_secret"] != true {
		t.Errorf("source view = %v", src)
	}
	dst := obj(t, m, "values", "destinations", "out|x")
	if dst["caldav_url"] != "https://dav.example/out/" {
		t.Errorf("destination view = %v", dst)
	}

	before := snapshotFiles(t, dir)
	for _, p := range []string{
		`{"sources":{"dav":{"caldav_url":"https://me:NEWPW@dav.example/"}}}`,
		`{"sources":{"dav":{"caldav_url":"https://me@dav.example/"}}}`,
		`{"destinations":{"out|x":{"caldav_url":"https://u:NEWPW@dav.example/"}}}`,
	} {
		_, err := svc.Patch(DomainCalendars, []byte(p))
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: err = %v, want ErrValidation", p, err)
		}
		if strings.Contains(err.Error(), "NEWPW") || strings.Contains(err.Error(), "me@") {
			t.Errorf("error echoes the credentials: %v", err)
		}
	}
	assertFilesUnchanged(t, dir, before)
	if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"dav":{"caldav_url":"https://dav.example/new/"}}}`)); err != nil {
		t.Errorf("plain caldav_url: %v", err)
	}
}
