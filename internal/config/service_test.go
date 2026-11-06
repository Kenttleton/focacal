package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
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
accounts:
  - id: work-google
    type: google
    credentials_file: /secrets/work.json
  - id: dav-account
    type: caldav
    caldav_url: https://dav.example
    username: me
    password: hunter2
  - id: plain-account
    type: caldav
    caldav_url: https://plain.example
calendars:
  - id: work
    account: work-google
    remote_id: primary
  - id: feed
    url: https://secret.example/feed.ics
  - id: dav
    account: dav-account
  - id: plain
    account: plain-account
destinations:
  - id: out
    inputs:
      - calendar: work
      - calendar: feed
    target:
      served: true
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
	live, err := NewLive(dir, nil)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	svc, err := NewService(dir, live)
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
	for _, s := range []string{"hunter2", "secret.example", `"password"`, `"url":`} {
		if strings.Contains(string(raw), s) {
			t.Errorf("view leaks %s: %s", s, raw)
		}
	}
	m := asMap(t, view)
	if m["pending_restart"] != false {
		t.Errorf("pending_restart = %v, want false", m["pending_restart"])
	}
	accounts := obj(t, m, "values", "accounts")
	wantSecret := map[string]bool{"work-google": true, "dav-account": true, "plain-account": false}
	for id, want := range wantSecret {
		acct := obj(t, accounts, id)
		if acct["id"] != id {
			t.Errorf("account %s: id field = %v", id, acct["id"])
		}
		if acct["has_secret"] != want {
			t.Errorf("account %s: has_secret = %v, want %v", id, acct["has_secret"], want)
		}
	}
	if u := obj(t, accounts, "dav-account")["username"]; u != "me" {
		t.Errorf("non-secret username dropped: %v", u)
	}
	cals := obj(t, m, "values", "calendars")
	if obj(t, cals, "feed")["has_secret"] != true || obj(t, cals, "work")["has_secret"] != false {
		t.Errorf("calendar has_secret wrong: %v", cals)
	}
	dest := obj(t, m, "values", "destinations", "out")
	if dest["id"] != "out" {
		t.Errorf("destination identity field missing: %v", dest)
	}
	for k, want := range map[string][]string{
		"accounts":     {"work-google", "dav-account", "plain-account"},
		"calendars":    {"work", "feed", "dav", "plain"},
		"destinations": {"out"},
	} {
		if !reflect.DeepEqual(view.Order[k], want) {
			t.Errorf("%s order = %v, want %v", k, view.Order[k], want)
		}
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
	if _, ok := obj(t, m, "values")["calendars"]; ok {
		t.Error("planner view must not include calendar keys")
	}
	if pl.Order == nil {
		t.Error("order should be an empty object, not null")
	}
}

