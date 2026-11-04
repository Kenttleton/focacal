package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const testAppYAML = `# app settings
server:
  caldav_port: 5232
  rest_port: 5233
  mcp_port: 5234
  oauth_port: 8080
auth:
  enabled: true
`

const testPlannerYAML = `# planner settings
working_hours:
  start: "09:00"
  end: "17:00" # leave on time
  timezone: "America/New_York"
colors:
  focus: "#00aa00"
`

const testCalendarsYAML = `# calendars
sources:
  - id: work
    type: google
    credentials_file: /secrets/work.json
  - id: feed
    type: ics
    ics_url: https://secret.example/feed.ics
  - id: dav
    type: caldav
    caldav_url: https://dav.example
    username: me
    password: hunter2
  - id: plain
    type: caldav
    caldav_url: https://plain.example
destinations:
  - id: work
    calendar_id: primary
    sources:
      - id: work
      - id: feed
`

func writeTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"config.yaml":    testAppYAML,
		"planner.yaml":   testPlannerYAML,
		"calendars.yaml": testCalendarsYAML,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newTestService(t *testing.T, dir string) (*Service, *Config) {
	t.Helper()
	started, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	svc, err := NewService(dir, started)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, started
}

func snapshotFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func assertFilesUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := snapshotFiles(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("files changed\nbefore: %v\nafter:  %v", before, after)
	}
}

// asMap re-decodes v through JSON, as an API client would see it.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func obj(t *testing.T, v any, path ...string) map[string]any {
	t.Helper()
	cur := v
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("at %q: not an object: %#v", p, cur)
		}
		cur = m[p]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("%v: not an object: %#v", path, cur)
	}
	return m
}

func TestGetCalendarsOmitsSecrets(t *testing.T) {
	svc, _ := newTestService(t, writeTestDir(t))
	view, err := svc.Get(DomainCalendars)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	for _, s := range []string{"hunter2", "secret.example", `"password"`, `"ics_url"`} {
		if strings.Contains(string(raw), s) {
			t.Errorf("view leaks %s: %s", s, raw)
		}
	}
	m := asMap(t, view)
	if m["pending_restart"] != false {
		t.Errorf("pending_restart = %v, want false", m["pending_restart"])
	}
	sources := obj(t, m, "values", "sources")
	wantSecret := map[string]bool{"work": true, "feed": true, "dav": true, "plain": false}
	for id, want := range wantSecret {
		src := obj(t, sources, id)
		if src["id"] != id {
			t.Errorf("source %s: id field = %v", id, src["id"])
		}
		if src["has_secret"] != want {
			t.Errorf("source %s: has_secret = %v, want %v", id, src["has_secret"], want)
		}
	}
	if u := obj(t, sources, "dav")["username"]; u != "me" {
		t.Errorf("non-secret username dropped: %v", u)
	}
	dest := obj(t, m, "values", "destinations", "work|primary")
	if dest["id"] != "work" || dest["calendar_id"] != "primary" {
		t.Errorf("destination identity fields missing: %v", dest)
	}
	if !reflect.DeepEqual(view.Order["sources"], []string{"work", "feed", "dav", "plain"}) {
		t.Errorf("sources order = %v", view.Order["sources"])
	}
	if !reflect.DeepEqual(view.Order["destinations"], []string{"work|primary"}) {
		t.Errorf("destinations order = %v", view.Order["destinations"])
	}
}

