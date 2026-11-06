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
	writeFile(t, dir, "calendars.yaml", "accounts:\n  - id: a\n    type: google\n    credentials_file: g.json\ncalendars:\n  - id: g\n    account: a\n")
	l := newTestLive(t, dir)
	if got := l.Current().Accounts[0].CredentialsFile; got != filepath.Join(dir, "g.json") {
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

func TestLiveNotificationsInSwapOrder(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	var mu sync.Mutex
	var got [][2]*Config
	first := true
	inFirst := make(chan struct{})
	release := make(chan struct{})
	l.OnChange(func(old, cur *Config) {
		mu.Lock()
		isFirst := first
		first = false
		mu.Unlock()
		if isFirst {
			close(inFirst)
			<-release
		}
		mu.Lock()
		got = append(got, [2]*Config{old, cur})
		mu.Unlock()
	})
	start := l.Current()
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	done := make(chan struct{})
	go func() { _, _ = l.Reload(); close(done) }()
	<-inFirst // first Reload is now inside its subscriber
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"19:00"`, 1))
	second := make(chan struct{})
	go func() {
		if _, err := l.Reload(); err != nil {
			t.Error(err)
		}
		close(second)
	}()
	close(release)
	<-done
	<-second
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0][0] != start || got[1][0] != got[0][1] || got[1][1] != l.Current() {
		t.Errorf("deliveries out of order: %d", len(got))
	}
}

func TestReloadRefusesEmptyOrMissingDomainFile(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, path string){
		"zero bytes": func(t *testing.T, p string) {
			if err := os.WriteFile(p, nil, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"whitespace": func(t *testing.T, p string) {
			if err := os.WriteFile(p, []byte(" \n\t\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"deleted": func(t *testing.T, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestDir(t)
			l := newTestLive(t, dir)
			var notified atomic.Int32
			l.OnChange(func(_, _ *Config) { notified.Add(1) })
			before := l.Current()
			if len(before.Sources) == 0 {
				t.Fatal("fixture has no sources")
			}
			damage(t, filepath.Join(dir, "calendars.yaml"))
			changed, err := l.Reload()
			if err == nil || changed {
				t.Fatalf("Reload = %v, %v; want an error", changed, err)
			}
			if !strings.Contains(err.Error(), "calendars.yaml") {
				t.Errorf("error does not name the file: %v", err)
			}
			if l.Current() != before || notified.Load() != 0 {
				t.Error("an empty file replaced the last good config")
			}
			if l.LoadError() == nil {
				t.Error("LoadError not set")
			}
			// Recovery: restoring the content applies cleanly.
			writeFile(t, dir, "calendars.yaml", testCalendarsYAML)
			if _, err := l.Reload(); err != nil || l.LoadError() != nil {
				t.Fatalf("recovery Reload err = %v, LoadError = %v", err, l.LoadError())
			}
		})
	}
}

func TestReloadDeleteThenCreateConverges(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	p := filepath.Join(dir, "calendars.yaml")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reload(); err == nil {
		t.Fatal("deleted file should be refused")
	}
	writeFile(t, dir, "calendars.yaml.tmp", "accounts:\n  - id: a\n    type: google\n    credentials_file: g.json\ncalendars:\n  - id: g\n    account: a\n")
	if err := os.Rename(filepath.Join(dir, "calendars.yaml.tmp"), p); err != nil {
		t.Fatal(err)
	}
	changed, err := l.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if n := len(l.Current().Sources); n != 1 {
		t.Errorf("sources = %d, want 1", n)
	}
}

func TestReloadDeliberateEmptyMappingApplies(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	writeFile(t, dir, "calendars.yaml", "{}\n")
	changed, err := l.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if n := len(l.Current().Sources); n != 0 {
		t.Errorf("sources = %d, want 0", n)
	}
}

func TestReloadFreshDirectoryStaysLenient(t *testing.T) {
	dir := t.TempDir()
	l := newTestLive(t, dir)
	if _, err := l.Reload(); err != nil {
		t.Fatalf("empty directory should reload fine: %v", err)
	}
}

func TestReloadRecoversSubscriberPanic(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	var after atomic.Int32
	l.OnChange(func(_, _ *Config) { panic("boom") })
	l.OnChange(func(_, _ *Config) { after.Add(1) })
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"18:00"`, 1))
	if changed, err := l.Reload(); err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if after.Load() != 1 {
		t.Errorf("subscriber after the panicking one ran %d times", after.Load())
	}
	// Locks were released: another reload must not deadlock.
	writeFile(t, dir, "planner.yaml", strings.Replace(testPlannerYAML, `"17:00"`, `"19:00"`, 1))
	if _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	if after.Load() != 2 {
		t.Errorf("second delivery count = %d", after.Load())
	}
}

func TestLoadErrorDoesNotEchoValues(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	svc, _ := NewService(dir, l)
	writeFile(t, dir, "config.yaml", "server:\n  rest_port: \"sekrit-value\"\n")
	if _, err := l.Reload(); err == nil {
		t.Fatal("expected error")
	}
	v, err := svc.Get(DomainApp)
	if err != nil || v.LoadError == "" || strings.Contains(v.LoadError, "sekrit-value") {
		t.Errorf("Get = %v, load_error %q", err, v.LoadError)
	}
}

func TestPatchRefusesWhenFileNoLongerParses(t *testing.T) {
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	svc, _ := NewService(dir, l)
	writeFile(t, dir, "planner.yaml", "working_hours: [")
	before := snapshotFiles(t, dir)
	if _, err := svc.Patch(DomainPlanner, []byte(`{"working_hours":{"end":"18:00"}}`)); err == nil {
		t.Fatal("Patch should fail")
	}
	assertFilesUnchanged(t, dir, before)
}