func TestPatchPlannerPersistsAndApplies(t *testing.T) {
	dir := writeTestDir(t)
	svc, started := newTestService(t, dir)

	view, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`), PatchOptions{})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if view.PendingRestart {
		t.Error("a planner change applies live; it must not flag a restart")
	}
	if e := obj(t, asMap(t, view), "values", "working_hours")["end"]; e != "18:00" {
		t.Errorf("patched view end = %v", e)
	}
	if e := svc.live.Current().WorkingHours.End; e != "18:00" {
		t.Errorf("running config end = %q, want 18:00 (Patch reloads)", e)
	}
	got, err := svc.Get(DomainPlanner)
	if err != nil {
		t.Fatal(err)
	}
	if e := obj(t, asMap(t, got), "values", "working_hours")["end"]; e != "18:00" {
		t.Errorf("Get after patch: end=%v", e)
	}
	if started.WorkingHours.End != "17:00" {
		t.Errorf("started config mutated: end = %q", started.WorkingHours.End)
	}
}

func TestPatchServerFlagsRestartAndRevertClears(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	view, err := svc.Patch(DomainApp, []byte(`{"server":{"rest_port":5300}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !view.PendingRestart {
		t.Error("server change should flag a restart")
	}
	if cal, _ := svc.Get(DomainCalendars); !cal.PendingRestart {
		t.Error("the flag is process-wide, calendars view should show it too")
	}
	view, err = svc.Patch(DomainApp, []byte(`{"server":{"rest_port":5233}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if view.PendingRestart {
		t.Error("reverting to the started value should clear the flag")
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
		"unknown source type": {DomainCalendars, `{"accounts":{"x":{"type":"nope"}}}`},
		"colon in source id":  {DomainCalendars, `{"calendars":{"a:b":{"url_env":"FOCACAL_X"}}}`},
		"id mismatch":         {DomainCalendars, `{"calendars":{"work":{"id":"other"}}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body), PatchOptions{})
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
		if _, err := svc.Patch(DomainPlanner, []byte(`{"focus":{"style":"bogus"}}`), PatchOptions{}); !errors.Is(err, ErrValidation) {
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
		"unknown top-level":  {DomainPlanner, `{"bogus":1}`},
		"other domain key":   {DomainPlanner, `{"sources":{}}`},
		"unknown nested":     {DomainPlanner, `{"working_hours":{"bogus":"x"}}`},
		"array body":         {DomainPlanner, `[]`},
		"string body":        {DomainPlanner, `"x"`},
		"null body":          {DomainPlanner, `null`},
		"empty body":         {DomainPlanner, ``},
		"malformed":          {DomainPlanner, `{`},
		"trailing data":      {DomainPlanner, `{} {}`},
		"wrong type":         {DomainPlanner, `{"working_hours":{"start":5}}`},
		"fractional int":     {DomainPlanner, `{"lunch":{"duration_minutes":30.5}}`},
		"section not object": {DomainPlanner, `{"working_hours":"9-5"}`},
		"color not string":   {DomainPlanner, `{"colors":{"focus":1}}`},
		"unknown item field": {DomainCalendars, `{"calendars":{"work":{"bogus":1}}}`},
		"collection as list": {DomainCalendars, `{"calendars":[]}`},
		"item not object":    {DomainCalendars, `{"calendars":{"work":5}}`},
		"nested list item":   {DomainCalendars, `{"destinations":{"out":{"inputs":[{"bogus":1}]}}}`},
		"inputs not all":     {DomainCalendars, `{"destinations":{"out":{"inputs":"some"}}}`},
		"plan not bool":      {DomainCalendars, `{"calendars":{"work":{"plan":"yes"}}}`},
		"app auth username":  {DomainApp, `{"auth":{"username":"x"}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body), PatchOptions{})
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

func TestPatchDanglingRefFailsAndUnreferencedDeleteWorks(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	_, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"ghost-ref":{"account":"nope"}}}`), PatchOptions{})
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v, want ErrValidation naming the account", err)
	}
	assertFilesUnchanged(t, dir, before)

	// Deleting an unreferenced source works.
	view, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"plain":null}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Order["calendars"], []string{"work", "feed", "dav"}) {
		t.Errorf("order after delete = %v", view.Order["calendars"])
	}
}

