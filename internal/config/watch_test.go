package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// startWatch runs l.Watch and returns a stop func that cancels it and waits
// for it to return.
// It returns once the watcher is armed (the initial catch-up reload ran) and
// the reload counter has been reset.
func startWatch(t *testing.T, l *Live, reloads *atomic.Int32) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Watch(ctx) }()
	var once atomic.Bool
	stop = func() {
		if !once.CompareAndSwap(false, true) {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Watch returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Watch did not return after cancel")
		}
	}
	t.Cleanup(stop)
	waitFor(t, "watcher armed", func() bool { return reloads.Load() >= 1 })
	reloads.Store(0)
	return stop
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func plannerEnd(end string) string {
	return strings.Replace(testPlannerYAML, `"17:00"`, `"`+end+`"`, 1)
}

// newWatchLive returns a Live with fast timings and a reload counter.
func newWatchLive(t *testing.T, debounce time.Duration) (*Live, string, *atomic.Int32) {
	t.Helper()
	dir := writeTestDir(t)
	l := newTestLive(t, dir)
	l.debounce = debounce
	l.pollEvery = time.Hour
	var reloads atomic.Int32
	l.reloadHook = func() { reloads.Add(1) }
	return l, dir, &reloads
}

func TestWatchReloadsOnWrite(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 20*time.Millisecond)
	startWatch(t, l, reloads)
	writeFile(t, dir, "planner.yaml", plannerEnd("18:00"))
	waitFor(t, "reload", func() bool { return l.Current().WorkingHours.End == "18:00" })
}

func TestWatchReloadsOnRenameReplace(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 20*time.Millisecond)
	startWatch(t, l, reloads)
	writeFile(t, dir, "planner.yaml.tmp", plannerEnd("19:00"))
	if err := os.Rename(filepath.Join(dir, "planner.yaml.tmp"), filepath.Join(dir, "planner.yaml")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "reload", func() bool { return l.Current().WorkingHours.End == "19:00" })
	// A second replace must still be seen: the watch is on the directory.
	writeFile(t, dir, "planner.yaml.tmp", plannerEnd("20:00"))
	if err := os.Rename(filepath.Join(dir, "planner.yaml.tmp"), filepath.Join(dir, "planner.yaml")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second reload", func() bool { return l.Current().WorkingHours.End == "20:00" })
}

func TestWatchDebouncesBurst(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 300*time.Millisecond)
	var changes atomic.Int32
	l.OnChange(func(_, _ *Config) { changes.Add(1) })
	startWatch(t, l, reloads)
	for _, end := range []string{"10:00", "11:00", "12:00", "13:00", "14:00", "15:00", "16:00", "18:00", "19:00", "20:00"} {
		writeFile(t, dir, "planner.yaml", plannerEnd(end))
	}
	waitFor(t, "reload", func() bool { return reloads.Load() >= 1 })
	if got := l.Current().WorkingHours.End; got != "20:00" {
		t.Errorf("end = %q, want the last write", got)
	}
	// Anything extra would arrive within a couple of debounce windows.
	time.Sleep(700 * time.Millisecond)
	if n := reloads.Load(); n != 1 {
		t.Errorf("reloads = %d, want 1", n)
	}
	if n := changes.Load(); n != 1 {
		t.Errorf("changes = %d, want 1", n)
	}
}

func TestWatchIgnoresUnrelatedFiles(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 30*time.Millisecond)
	startWatch(t, l, reloads)
	for _, name := range []string{"planner.yaml.bak", ".planner.yaml.swp", "planner.yaml~", "notes.txt", "other.yaml"} {
		writeFile(t, dir, name, "x")
	}
	// Negative assertion: give unrelated events several debounce windows.
	time.Sleep(300 * time.Millisecond)
	if n := reloads.Load(); n != 0 {
		t.Fatalf("unrelated files triggered %d reloads", n)
	}
	// Sentinel: a real file still triggers.
	writeFile(t, dir, "planner.yaml", plannerEnd("18:00"))
	waitFor(t, "sentinel reload", func() bool { return reloads.Load() == 1 })
}

func TestWatchSurvivesTruncatedWrite(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 20*time.Millisecond)
	startWatch(t, l, reloads)
	before := l.Current()
	writeFile(t, dir, "planner.yaml", "working_hours: [")
	waitFor(t, "failed reload", func() bool { return l.LoadError() != nil })
	if l.Current() != before {
		t.Error("a broken file replaced the last good config")
	}
	writeFile(t, dir, "planner.yaml", plannerEnd("18:00"))
	waitFor(t, "recovery", func() bool { return l.Current().WorkingHours.End == "18:00" })
	if l.LoadError() != nil {
		t.Errorf("LoadError = %v after recovery", l.LoadError())
	}
}

func TestWatchPollFallback(t *testing.T) {
	l, dir, reloads := newWatchLive(t, 20*time.Millisecond)
	l.pollEvery = 20 * time.Millisecond
	l.newWatcher = func() (*fsnotify.Watcher, error) { return nil, errors.New("no inotify") }
	startWatch(t, l, reloads)
	writeFile(t, dir, "planner.yaml", plannerEnd("18:00"))
	waitFor(t, "poll reload", func() bool { return l.Current().WorkingHours.End == "18:00" })
}

func TestWatchStopsOnContextCancel(t *testing.T) {
	l, _, _ := newWatchLive(t, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Watch(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
}
