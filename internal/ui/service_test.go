package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
)

func writeSources(t *testing.T, dir, calendars string) {
	t.Helper()
	for name, body := range map[string]string{
		"config.yaml":    "server:\n  rest_port: 5233\n",
		"planner.yaml":   "working_hours:\n  start: \"09:00\"\n  end: \"17:00\"\n  timezone: \"UTC\"\n",
		"calendars.yaml": calendars,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUISourcesFollowLiveConfig(t *testing.T) {
	dir := t.TempDir()
	writeSources(t, dir, "sources:\n  - id: one\n    type: ics\n    ics_url: https://x.example/a.ics\n")
	live, err := config.NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, live)
	got, err := svc.Sources(t.Context())
	if err != nil || len(got) != 1 || got[0]["id"] != "one" {
		t.Fatalf("Sources = %v, %v", got, err)
	}
	writeSources(t, dir, "sources:\n  - id: one\n    type: ics\n    ics_url: https://x.example/a.ics\n  - id: two\n    type: ics\n    ics_url: https://x.example/b.ics\n")
	if _, err := live.Reload(); err != nil {
		t.Fatal(err)
	}
	got, err = svc.Sources(t.Context())
	if err != nil || len(got) != 2 || got[1]["id"] != "two" {
		t.Fatalf("Sources after reload = %v, %v", got, err)
	}
}

func TestUISourcesNoLive(t *testing.T) {
	if _, err := NewService(nil, nil).Sources(t.Context()); err == nil {
		t.Fatal("want error without config")
	}
}
