package config

import (
	"os"
	"path/filepath"
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
	if _, _, err := LoadStart(dir); err != nil {
		t.Fatalf("seeded defaults do not load: %v", err)
	}
}