func TestGetAppAndPlannerViews(t *testing.T) {
	svc, _ := newTestService(t, writeTestDir(t))
	app, err := svc.Get(DomainApp)
	if err != nil {
		t.Fatal(err)
	}
	m := asMap(t, app)
	if p := obj(t, m, "values", "server")["rest_port"]; p != float64(5233) {
		t.Errorf("server.rest_port = %v", p)
	}
	if auth := obj(t, m, "values", "auth"); len(auth) != 1 || auth["enabled"] != true {
		t.Errorf("auth view = %v, want only enabled", auth)
	}
	if _, ok := obj(t, m, "values")["agent"]; ok {
		t.Error("app view must not include agent (planner key)")
	}
	if _, ok := obj(t, m, "values")["working_hours"]; ok {
		t.Error("app view must not include planner keys")
	}

	pl, err := svc.Get(DomainPlanner)
	if err != nil {
		t.Fatal(err)
	}
	m = asMap(t, pl)
	if _, ok := obj(t, m, "values", "agent")["api_key"]; ok {
		t.Error("agent.api_key must not be exposed")
	}
	if tz := obj(t, m, "values", "working_hours")["timezone"]; tz != "America/New_York" {
		t.Errorf("timezone = %v", tz)
	}
	if c := obj(t, m, "values", "colors")["focus"]; c != "#00aa00" {
		t.Errorf("colors.focus = %v", c)
	}
	if d := obj(t, m, "values", "lunch")["duration_minutes"]; d != float64(60) {
		t.Errorf("lunch default not applied: %v", d)
	}
	if _, ok := obj(t, m, "values")["sources"]; ok {
		t.Error("planner view must not include calendar keys")
	}
	if pl.Order == nil {
		t.Error("order should be an empty object, not null")
	}
}

func TestPatchPlannerPersistsAndFlagsRestart(t *testing.T) {
	dir := writeTestDir(t)
	svc, started := newTestService(t, dir)

	view, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`))
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !view.PendingRestart {
		t.Error("PendingRestart should be true after a change")
	}
	if e := obj(t, asMap(t, view), "values", "working_hours")["end"]; e != "18:00" {
		t.Errorf("patched view end = %v", e)
	}

	got, err := svc.Get(DomainPlanner)
	if err != nil {
		t.Fatal(err)
	}
	if e := obj(t, asMap(t, got), "values", "working_hours")["end"]; e != "18:00" || !got.PendingRestart {
		t.Errorf("Get after patch: end=%v pending=%v", e, got.PendingRestart)
	}
	if started.WorkingHours.End != "17:00" {
		t.Errorf("started config mutated: end = %q", started.WorkingHours.End)
	}
	if cal, _ := svc.Get(DomainCalendars); cal.PendingRestart {
		t.Error("calendars untouched, should not be pending")
	}

	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if !strings.Contains(string(b), "# planner settings") || !strings.Contains(string(b), `end: "18:00" # leave on time`) {
		t.Errorf("comments not preserved:\n%s", b)
	}

	// Reverting to the started value clears the flag.
	view, err = svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"17:00"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if view.PendingRestart {
		t.Error("PendingRestart should clear when saved equals started")
	}
}

