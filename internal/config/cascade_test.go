package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

const cascadeYAML = `accounts:
  - id: acme
    type: caldav
    caldav_url: https://dav.example
  - id: other
    type: caldav
    caldav_url: https://other.example
calendars:
  - id: work
    account: acme
  - id: work2
    account: acme
  - id: home
    account: other
  - id: out
    account: other
  - id: feed
    url_env: FOCACAL_FEED
destinations:
  - id: mirror
    inputs:
      - calendar: work
      - calendar: home
      - calendar: feed
    target:
      calendar: out
  - id: into-work
    inputs:
      - calendar: feed
    target:
      calendar: work
  - id: only-work
    inputs:
      - calendar: work
    target:
      served: true
  - id: untouched
    name: Untouched
    inputs:
      - calendar: feed
      - calendar: home
    target:
      served: true
  - id: everything
    inputs: all
    target:
      served: true
`

func cascadeDir(t *testing.T) string {
	t.Helper()
	t.Setenv("FOCACAL_FEED", "https://feed.example/x.ics")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(cascadeYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func destsByID(t *testing.T, dir string) map[string]DestinationConfig {
	t.Helper()
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]DestinationConfig{}
	for _, d := range cfg.Destinations {
		out[d.ID] = d
	}
	return out
}

func TestDeleteCalendarPrunesDestinations(t *testing.T) {
	dir := cascadeDir(t)
	before := destsByID(t, dir)
	svc, _ := newTestService(t, dir)
	view, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"work":null}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after := destsByID(t, dir)
	if _, ok := after["into-work"]; ok {
		t.Error("destination targeting work should be removed")
	}
	mirror := after["mirror"]
	if len(mirror.Inputs.Items) != 2 || mirror.Inputs.Items[0].Calendar != "home" || mirror.Inputs.Items[1].Calendar != "feed" {
		t.Errorf("mirror inputs = %+v", mirror.Inputs.Items)
	}
	ow, ok := after["only-work"]
	if !ok {
		t.Fatal("a destination left with no inputs stays")
	}
	if len(ow.Inputs.Items) != 0 {
		t.Errorf("only-work inputs = %+v", ow.Inputs.Items)
	}
	for _, id := range []string{"untouched", "everything"} {
		if !reflect.DeepEqual(after[id], before[id]) {
			t.Errorf("%s changed: %+v vs %+v", id, after[id], before[id])
		}
	}
	want := []Pruned{
		{Kind: "destination_input", ID: "mirror/work"},
		{Kind: "destination_input", ID: "only-work/work"},
		{Kind: "destination", ID: "into-work"},
	}
	if !reflect.DeepEqual(view.Pruned, want) {
		t.Errorf("Pruned = %+v, want %+v", view.Pruned, want)
	}
	// The saved file keeps the untouched destination's text.
	b, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
	if !strings.Contains(string(b), "id: untouched\n    name: Untouched\n    inputs:\n      - calendar: feed\n      - calendar: home\n    target:\n      served: true\n") {
		t.Errorf("untouched destination rewritten:\n%s", b)
	}
}

func TestDeleteAccountCascades(t *testing.T) {
	dir := cascadeDir(t)
	orig, _ := os.ReadFile(filepath.Join(dir, "calendars.yaml"))
	// Force the rename to fail so the writer takes the .bak path: one
	// Update leaves exactly one .bak holding the pre-delete bytes.
	renameFile = func(string, string) error { return syscall.EBUSY }
	t.Cleanup(func() { renameFile = os.Rename })
	svc, _ := newTestService(t, dir)
	view, err := svc.Patch(DomainCalendars, []byte(`{"accounts":{"acme":null}}`), PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(filepath.Join(dir, "calendars.yaml.bak"))
	if err != nil || string(bak) != string(orig) {
		t.Fatalf("expected one .bak with the original bytes: %v", err)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "*.bak"))
	if len(baks) != 1 {
		t.Errorf("baks = %v", baks)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].ID != "other" {
		t.Errorf("accounts = %+v", cfg.Accounts)
	}
	var ids []string
	for _, c := range cfg.Calendars {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"home", "out", "feed"}) {
		t.Errorf("calendars = %v", ids)
	}
	got := map[string]bool{}
	for _, p := range view.Pruned {
		got[p.Kind+":"+p.ID] = true
	}
	for _, w := range []string{"calendar:work", "calendar:work2", "destination:into-work", "destination_input:mirror/work", "destination_input:only-work/work"} {
		if !got[w] {
			t.Errorf("Pruned missing %s: %+v", w, view.Pruned)
		}
	}
}

