package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStartAppliesEnvToRunningOnly(t *testing.T) {
	dir := writeTestDir(t)
	t.Setenv("FOCACAL_SERVER_REST_PORT", "7000")
	t.Setenv("OAUTH_REDIRECT_BASE_URL", "https://cal.example")

	started, running, err := LoadStart(dir)
	if err != nil {
		t.Fatal(err)
	}
	if started.Server.RESTPort != 5233 || started.OAuth.RedirectBaseURL != "http://localhost:8080" {
		t.Errorf("started has env applied: %+v %+v", started.Server, started.OAuth)
	}
	if running.Server.RESTPort != 7000 || running.OAuth.RedirectBaseURL != "https://cal.example" {
		t.Errorf("running lacks env overrides: %+v %+v", running.Server, running.OAuth)
	}
	t.Setenv("FOCACAL_AGENT_BASE_URL", "http://ollama:11434/v1")
	if _, r2, err := LoadStart(dir); err != nil || r2.Agent.BaseURL != "http://ollama:11434/v1" {
		t.Errorf("FOCACAL_AGENT_BASE_URL not applied: %v %+v", err, r2)
	}
	// pending_restart compares the file server block with the one at start;
	// env must not trip it.
	live, err := NewLive(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(dir, live)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Domains {
		v, err := svc.Get(d)
		if err != nil {
			t.Fatal(err)
		}
		if v.PendingRestart {
			t.Errorf("%s: pending_restart true right after start", d)
		}
	}
}

func TestLoadStartResolvesRelativeFileRefs(t *testing.T) {
	dir := t.TempDir()
	cal := `sources:
  - id: rel
    type: google
    credentials_file: secrets/google.json
    token_file: tokens/rel.json
  - id: abs
    type: google
    credentials_file: /secrets/abs.json
destinations:
  - id: rel
    calendar_id: primary
    credentials_file: dest.json
`
	if err := os.WriteFile(filepath.Join(dir, "calendars.yaml"), []byte(cal), 0644); err != nil {
		t.Fatal(err)
	}
	started, running, err := LoadStart(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := running.Sources[0].CredentialsFile, filepath.Join(dir, "secrets/google.json"); got != want {
		t.Errorf("credentials_file = %q, want %q", got, want)
	}
	if got, want := running.Sources[0].TokenFile, filepath.Join(dir, "tokens/rel.json"); got != want {
		t.Errorf("token_file = %q, want %q", got, want)
	}
	if got := running.Sources[1].CredentialsFile; got != "/secrets/abs.json" {
		t.Errorf("absolute credentials_file changed to %q", got)
	}
	if got, want := running.Destinations[0].CredentialsFile, filepath.Join(dir, "dest.json"); got != want {
		t.Errorf("destination credentials_file = %q, want %q", got, want)
	}
	// started keeps the values as written, so pending_restart is unaffected.
	if started.Sources[0].CredentialsFile != "secrets/google.json" {
		t.Errorf("started was resolved: %q", started.Sources[0].CredentialsFile)
	}
}

func mkdirs(t *testing.T, base string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(base, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDir(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	home := filepath.Join(base, "home")
	cwd := filepath.Join(base, "cwd")
	mkdirs(t, base, "cwd", "flagdir")
	paths := dirPaths{
		search:   []string{filepath.Join(data, "config"), home, cwd},
		existing: []string{filepath.Join(data, "config"), home},
		legacy:   data,
		dataRoot: data,
		local:    filepath.Join(cwd, "config"),
	}

	t.Run("flag directory", func(t *testing.T) {
		got, _ := paths.resolve(filepath.Join(base, "flagdir"))
		if got != filepath.Join(base, "flagdir") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("flag legacy file", func(t *testing.T) {
		f := filepath.Join(base, "flagdir", "config.yaml")
		touch(t, f)
		got, _ := paths.resolve(f)
		if got != filepath.Join(base, "flagdir") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("flag missing yaml file", func(t *testing.T) {
		got, _ := paths.resolve(filepath.Join(base, "nope", "config.yaml"))
		if got != filepath.Join(base, "nope") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("nothing found outside a container", func(t *testing.T) {
		got, _ := paths.resolve("")
		if got != filepath.Join(cwd, "config") {
			t.Errorf("got %q, want ./config", got)
		}
	})
	t.Run("existing empty candidate", func(t *testing.T) {
		mkdirs(t, base, "home")
		defer os.RemoveAll(home)
		got, _ := paths.resolve("")
		if got != home {
			t.Errorf("got %q, want the existing home candidate", got)
		}
	})
	t.Run("nothing found in a container", func(t *testing.T) {
		mkdirs(t, base, "data")
		defer os.RemoveAll(data)
		got, _ := paths.resolve("")
		if got != filepath.Join(data, "config") {
			t.Errorf("got %q, want /data/config", got)
		}
	})
	t.Run("cwd with config wins over empty dirs", func(t *testing.T) {
		mkdirs(t, base, "home")
		defer os.RemoveAll(home)
		touch(t, filepath.Join(cwd, "config.yaml"))
		defer os.Remove(filepath.Join(cwd, "config.yaml"))
		got, _ := paths.resolve("")
		if got != cwd {
			t.Errorf("got %q, want cwd", got)
		}
	})
	t.Run("first candidate with config wins", func(t *testing.T) {
		mkdirs(t, base, "data/config", "home")
		defer os.RemoveAll(data)
		defer os.RemoveAll(home)
		touch(t, filepath.Join(home, "planner.yaml"))
		touch(t, filepath.Join(data, "config", "config.yaml"))
		got, _ := paths.resolve("")
		if got != filepath.Join(data, "config") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("empty legacy placeholder is not a legacy mount", func(t *testing.T) {
		mkdirs(t, base, "data")
		defer os.RemoveAll(data)
		if err := os.WriteFile(filepath.Join(data, "config.yaml"), []byte("\n  \n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, how := paths.resolve("")
		if how == DirLegacy || got != filepath.Join(data, "config") {
			t.Errorf("got %q (%s), want default /data/config", got, how)
		}
	})
	t.Run("existing config dir wins over a legacy file", func(t *testing.T) {
		mkdirs(t, base, "data/config")
		defer os.RemoveAll(data)
		touch(t, filepath.Join(data, "config.yaml"))
		got, how := paths.resolve("")
		if how == DirLegacy || got != filepath.Join(data, "config") {
			t.Errorf("got %q (%s), want the existing /data/config", got, how)
		}
	})
	t.Run("legacy single-file mount", func(t *testing.T) {
		mkdirs(t, base, "data")
		defer os.RemoveAll(data)
		touch(t, filepath.Join(data, "config.yaml"))
		got, how := paths.resolve("")
		if got != data || how != DirLegacy {
			t.Errorf("got %q (%s), want legacy /data", got, how)
		}
	})
}

func TestCheckFlagDir(t *testing.T) {
	base := t.TempDir()
	if err := CheckFlagDir(filepath.Join(base, "typo")); err == nil {
		t.Error("a missing --config directory must be an error")
	}
	if err := CheckFlagDir(base); err != nil {
		t.Errorf("existing directory: %v", err)
	}
}