func TestPatchInvalidLeavesFilesUntouched(t *testing.T) {
	cases := map[string]struct {
		domain Domain
		body   string
	}{
		"bad time":            {DomainPlanner, `{"working_hours":{"start":"25:00"}}`},
		"start after end":     {DomainPlanner, `{"working_hours":{"start":"19:00"}}`},
		"null required":       {DomainPlanner, `{"working_hours":{"timezone":null}}`},
		"null section":        {DomainPlanner, `{"lunch":null}`},
		"bad port":            {DomainApp, `{"server":{"rest_port":0}}`},
		"duplicate port":      {DomainApp, `{"server":{"rest_port":5232}}`},
		"unknown source type": {DomainCalendars, `{"sources":{"x":{"type":"nope"}}}`},
		"id mismatch":         {DomainCalendars, `{"sources":{"work":{"id":"other"}}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body))
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}

	t.Run("missing file not created", func(t *testing.T) {
		dir := t.TempDir()
		svc, err := NewService(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Patch(DomainPlanner, []byte(`{"focus":{"style":"bogus"}}`)); !errors.Is(err, ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "planner.yaml")); !os.IsNotExist(err) {
			t.Fatalf("planner.yaml should not exist, stat err = %v", err)
		}
	})
}

func TestPatchRejectsUnknownFieldAndNonObjectBody(t *testing.T) {
	cases := map[string]struct {
		domain Domain
		body   string
	}{
		"unknown top-level":     {DomainPlanner, `{"bogus":1}`},
		"other domain key":      {DomainPlanner, `{"sources":{}}`},
		"unknown nested":        {DomainPlanner, `{"working_hours":{"bogus":"x"}}`},
		"array body":            {DomainPlanner, `[]`},
		"string body":           {DomainPlanner, `"x"`},
		"null body":             {DomainPlanner, `null`},
		"empty body":            {DomainPlanner, ``},
		"malformed":             {DomainPlanner, `{`},
		"trailing data":         {DomainPlanner, `{} {}`},
		"wrong type":            {DomainPlanner, `{"working_hours":{"start":5}}`},
		"fractional int":        {DomainPlanner, `{"lunch":{"duration_minutes":30.5}}`},
		"section not object":    {DomainPlanner, `{"working_hours":"9-5"}`},
		"color not string":      {DomainPlanner, `{"colors":{"focus":1}}`},
		"unknown item field":    {DomainCalendars, `{"sources":{"work":{"bogus":1}}}`},
		"collection as list":    {DomainCalendars, `{"sources":[]}`},
		"item not object":       {DomainCalendars, `{"sources":{"work":5}}`},
		"nested list item":      {DomainCalendars, `{"destinations":{"work|primary":{"sources":[{"bogus":1}]}}}`},
		"calendar_ids not list": {DomainCalendars, `{"sources":{"work":{"calendar_ids":"primary"}}}`},
		"app auth username":     {DomainApp, `{"auth":{"username":"x"}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body))
			if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("err = %v, want ErrBadRequest", err)
			}
			if errors.Is(err, ErrValidation) {
				t.Fatalf("err %v should not also be ErrValidation", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

func TestPatchDeleteReferencedSourceFails(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	_, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"work":null}}`))
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "work") {
		t.Errorf("error should name the source: %v", err)
	}
	assertFilesUnchanged(t, dir, before)

	// Deleting an unreferenced source works.
	view, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"plain":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Order["sources"], []string{"work", "feed", "dav"}) {
		t.Errorf("order after delete = %v", view.Order["sources"])
	}
}

func TestPatchRejectsInlineSecrets(t *testing.T) {
	cases := map[string]struct {
		domain Domain
		body   string
	}{
		"source password":      {DomainCalendars, `{"sources":{"dav":{"password":"x"}}}`},
		"new ics source":       {DomainCalendars, `{"sources":{"new":{"type":"ics","ics_url":"https://x/y.ics"}}}`},
		"destination password": {DomainCalendars, `{"destinations":{"work|primary":{"password":"x"}}}`},
		"destination ics_url":  {DomainCalendars, `{"destinations":{"out|x":{"type":"ics","ics_url":"https://x"}}}`},
		"agent api key":        {DomainPlanner, `{"agent":{"api_key":"sk-123"}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body))
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}

	t.Run("env reference accepted", func(t *testing.T) {
		t.Setenv("FOCACAL_TEST_FEED", "https://secret.example/other.ics")
		dir := writeTestDir(t)
		svc, _ := newTestService(t, dir)
		view, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"new":{"type":"ics","ics_url_env":"FOCACAL_TEST_FEED"}}}`))
		if err != nil {
			t.Fatal(err)
		}
		src := obj(t, asMap(t, view), "values", "sources", "new")
		if src["has_secret"] != true || src["ics_url_env"] != "FOCACAL_TEST_FEED" {
			t.Errorf("new source view = %v", src)
		}
		if got := view.Order["sources"]; got[len(got)-1] != "new" {
			t.Errorf("new source not appended: %v", got)
		}
	})

	t.Run("null clears an inline secret", func(t *testing.T) {
		dir := writeTestDir(t)
		svc, _ := newTestService(t, dir)
		if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"dav":{"password":null}}}`)); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
		if strings.Contains(string(b), "hunter2") {
			t.Errorf("password not removed:\n%s", b)
		}
	})
}