func TestDeleteOddIDNotBlocked(t *testing.T) {
	dir := t.TempDir()
	yml := "calendars:\n  - id: \"a:b\"\n    url_env: FOCACAL_ODD\n  - id: ok\n    url_env: FOCACAL_ODD\n"
	t.Setenv("FOCACAL_ODD", "https://x.example/a.ics")
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, _ := newTestService(t, dir)
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"a:b":null}}`), PatchOptions{}); err != nil {
		t.Fatalf("delete odd id: %v", err)
	}
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"x:y":{"url_env":"FOCACAL_ODD"}}}`), PatchOptions{}); !errors.Is(err, ErrValidation) {
		t.Errorf("creating odd id: err = %v, want ErrValidation", err)
	}
	if _, _, err := svc.Create("calendars", []byte(`{"name":"a:b","url_env":"FOCACAL_ODD"}`)); err != nil {
		t.Errorf("create slugs the name: %v", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := cascadeDir(t)
	path := filepath.Join(dir, "calendars.yaml")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	svc, _ := newTestService(t, dir)
	before := snapshotFiles(t, dir)
	view, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"work":null}}`), PatchOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pruned) != 3 {
		t.Errorf("Pruned = %+v", view.Pruned)
	}
	assertFilesUnchanged(t, dir, before)
	st, _ := os.Stat(path)
	if !st.ModTime().Equal(old) {
		t.Errorf("mtime changed: %v", st.ModTime())
	}
	// A dry run still validates.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"x":{"account":"nope"}}}`), PatchOptions{DryRun: true}); !errors.Is(err, ErrValidation) {
		t.Errorf("dry run must validate: %v", err)
	}
}

func TestCascadeLeavesHandEditedDanglingRefs(t *testing.T) {
	t.Setenv("FOCACAL_FEED", "https://feed.example/x.ics")
	dir := t.TempDir()
	yml := `calendars:
  - id: a
    url_env: FOCACAL_FEED
  - id: b
    url_env: FOCACAL_FEED
destinations:
  - id: d
    inputs:
      - calendar: a
      - calendar: ghost
    target:
      served: true
`
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, _ := newTestService(t, dir)
	// Ruling 24: a pre-existing dangling ref does not block an unrelated delete.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"calendars":{"b":null}}`), PatchOptions{DryRun: true}); err != nil {
		t.Fatalf("unrelated delete with a pre-existing dangling ref: %v", err)
	}
	// A patch that adds a new dangling ref is still rejected.
	if _, err := svc.Patch(DomainCalendars, []byte(`{"destinations":{"d":{"inputs":[{"calendar":"a"},{"calendar":"ghost"},{"calendar":"ghost2"}]}}}`), PatchOptions{DryRun: true}); !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	// pruneCascade itself only touches references to what was deleted.
	doc := map[string]any{
		"calendars": []any{},
		"destinations": []any{map[string]any{"id": "d", "inputs": []any{
			map[string]any{"calendar": "a"}, map[string]any{"calendar": "ghost"}},
			"target": map[string]any{"served": true}}},
	}
	p := pruneCascade(doc, map[string][]string{"calendars": {"a"}})
	in := doc["destinations"].([]any)[0].(map[string]any)["inputs"].([]any)
	if len(in) != 1 || in[0].(map[string]any)["calendar"] != "ghost" || len(p) != 1 {
		t.Errorf("inputs = %v pruned = %v", in, p)
	}
}