func TestPatchRejectsInlineSecrets(t *testing.T) {
	cases := map[string]struct {
		domain Domain
		body   string
	}{
		"account password": {DomainCalendars, `{"accounts":{"dav-account":{"password":"x"}}}`},
		"new url calendar": {DomainCalendars, `{"calendars":{"new":{"url":"https://x/y.ics"}}}`},
		"agent api key":    {DomainPlanner, `{"agent":{"api_key":"sk-123"}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(tc.domain, []byte(tc.body), PatchOptions{})
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
		view, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"new":{"url_env":"FOCACAL_TEST_FEED"}}}`), PatchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		src := obj(t, asMap(t, view), "values", "calendars", "new")
		if src["has_secret"] != true || src["url_env"] != "FOCACAL_TEST_FEED" {
			t.Errorf("new source view = %v", src)
		}
		if got := view.Order["calendars"]; got[len(got)-1] != "new" {
			t.Errorf("new source not appended: %v", got)
		}
	})

	t.Run("null clears an inline secret", func(t *testing.T) {
		dir := writeTestDir(t)
		svc, _ := newTestService(t, dir)
		if _, err := svc.Patch(DomainCalendars, []byte(`{"accounts":{"dav-account":{"password":null}}}`), PatchOptions{}); err != nil {
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
	view, err := svc.Patch(DomainCalendars, []byte(`{"destinations":{"team":{"inputs":[{"calendar":"focacal","kinds":["focus"]}],"target":{"calendar":"work"}}}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Order["destinations"], []string{"out", "team"}) {
		t.Errorf("destinations order = %v", view.Order["destinations"])
	}
	d := obj(t, asMap(t, view), "values", "destinations", "team")
	if d["id"] != "team" {
		t.Errorf("identity not derived from key: %v", d)
	}
	if view.PendingRestart {
		t.Error("a calendars change applies live; no restart expected")
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
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"timezone":"Europe/Berlin"}}`), PatchOptions{}); err != nil {
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
	if _, err := svc.Patch(Domain("bogus"), []byte(`{}`), PatchOptions{}); !errors.Is(err, ErrUnknownDomain) {
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
			if _, err := svc.Patch(DomainPlanner, []byte(body), PatchOptions{}); err != nil {
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
	live, err := NewLive(dst, nil)
	if err != nil {
		t.Fatalf("NewLive(copy): %v", err)
	}
	svc, err := NewService(dst, live)
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
	if len(got.Sources) != 4 || len(got.Accounts) != 3 || len(got.Destinations) != 1 {
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
		stray := testAppYAML + "calendars:\n  - id: a\n    url_env: FOCACAL_A\n  - id: b\n    url_env: FOCACAL_B\n"
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(stray), 0644); err != nil {
			t.Fatal(err)
		}
		svc, _ := newTestService(t, dir)
		before, err := svc.Get(DomainCalendars)
		if err != nil {
			t.Fatal(err)
		}
		if len(before.Order["calendars"]) != 0 {
			t.Fatalf("stray config.yaml calendars leaked into calendars: %v", before.Order["calendars"])
		}
		if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"c":{"url_env":"FOCACAL_C"}}}`), PatchOptions{}); err != nil {
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
		if !reflect.DeepEqual(after.Order["calendars"], []string{"c"}) {
			t.Errorf("Get after patch = %v, want [c] (what LoadDir sees)", after.Order["calendars"])
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
		`{"calendars":{"n":{"url_env":"FOCACAL_ICS_X"}}}`,
		`{"accounts":{"work-google":{"credentials_file":"creds/work.json","token_file":"tokens/work.json"}}}`,
	}
	for _, body := range accepted {
		t.Run("accept "+body, func(t *testing.T) {
			svc, _ := newTestService(t, writeTestDir(t))
			if _, err := svc.Patch(DomainCalendars, []byte(body), PatchOptions{}); err != nil {
				t.Fatalf("err = %v", err)
			}
		})
	}
	rejected := []string{
		`{"calendars":{"n":{"url_env":"SOME_SECRET"}}}`,
		`{"calendars":{"n":{"url_env":"focacal_ics_x"}}}`,
		`{"accounts":{"work-google":{"token_file":"/data/focacal.db"}}}`,
		`{"accounts":{"work-google":{"credentials_file":"/etc/passwd"}}}`,
		`{"accounts":{"work-google":{"token_file":"../x"}}}`,
		`{"accounts":{"work-google":{"token_file":"a/../../x"}}}`,
		`{"accounts":{"work-google":{"token_file":"..\\x"}}}`,
	}
	for _, body := range rejected {
		t.Run("reject "+body, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			if _, err := svc.Patch(DomainCalendars, []byte(body), PatchOptions{}); !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
			assertFilesUnchanged(t, dir, before)
		})
	}
}

func TestPatchAddThenDeleteDoesNotFlagRestart(t *testing.T) {
	dir := t.TempDir()
	live, err := NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(dir, live)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"c":{"url_env":"FOCACAL_C"}}}`), PatchOptions{}); err != nil || v.PendingRestart {
		t.Fatalf("add: err %v pending %v", err, v.PendingRestart)
	}
	if len(live.Current().Sources) != 1 {
		t.Errorf("running sources = %d, want 1", len(live.Current().Sources))
	}
	v, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"c":null}}`), PatchOptions{})
	if err != nil || v.PendingRestart {
		t.Fatalf("delete: err %v pending %v", err, v.PendingRestart)
	}
	if len(live.Current().Sources) != 0 {
		t.Errorf("running sources = %d, want 0", len(live.Current().Sources))
	}
}

// TestPatchColorKeysKeepCase: color keys are read back exactly as written, so
// a null-delete by the same key removes them.
func TestPatchColorKeysKeepCase(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	v, err := svc.Patch(DomainPlanner, []byte(`{"colors":{"Meeting":"#ff0000","a.b":"#00ff00"}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cols := obj(t, asMap(t, v), "values", "colors")
	if cols["Meeting"] != "#ff0000" || cols["a.b"] != "#00ff00" {
		t.Fatalf("colors = %v", cols)
	}
	if v, err = svc.Patch(DomainPlanner, []byte(`{"colors":{"Meeting":null,"a.b":null}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	cols = obj(t, asMap(t, v), "values", "colors")
	if _, ok := cols["Meeting"]; ok {
		t.Error("null-delete did not remove Meeting")
	}
	if _, ok := cols["a.b"]; ok {
		t.Error("null-delete did not remove a.b")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if strings.Contains(string(b), "Meeting") {
		t.Errorf("planner.yaml still has a Meeting color:\n%s", b)
	}
}

// TestPatchRejectsEmptyMapKey: an empty color key is meaningless and cannot
// be addressed later, so it is a 400 with the files untouched.
func TestPatchRejectsEmptyMapKey(t *testing.T) {
	for name, body := range map[string]string{
		"empty":      `{"colors":{"":"#ffffff"}}`,
		"empty null": `{"colors":{"":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			svc, _ := newTestService(t, dir)
			before := snapshotFiles(t, dir)
			_, err := svc.Patch(DomainPlanner, []byte(body), PatchOptions{})
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
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"start":"08:00"}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	// Unknown keys in a live file are dropped on save (intended).
	if strings.Contains(string(b), "display_timezone") {
		t.Errorf("PATCH should drop the unknown display_timezone key:\n%s", b)
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"display_timezone":"UTC"}`), PatchOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("patching display_timezone: err = %v, want ErrBadRequest", err)
	}
}

// TestDecodeErrorHidesValue: a hand-edited file whose value does not decode
// must yield an error naming the field without quoting the (possibly
// secret) value, since it reaches startup output and the server log.
func TestDecodeErrorHidesValue(t *testing.T) {
	dir := t.TempDir()
	bad := "calendars:\n  - id: feed\n    url: [https://x/LEAKME.ics]\n"
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
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("error should name the field: %v", err)
	}
}

func TestPatchDropsComments(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if strings.Contains(string(b), "planner settings") || strings.Contains(string(b), "leave on time") {
		t.Errorf("comments should be dropped:\n%s", b)
	}
	// Values survive: reload sees the patched end, the other start and color.
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkingHours.End != "18:00" || cfg.WorkingHours.Start != "09:00" || cfg.Colors["focus"] != "#00aa00" {
		t.Errorf("values lost: %+v %v", cfg.WorkingHours, cfg.Colors)
	}
}

func TestPatchOrderStable(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	want := []string{"work", "feed", "dav", "plain"}
	for i := 0; i < 3; i++ {
		view, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"feed":{"name":"Feed `+fmt.Sprint(i)+`"}}}`), PatchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(view.Order["calendars"], want) {
			t.Fatalf("order = %v, want %v", view.Order["calendars"], want)
		}
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range cfg.Sources {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("on-disk order = %v", ids)
	}
	// A new item appends; existing positions are unchanged.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"aaa":{"url_env":"FOCACAL_X"}}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadDir(dir)
	if n := len(cfg.Sources); n != 5 || cfg.Sources[4].ID != "aaa" || cfg.Sources[0].ID != "work" {
		t.Fatalf("sources = %+v", cfg.Sources)
	}
}

func TestPatchFailureLeavesFileByteIdentical(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	for _, body := range []string{
		`{"working_hours":{"start":"25:00"}}`,
		`{"working_hours":{"start":"19:00"}}`,
	} {
		if _, err := svc.Patch(DomainPlanner, []byte(body), PatchOptions{}); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: err = %v", body, err)
		}
		assertFilesUnchanged(t, dir, before)
	}
	// A non-mapping file is rejected without being rewritten.
	p := filepath.Join(dir, "planner.yaml")
	if err := os.WriteFile(p, []byte("- a\n- b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before = snapshotFiles(t, dir)
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`), PatchOptions{}); err == nil {
		t.Fatal("want error for a non-mapping file")
	}
	assertFilesUnchanged(t, dir, before)
}

func TestMarshalDomainDeterministicAndScoped(t *testing.T) {
	dir := writeTestDir(t)
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := marshalDomain(DomainCalendars, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := marshalDomain(DomainCalendars, cfg)
	if string(a) != string(b) {
		t.Error("marshalDomain not deterministic")
	}
	if !strings.HasPrefix(string(a), "accounts:\n") || strings.Contains(string(a), "working_hours") {
		t.Errorf("unexpected output:\n%s", a)
	}
	pl, _ := marshalDomain(DomainPlanner, cfg)
	if strings.Contains(string(pl), "api_key") || strings.Index(string(pl), "working_hours:") != 0 {
		t.Errorf("planner output:\n%s", pl)
	}
	// Round trip: decoding the output reproduces the domain.
	again := Defaults()
	if err := decodeDomain(DomainCalendars, a, again); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Accounts, cfg.Accounts) || !reflect.DeepEqual(again.Calendars, cfg.Calendars) || !reflect.DeepEqual(again.Destinations, cfg.Destinations) {
		t.Error("round trip changed calendars")
	}
}

func TestPatchKeepsInlineAPIKeys(t *testing.T) {
	dir := writeTestDir(t)
	app := testAppYAML + "  api_key: abc\n"
	pl := testPlannerYAML + "agent:\n  base_url: http://x/v1\n  api_key: sk-xyz\n"
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(app), 0644)
	os.WriteFile(filepath.Join(dir, "planner.yaml"), []byte(pl), 0644)
	svc, _ := newTestService(t, dir)

	if _, err := svc.Patch(DomainApp, []byte(`{"server":{"rest_port":5240}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	p, _ := os.ReadFile(filepath.Join(dir, "planner.yaml"))
	if !strings.Contains(string(a), "api_key: abc") || !strings.Contains(string(p), "api_key: sk-xyz") {
		t.Errorf("inline api_key lost:\n%s\n%s", a, p)
	}

	for _, c := range []struct {
		d    Domain
		body string
	}{
		{DomainApp, `{"auth":{"api_key":"n"}}`},
		{DomainPlanner, `{"agent":{"api_key":"n"}}`},
	} {
		if _, err := svc.Patch(c.d, []byte(c.body), PatchOptions{}); err == nil {
			t.Errorf("%s: api_key patch accepted", c.body)
		}
	}
	for _, d := range []Domain{DomainApp, DomainPlanner} {
		v, err := svc.Get(d)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(v); strings.Contains(string(b), "api_key") || strings.Contains(string(b), "abc") || strings.Contains(string(b), "sk-xyz") {
			t.Errorf("%s view shows api_key: %s", d, b)
		}
	}
}

func TestMarshalDomainRoundTripAppPlanner(t *testing.T) {
	cfg := Defaults()
	cfg.Auth.Enabled = false
	cfg.Auth.APIKey = "k"
	cfg.Server.WebhooksEnabled = false
	cfg.Server.OAuthPort = 0
	cfg.Server.BindAddress = ""
	cfg.WorkingHours.Start = "09:00"
	cfg.Lunch.DurationMinutes = 0
	cfg.Focus.BufferAroundMeetings = 0
	cfg.Agent.APIKey = "sk"
	cfg.Agent.MaxTokens = 0
	cfg.Colors = map[string]string{"focus": "#00aa00"}
	for _, d := range []Domain{DomainApp, DomainPlanner} {
		b, err := marshalDomain(d, cfg)
		if err != nil {
			t.Fatal(err)
		}
		got := Defaults()
		if err := decodeDomain(d, b, got); err != nil {
			t.Fatalf("%s: %v\n%s", d, err, b)
		}
		for _, dk := range domainKeys[d] {
			if !reflect.DeepEqual(dk.get(got), dk.get(cfg)) {
				t.Errorf("%s.%s: %v != %v\n%s", d, dk.name, dk.get(got), dk.get(cfg), b)
			}
		}
		if d == DomainApp && got.Auth != cfg.Auth || d == DomainPlanner && got.Agent != cfg.Agent {
			t.Errorf("%s: secret fields lost", d)
		}
	}
}

func TestCreateGeneratesSlugID(t *testing.T) {
	t.Setenv("FOCACAL_NEW", "https://x.example/a.ics")
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	create := func(body string) (string, View, error) { return svc.Create("calendars", []byte(body)) }

	id, view, err := create(`{"name":"Work Calendar","url_env":"FOCACAL_NEW"}`)
	if err != nil || id != "work-calendar" {
		t.Fatalf("id = %q err = %v", id, err)
	}
	if _, ok := view.Values["calendars"].(map[string]any)["work-calendar"]; !ok {
		t.Error("view lacks the new calendar")
	}
	if id, _, err = create(`{"name":"Work Calendar","url_env":"FOCACAL_NEW"}`); err != nil || id != "work-calendar-2" {
		t.Errorf("second id = %q err = %v", id, err)
	}
	// Existing ids compare case-insensitively.
	if id, _, err = create(`{"name":"WORK","url_env":"FOCACAL_NEW"}`); err != nil || id != "work-2" {
		t.Errorf("WORK id = %q err = %v", id, err)
	}
	for _, name := range []string{"focacal", "日本語"} {
		id, _, err := create(`{"name":"` + name + `","url_env":"FOCACAL_NEW"}`)
		if err != nil || !ValidID(id) || id == "focacal" {
			t.Errorf("name %q gave id %q err %v", name, id, err)
		}
	}
	if _, _, err := create(`{"id":"mine","name":"X","url_env":"FOCACAL_NEW"}`); !errors.Is(err, ErrBadRequest) {
		t.Errorf("body with id: %v", err)
	}
	for _, body := range []string{`{"url_env":"FOCACAL_NEW"}`, `{"name":"  ","url_env":"FOCACAL_NEW"}`} {
		if _, _, err := create(body); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: %v", body, err)
		}
	}
	if _, _, err := create(`{"name":"X","bogus":1}`); !errors.Is(err, ErrBadRequest) {
		t.Errorf("unknown field: %v", err)
	}
	if _, _, err := create(`{"name":"X","url":"https://s.example/f.ics"}`); !errors.Is(err, ErrValidation) {
		t.Errorf("inline secret: %v", err)
	}
	if _, _, err := create(`{"name":"X","account":"missing"}`); !errors.Is(err, ErrValidation) {
		t.Errorf("dangling account: %v", err)
	}
	if _, _, err := svc.Create("oauth", []byte(`{"name":"x"}`)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("non-collection: %v", err)
	}
}

func TestCreateAccountAndDestination(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	id, _, err := svc.Create("accounts", []byte(`{"name":"Team DAV","type":"caldav","caldav_url":"https://t.example"}`))
	if err != nil || id != "team-dav" {
		t.Fatalf("account id = %q %v", id, err)
	}
	id, _, err = svc.Create("destinations", []byte(`{"name":"Out","inputs":"all","target":{"served":true}}`))
	if err != nil || id != "out-2" {
		t.Fatalf("destination id = %q %v (out exists)", id, err)
	}
}

func TestPatchIDImmutable(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"work":{"id":"renamed"}}}`), PatchOptions{}); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	assertFilesUnchanged(t, dir, before)
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"work":{"name":"Work Cal"}}}`), PatchOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestViewsHideSecrets(t *testing.T) {
	dir := writeTestDir(t)
	svc, _ := newTestService(t, dir)
	v, err := svc.Get(DomainCalendars)
	if err != nil {
		t.Fatal(err)
	}
	acc := v.Values["accounts"].(map[string]any)
	dav := acc["dav-account"].(map[string]any)
	if _, ok := dav["password"]; ok || dav["has_secret"] != true {
		t.Errorf("dav-account = %v", dav)
	}
	if acc["plain-account"].(map[string]any)["has_secret"] != false {
		t.Errorf("plain-account = %v", acc["plain-account"])
	}
	cals := v.Values["calendars"].(map[string]any)
	feed := cals["feed"].(map[string]any)
	if _, ok := feed["url"]; ok || feed["has_secret"] != true {
		t.Errorf("feed = %v", feed)
	}
	out := v.Values["destinations"].(map[string]any)["out"].(map[string]any)
	for _, k := range []string{"inputs", "target"} {
		if _, ok := out[k]; !ok {
			t.Errorf("destination view lacks %s: %v", k, out)
		}
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "secret.example") {
		t.Errorf("view leaks a secret: %s", b)
	}
}

// A full-visibility input has no event block, and the config view must say so:
// GET, edit nothing, PATCH the inputs back must not turn it into a hidden one.
func TestVisibilitySurvivesViewPatchRoundTrip(t *testing.T) {
	dir := writeTestDir(t)
	svc, live := newTestService(t, dir)
	_ = live
	_, err := svc.Patch(DomainCalendars, []byte(`{"destinations":{"rt":{"inputs":[
		{"calendar":"work"},
		{"calendar":"feed","event":{"title":{"visibility":false},"body":{"visibility":false}}}],
		"target":{"served":true}}}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string) {
		t.Helper()
		var d *DestinationConfig
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := range cfg.Destinations {
			if cfg.Destinations[i].ID == "rt" {
				d = &cfg.Destinations[i]
			}
		}
		if d == nil || len(d.Inputs.Items) != 2 {
			t.Fatalf("%s: destination = %+v", label, d)
		}
		if d.Inputs.Items[0].Event != nil {
			t.Errorf("%s: full input gained an event block: %+v", label, d.Inputs.Items[0].Event)
		}
		if e := d.Inputs.Items[1].Event; e == nil || e.Title.Visibility || e.Body.Visibility {
			t.Errorf("%s: hidden input lost its block: %+v", label, e)
		}
	}
	check("after create")

	view, err := svc.Get(DomainCalendars)
	if err != nil {
		t.Fatal(err)
	}
	inputs := asMap(t, view)["values"].(map[string]any)["destinations"].(map[string]any)["rt"].(map[string]any)["inputs"]
	if first := inputs.([]any)[0].(map[string]any); first["event"] != nil {
		t.Errorf("view shows an event block for a full input: %v", first)
	}
	body, err := json.Marshal(map[string]any{"destinations": map[string]any{"rt": map[string]any{"inputs": inputs}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Patch(DomainCalendars, body, PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	check("after view->patch")
}

func TestEventVisibilityYAMLRoundTrip(t *testing.T) {
	in := DestinationConfig{ID: "o", Target: DestinationTarget{Served: true}, Inputs: InputList{Items: []DestinationInput{
		{Calendar: "a"},
		{Calendar: "b", Event: &EventVisibility{}},
		{Calendar: "c", Event: &EventVisibility{Title: FieldVisibility{Visibility: true, Rename: "Busy"}}},
	}}}
	out, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var back DestinationConfig
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, back) {
		t.Errorf("round trip changed the value:\n%s\n%+v\n%+v", out, in, back)
	}
	if back.Inputs.Items[0].Event != nil || back.Inputs.Items[1].Event == nil {
		t.Errorf("nil and explicit all-false must stay distinct: %s", out)
	}
}
