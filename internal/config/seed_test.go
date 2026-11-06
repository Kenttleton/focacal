package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	configexample "github.com/Kenttleton/focacal/config.example"
)

func TestSeedDefaults(t *testing.T) {
	src := fstest.MapFS{
		"config.yaml":    {Data: []byte("a: 1\n")},
		"planner.yaml":   {Data: []byte("b: 2\n")},
		"calendars.yaml": {Data: []byte("c: 3\n")},
	}

	t.Run("empty dir gets all three", func(t *testing.T) {
		dir := t.TempDir()
		got, err := SeedDefaults(dir, src)
		if err != nil || len(got) != 3 {
			t.Fatalf("got %v, %v", got, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml")); string(b) != "b: 2\n" {
			t.Errorf("planner.yaml = %q", b)
		}
		if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); err == nil {
			t.Error("credentials.yaml must not be seeded")
		}
	})

	t.Run("existing files are never overwritten", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "planner.yaml"), []byte("mine: true\n"), 0o644)
		got, err := SeedDefaults(dir, src)
		if err != nil || len(got) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "planner.yaml")); string(b) != "mine: true\n" {
			t.Errorf("planner.yaml overwritten: %q", b)
		}
	})

	t.Run("second run creates nothing", func(t *testing.T) {
		dir := t.TempDir()
		SeedDefaults(dir, src)
		if got, err := SeedDefaults(dir, src); err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}

// The embedded samples must load as a working config: a first start on an
// empty directory runs on exactly these.
func TestEmbeddedDefaultsLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := SeedDefaults(dir, configexample.FS); err != nil {
		t.Fatal(err)
	}
	_, running, err := LoadStart(dir)
	if err != nil {
		t.Fatalf("seeded defaults do not load: %v", err)
	}
	// The sample ships one active default destination; everything else is
	// commented out.
	if len(running.Accounts) != 0 || len(running.Calendars) != 0 || len(running.Warnings) != 0 {
		t.Errorf("sample should have no accounts or calendars: %+v", running.UserConfig)
	}
	want := []DestinationConfig{{
		ID: "focacal", Name: "Focacal", Inputs: InputList{All: true},
		Target: DestinationTarget{Served: true, Auth: "private"},
	}}
	if !reflect.DeepEqual(running.Destinations, want) {
		t.Errorf("destinations = %+v, want %+v", running.Destinations, want)
	}
}

// uncommentExamples strips the leading "# " from the commented example blocks
// of calendars.yaml (the indented lines and the top-level keys), leaving the
// prose comments alone.
func uncommentExamples(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		switch {
		case strings.HasPrefix(line, "#   "),
			strings.HasPrefix(line, "# accounts:"),
			strings.HasPrefix(line, "# calendars:"),
			strings.HasPrefix(line, "# oauth:"):
			line = line[2:]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Every commented example in calendars.yaml must stay a valid, self-consistent
// config once uncommented: strict decode, strict validation, no warnings.
func TestExampleCalendarsUncommentedIsValid(t *testing.T) {
	raw, err := fs.ReadFile(configexample.FS, "calendars.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := uncommentExamples(string(raw))
	if text == string(raw) {
		t.Fatal("no example lines were uncommented")
	}
	dir := t.TempDir()
	if _, err := SeedDefaults(dir, configexample.FS); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("uncommented example does not load: %v", err)
	}
	if err := cfg.ValidateCalendars(); err != nil {
		t.Fatalf("uncommented example fails strict validation: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("warnings: %v", cfg.Warnings)
	}
	if len(cfg.Accounts) != 1 || len(cfg.Calendars) != 3 || len(cfg.Destinations) != 3 {
		t.Errorf("got %d accounts, %d calendars, %d destinations", len(cfg.Accounts), len(cfg.Calendars), len(cfg.Destinations))
	}
	var url, push, served bool
	for _, c := range cfg.Calendars {
		url = url || c.URL != "" || c.URLEnv != ""
	}
	for _, d := range cfg.Destinations {
		push = push || d.Target.Calendar != ""
		served = served || d.Target.Served
	}
	if !url || !push || !served {
		t.Errorf("example must cover url=%v push=%v served=%v", url, push, served)
	}
}