func TestPatchCalendarsAddsDestinationFromKey(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	view, err := svc.Patch(DomainCalendars, []byte(`{"destinations":{"out|team":{"type":"caldav","caldav_url":"https://out.example","sources":[{"id":"focacal"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Order["destinations"], []string{"work|primary", "out|team"}) {
		t.Errorf("destinations order = %v", view.Order["destinations"])
	}
	d := obj(t, asMap(t, view), "values", "destinations", "out|team")
	if d["id"] != "out" || d["calendar_id"] != "team" {
		t.Errorf("identity not derived from key: %v", d)
	}
	if !view.PendingRestart {
		t.Error("expected pending restart")
	}
}

func TestNewServiceToleratesMissingFiles(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewService(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Domain{DomainApp, DomainPlanner, DomainCalendars} {
		if _, err := svc.Get(d); err != nil {
			t.Errorf("Get(%s): %v", d, err)
		}
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"timezone":"Europe/Berlin"}}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if err != nil || !strings.Contains(string(b), "Europe/Berlin") {
		t.Fatalf("planner.yaml = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "calendars.yaml")); !os.IsNotExist(err) {
		t.Error("calendars.yaml should not be created by a planner patch")
	}

	if _, err := NewService(filepath.Join(dir, "nope"), nil); err == nil {
		t.Error("NewService on a missing directory should fail")
	}
}

func TestGetUnknownDomain(t *testing.T) {
	svc, _ := newTestService(t, writeTestDir(t))
	if _, err := svc.Get(Domain("bogus")); !errors.Is(err, ErrUnknownDomain) {
		t.Errorf("Get: %v", err)
	}
	if _, err := svc.Patch(Domain("bogus"), []byte(`{}`)); !errors.Is(err, ErrUnknownDomain) {
		t.Errorf("Patch: %v", err)
	}
}

func TestPatchConcurrentNoLostUpdates(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	const n = 12
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"colors":{"kind%d":"#%06d"}}`, i, i)
			if _, err := svc.Patch(DomainPlanner, []byte(body)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	view, err := svc.Get(DomainPlanner)
	if err != nil {
		t.Fatal(err)
	}
	colors := obj(t, asMap(t, view), "values", "colors")
	if len(colors) != n+1 {
		t.Errorf("got %d colors, want %d: %v", len(colors), n+1, colors)
	}
}

func TestConfigDirIsSelfContained(t *testing.T) {
	src := writeTestDir(t)
	orig, err := LoadDir(src)
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := LoadDir(dst)
	if err != nil {
		t.Fatalf("LoadDir(copy): %v", err)
	}
	svc, err := NewService(dst, got)
	if err != nil {
		t.Fatalf("NewService(copy): %v", err)
	}
	for _, d := range []Domain{DomainApp, DomainPlanner, DomainCalendars} {
		v, err := svc.Get(d)
		if err != nil {
			t.Fatalf("Get(%s): %v", d, err)
		}
		if v.PendingRestart {
			t.Errorf("Get(%s): unexpected pending restart", d)
		}
	}

	if !reflect.DeepEqual(got.Sources, orig.Sources) {
		t.Errorf("sources differ:\n%+v\n%+v", got.Sources, orig.Sources)
	}
	if !reflect.DeepEqual(got.Destinations, orig.Destinations) {
		t.Errorf("destinations differ")
	}
	if got.WorkingHours != orig.WorkingHours || got.WorkingHours.End != "17:00" {
		t.Errorf("working hours differ: %+v vs %+v", got.WorkingHours, orig.WorkingHours)
	}
	if !reflect.DeepEqual(got.Colors, orig.Colors) || got.Colors["focus"] != "#00aa00" {
		t.Errorf("colors differ: %v vs %v", got.Colors, orig.Colors)
	}
	if len(got.Sources) != 4 || len(got.Destinations) != 1 {
		t.Errorf("unexpected counts: %d sources, %d destinations", len(got.Sources), len(got.Destinations))
	}

	after, _ := os.ReadDir(dst)
	for _, e := range after {
		if strings.HasSuffix(e.Name(), ".db") || strings.HasSuffix(e.Name(), ".sqlite") {
			t.Errorf("unexpected DB file %s", e.Name())
		}
	}
}

func TestLoadReadsOnlyEachDomainsOwnKeys(t *testing.T) {
	t.Run("stray sources in config.yaml", func(t *testing.T) {
		dir := t.TempDir()
		stray := testAppYAML + "sources:\n  - id: a\n    type: google\n  - id: b\n    type: google\n"
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(stray), 0644); err != nil {
			t.Fatal(err)
		}
		svc, _ := newTestService(t, dir)
		before, err := svc.Get(DomainCalendars)
		if err != nil {
			t.Fatal(err)
		}
		if len(before.Order["sources"]) != 0 {
			t.Fatalf("stray config.yaml sources leaked into calendars: %v", before.Order["sources"])
		}
		if _, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"c":{"type":"google"}}}`)); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Sources) != 1 || cfg.Sources[0].ID != "c" {
			t.Errorf("loaded sources = %+v, want only c", cfg.Sources)
		}
		after, _ := svc.Get(DomainCalendars)
		if !reflect.DeepEqual(after.Order["sources"], []string{"c"}) {
			t.Errorf("Get after patch = %v, want [c] (what LoadDir sees)", after.Order["sources"])
		}
	})
	t.Run("leftover working_hours in calendars.yaml", func(t *testing.T) {
		dir := writeTestDir(t)
		extra := testCalendarsYAML + "working_hours:\n  start: \"06:00\"\n  end: \"07:00\"\n  timezone: UTC\n"
		if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(extra), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.WorkingHours.Start != "09:00" || cfg.WorkingHours.Timezone != "America/New_York" {
			t.Errorf("planner.yaml overridden by calendars.yaml: %+v", cfg.WorkingHours)
		}
		svc, _ := newTestService(t, dir)
		v, _ := svc.Get(DomainPlanner)
		if s := obj(t, asMap(t, v), "values", "working_hours")["start"]; s != "09:00" {
			t.Errorf("Get planner start = %v", s)
		}
	})
}

func TestPatchRestrictsReferences(t *testing.T) {
	t.Setenv("FOCACAL_ICS_X", "https://secret.example/x.ics")
	t.Setenv("SOME_SECRET", "https://secret.example/y.ics")
	accepted := []string{
		`{"sources":{"n":{"type":"ics","ics_url_env":"FOCACAL_ICS_X"}}}`,
		`{"sources":{"work":{"credentials_file":"creds/work.json","token_file":"tokens/work.json"}}}`,
		`{"destinations":{"out|x":{"type":"ics","ics_url_env":"FOCACAL_ICS_X"}}}`,
	}
	for _, body := range accepted {
		t.Run("accept "+body, func(t *testing.T) {
			svc, _ := newTestService(t, writeTestDir(t))
			if _, err := svc.Patch(DomainCalendars, []byte(body)); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
	rejected := []string{
		`{"sources":{"n":{"type":"ics","ics_url_env":"SOME_SECRET"}}}`,
		`{"sources":{"n":{"type":"ics","ics_url_env":"focacal_ics_x"}}}`,
		`{"destinations":{"out|x":{"type":"ics","ics_url_env":"SOME_SECRET"}}}`,
		`{"sources":{"work":{"token_file":"/data/focacal.db"}}}`,
		`{"sources":{"work":{"credentials_file":"/etc/passwd"}}}`,
		`{"sources":{"work":{"token_file":"../x"}}}`,
		`{"sources":{"work":{"token_file":"a/../../x"}}}`,
		`{"destinations":{"work|primary":{"token_file":"..\\x"}}}`,
	}
	for _, body := range rejected {
		t.Run("reject "+body, func(t *testing.T) {
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

func TestPendingRestartClearsAfterAddThenDelete(t *testing.T) {
	dir := t.TempDir()
	started, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(dir, started)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.Patch(DomainCalendars, []byte(`{"sources":{"c":{"type":"google"}}}`)); !v.PendingRestart {
		t.Fatal("add should be pending")
	}
	v, err := svc.Patch(DomainCalendars, []byte(`{"sources":{"c":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.PendingRestart {
		t.Error("empty list equals the started nil list; pending should be false")
	}
	if v, _ := svc.Patch(DomainPlanner, []byte(`{"colors":{"x":"#111111"}}`)); !v.PendingRestart {
		t.Fatal("color add should be pending")
	}
	if v, _ := svc.Patch(DomainPlanner, []byte(`{"colors":{"x":null}}`)); v.PendingRestart {
		t.Error("empty colors map equals started nil map; pending should be false")
	}
}

func TestPatchColorKeysLowercased(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	v, err := svc.Patch(DomainPlanner, []byte(`{"colors":{"Meeting":"#ff0000"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c := obj(t, asMap(t, v), "values", "colors")["meeting"]; c != "#ff0000" {
		t.Fatalf("colors.meeting = %v", c)
	}
	if v, err = svc.Patch(DomainPlanner, []byte(`{"colors":{"meeting":null}}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj(t, asMap(t, v), "values", "colors")["meeting"]; ok {
		t.Error("null-delete of meeting did not remove the key written as Meeting")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if strings.Contains(strings.ToLower(string(b)), "meeting") {
		t.Errorf("planner.yaml still has a meeting color:\n%s", b)
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"colors":{"Focus":"#1","focus":"#2"}}`)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("colliding keys: err = %v, want ErrBadRequest", err)
	}
}

// TestPatchRejectsUnloadableMapKeys: viper splits map keys on "." (and an
// empty key cannot be read back), so such a colors key would pass the shape
// check and then fail to load. It must be a 400-class error with the files
// untouched, not an untyped (500) load error.
func TestPatchRejectsUnloadableMapKeys(t *testing.T) {
	for name, body := range map[string]string{
		"dot":         `{"colors":{"a.b":"#ffffff"}}`,
		"leading dot": `{"colors":{".a":"#ffffff"}}`,
		"empty":       `{"colors":{"":"#ffffff"}}`,
		"dot null":    `{"colors":{"a.b":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(DomainPlanner, []byte(body))
			if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("err = %v, want ErrBadRequest", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

// TestLegacyDisplayTimezoneIgnored: display_timezone was removed (R1). Old
// files that still hold it load, and a PATCH leaves the key in place.
func TestLegacyDisplayTimezoneIgnored(t *testing.T) {
	dir := writeTestDir(t)
	for _, f := range []string{"planner.yaml", "config.yaml"} {
		p := filepath.Join(dir, f)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append(b, []byte("display_timezone: Europe/Paris\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := newTestService(t, dir)
	v, err := svc.Get(DomainPlanner)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Values["display_timezone"]; ok {
		t.Error("view exposes display_timezone")
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"start":"08:00"}}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if !strings.Contains(string(b), "display_timezone: Europe/Paris") {
		t.Errorf("PATCH dropped the unknown display_timezone key:\n%s", b)
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"display_timezone":"UTC"}`)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("patching display_timezone: err = %v, want ErrBadRequest", err)
	}
}

// TestDecodeErrorHidesValue: a hand-edited file whose value does not decode
// must yield an error naming the field without quoting the (possibly
// secret) value, since it reaches startup output and the server log.
func TestDecodeErrorHidesValue(t *testing.T) {
	dir := t.TempDir()
	bad := "sources:\n  - id: feed\n    type: ics\n    ics_url: [https://x/LEAKME.ics]\n"
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("LoadDir succeeded on an undecodable file")
	}
	if strings.Contains(err.Error(), "LEAKME") {
		t.Errorf("error quotes the value: %v", err)
	}
	if !strings.Contains(err.Error(), "ics_url") {
		t.Errorf("error should name the field: %v", err)
	}
}
