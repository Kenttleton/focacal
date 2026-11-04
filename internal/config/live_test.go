package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func newTestLive(t *testing.T, dir string) *Live {
	t.Helper()
	l, err := NewLive(dir, nil)
	if err != nil {
		t.Fatalf("NewLive: %v", err)
	}
	return l
}

func TestNewLiveFailsFastOnBadConfig(t *testing.T) {
	dir := writeTestDir(t)
	writeFile(t, dir, "planner.yaml", "working_hours: [")
	if _, err := NewLive(dir, nil); err == nil {
		t.Fatal("NewLive should fail on a broken file")
	}
}

func TestLiveReloadSwapsOnChange(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	if got := l.Current().WorkingHours.End; got != "17:00" {
		t.Fatalf("end = %q", got)
	}
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	changed, err := l.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if got := l.Current().WorkingHours.End; got != "18:00" {
		t.Errorf("end after reload = %q", got)
	}
	if l.LoadError() != nil {
		t.Errorf("LoadError = %v", l.LoadError())
	}
}

func TestLiveReloadKeepsLastGoodOnBadYAML(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	before := l.Current()
	writeFile(t, dir, "planner.yaml", "working_hours: [")
	changed, err := l.Reload()
	if err == nil || changed {
		t.Fatalf("Reload = %v, %v; want error", changed, err)
	}
	if l.Current() != before || l.LoadError() == nil {
		t.Errorf("snapshot must be kept and error recorded")
	}
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	changed, err = l.Reload()
	if err != nil || !changed || l.LoadError() != nil {
		t.Fatalf("recovery Reload = %v, %v, loaderr %v", changed, err, l.LoadError())
	}
	if l.Current().WorkingHours.End != "18:00" {
		t.Errorf("not swapped after fix")
	}
}

func TestLiveReloadKeepsLastGoodOnValidationFailure(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	before := l.Current()
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"nonsense"`, 1))
	if _, err := l.Reload(); err == nil {
		t.Fatal("expected validation error")
	}
	if l.Current() != before || l.LoadError() == nil {
		t.Errorf("snapshot must be kept and error recorded")
	}
}

func TestLiveOnChangeOnlyWhenDifferent(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	var order []string
	var olds, news []*Config
	l.OnChange(func(old, nw *Config) {
		order = append(order, "a")
		olds, news = append(olds, old), append(news, nw)
		_ = l.Current() // must not deadlock
	})
	l.OnChange(func(_, _ *Config) { order = append(order, "b") })

	writeFile(t, dir, "planner.yaml", testPlannerYAML) // identical bytes
	if changed, err := l.Reload(); err != nil || changed || len(order) != 0 {
		t.Fatalf("identical reload: %v %v %v", changed, err, order)
	}
	prev := l.Current()
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	if changed, err := l.Reload(); err != nil || !changed {
		t.Fatalf("reload: %v %v", changed, err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b"}) {
		t.Errorf("order = %v", order)
	}
	if olds[0] != prev || news[0] != l.Current() {
		t.Errorf("subscriber got wrong old/new")
	}
}

func TestLiveRestartRequiredOnlyForServer(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	if l.RestartRequired() {
		t.Fatal("restart required at start")
	}
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	if _, err := l.Reload(); err != nil || l.RestartRequired() {
		t.Fatalf("planner change: err %v restart %v", err, l.RestartRequired())
	}
	writeFile(t, dir, "config.yaml", strings.Replace(testAppYAML, "5233", "5300", 1))
	if _, err := l.Reload(); err != nil || !l.RestartRequired() {
		t.Fatalf("server change: err %v restart %v", err, l.RestartRequired())
	}
	writeFile(t, dir, "config.yaml", testAppYAML)
	if _, err := l.Reload(); err != nil || l.RestartRequired() {
		t.Fatalf("revert: err %v restart %v", err, l.RestartRequired())
	}
}

func TestLiveEnvOverrideSurvivesReload(t *testing.T) {
	dir := writeTestDir(t)
	t.Setenv("FOCACAL_SERVER_REST_PORT", "7000")
	l := newTestLive(t, dir)
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	if _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := l.Current().Server.RESTPort; got != 7000 {
		t.Errorf("rest_port = %d, env override lost", got)
	}
	if l.RestartRequired() {
		t.Error("env override must not read as restart required")
	}
}

func TestLiveResolvesFilePathsUnderDir(t *testing.T) {
	dir := writeTestDir(t)
	writeFile(t, dir, "calendars.yaml", "sources:\n  - id: g\n    type: google\n    credentials_file: g.json\n")
	l := newTestLive(t, dir)
	if got := l.Current().Sources[0].CredentialsFile; got != filepath.Join(dir, "g.json") {
		t.Errorf("credentials_file = %q", got)
	}
}

func TestLiveConcurrentReaders(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if l.Current() == nil || l.Current().WorkingHours.End == "" {
					t.Error("bad snapshot")
					return
				}
				_ = l.LoadError()
				_ = l.RestartRequired()
			}
		}()
	}
	for i := range 50 {
		end := "18:00"
		if i%2 == 0 {
			end = "17:00"
		}
		writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"`+end+`"`, 1))
		if _, err := l.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
}

func TestServiceGetShowsLoadError(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	svc, err := NewService(dir, l)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.Get(DomainPlanner); v.LoadError != "" {
		t.Fatalf("load_error = %q on a good dir", v.LoadError)
	}
	// Decodes fine but fails validation.
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"nonsense"`, 1))
	if _, err := l.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	v, err := svc.Get(DomainPlanner)
	if err != nil || v.LoadError == "" {
		t.Fatalf("Get = %v, load_error %q", err, v.LoadError)
	}
	// Unparseable: Get falls back to the last good values and still reports it.
	writeFile(t, dir, "planner.yaml", "working_hours: [")
	if _, err := l.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	v, err = svc.Get(DomainPlanner)
	if err != nil || v.LoadError == "" {
		t.Fatalf("Get on bad yaml = %v, load_error %q", err, v.LoadError)
	}
	if e := obj(t, asMap(t, v), "values", "working_hours")["end"]; e != "17:00" {
		t.Errorf("fallback end = %v", e)
	}
}

func TestServicePatchReloadsSynchronously(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	svc, _ := NewService(dir, l)
	n := 0
	l.OnChange(func(_, _ *Config) { n++ })
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`)); err != nil {
		t.Fatal(err)
	}
	if n != 1 || l.Current().WorkingHours.End != "18:00" {
		t.Errorf("patch did not reload: notified %d, end %q", n, l.Current().WorkingHours.End)
	}
	if changed, err := l.Reload(); err != nil || changed {
		t.Errorf("later reload: %v %v", changed, err)
	}
}
