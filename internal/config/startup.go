package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirSource says how ResolveDir chose the config directory.
type DirSource string

const (
	DirFlag     DirSource = "flag"     // --config named it (directory or legacy file)
	DirFound    DirSource = "found"    // first search-path entry holding a config file
	DirExisting DirSource = "existing" // first existing search-path directory, still empty
	DirLegacy   DirSource = "legacy"   // old single-file mount at /data/config.yaml
	DirDefault  DirSource = "default"  // nothing found: /data/config or ./config
)

// dirPaths are the locations ResolveDir consults; tests substitute them.
type dirPaths struct {
	search   []string // checked in order for a config file
	existing []string // checked in order for an existing directory
	legacy   string   // dir of the old single-file mount (legacy/config.yaml)
	dataRoot string   // present in the container image (holds the DB)
	local    string   // default outside the container
}

func defaultDirPaths() dirPaths {
	search := []string{"/data/config"}
	if home, err := os.UserHomeDir(); err == nil {
		search = append(search, filepath.Join(home, ".config", "focacal"))
	}
	existing := append([]string(nil), search...)
	return dirPaths{
		search:   append(search, "."),
		existing: existing,
		legacy:   "/data",
		dataRoot: "/data",
		local:    "config",
	}
}

// ResolveDir returns the config directory to use.
//
// With flag set (--config) it accepts a directory or a legacy file path; a
// file path (an existing file, or a missing path ending in .yaml/.yml) means
// its directory. The CLI then requires that directory to exist (CheckFlagDir).
// Without a flag the search path is /data/config, $HOME/.config/focacal,
// then "."; the first that holds config.yaml, planner.yaml, calendars.yaml or
// credentials.yaml wins. Otherwise the first existing directory among
// /data/config and $HOME/.config/focacal is used ("." is skipped: it always
// exists and would scatter files into the working directory). Failing that,
// a non-empty legacy single-file mount at /data/config.yaml returns /data
// with DirLegacy (the CLI refuses to start on it and asks for a directory
// mount); otherwise /data/config when /data exists (the container image,
// where the DB lives) or ./config.
func ResolveDir(flag string) (string, DirSource) { return defaultDirPaths().resolve(flag) }

func (p dirPaths) resolve(flag string) (string, DirSource) {
	if flag != "" {
		flag = filepath.Clean(flag)
		st, err := os.Stat(flag)
		switch {
		case err == nil && st.IsDir():
			return flag, DirFlag
		case err == nil:
			return filepath.Dir(flag), DirFlag
		}
		if ext := strings.ToLower(filepath.Ext(flag)); ext == ".yaml" || ext == ".yml" {
			return filepath.Dir(flag), DirFlag
		}
		return flag, DirFlag
	}
	for _, d := range p.search {
		if hasConfigFile(d) {
			return d, DirFound
		}
	}
	for _, d := range p.existing {
		if isDir(d) {
			return d, DirExisting
		}
	}
	if nonEmptyFile(filepath.Join(p.legacy, DomainApp.File())) {
		return p.legacy, DirLegacy
	}
	if isDir(p.dataRoot) {
		return filepath.Join(p.dataRoot, "config"), DirDefault
	}
	return p.local, DirDefault
}

func hasConfigFile(dir string) bool {
	for _, d := range Domains {
		if isFile(filepath.Join(dir, d.File())) {
			return true
		}
	}
	return isFile(filepath.Join(dir, CredentialsFile))
}

func nonEmptyFile(p string) bool {
	b, err := os.ReadFile(p)
	return err == nil && len(bytes.TrimSpace(b)) > 0
}

// HasConfigFiles reports whether dir holds any domain file or
// credentials.yaml.
func HasConfigFiles(dir string) bool { return hasConfigFile(dir) }

// CheckFlagDir returns an error when the directory a --config value resolved
// to does not exist. A typo must not silently start a fresh instance with
// /auth/setup open; the search-path defaults, by contrast, are created.
func CheckFlagDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--config: %w (create the directory, or fix the path)", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("--config: %s is not a directory", dir)
	}
	return nil
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }
func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }

// LoadStart loads dir's domain files once and returns two configs:
//
//   - started: file values over defaults, no environment overrides. Pass it
//     to NewService. It must stay env-free, because Service compares it with
//     the (env-free) files on disk for pending_restart; applying env here
//     would flag every env-overridden key as pending forever.
//   - running: started's sources plus environment overrides (FOCACAL_<KEY>
//     with dots as underscores, and OAUTH_REDIRECT_BASE_URL), with relative
//     credentials_file / token_file paths resolved under dir. The process
//     runs with this one.
//
// Both are validated.
func LoadStart(dir string) (started, running *Config, err error) {
	files, err := readDomainFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	started, err = loadFiles(files)
	if err != nil {
		return nil, nil, err
	}
	if err := started.Validate(); err != nil {
		return nil, nil, err
	}
	running, err = loadFiles(files)
	if err != nil {
		return nil, nil, err
	}
	ApplyEnv(running, os.Getenv)
	if err := running.Validate(); err != nil {
		return nil, nil, err
	}
	running.ResolveFilePaths(dir)
	return started, running, nil
}

// ResolveFilePaths joins relative credentials_file and token_file values of
// every source and destination with dir, so they resolve under the config
// directory rather than the process working directory. Absolute paths are
// kept.
func (c *Config) ResolveFilePaths(dir string) {
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	for i := range c.Sources {
		c.Sources[i].CredentialsFile = abs(c.Sources[i].CredentialsFile)
		c.Sources[i].TokenFile = abs(c.Sources[i].TokenFile)
	}
	for i := range c.Destinations {
		c.Destinations[i].CredentialsFile = abs(c.Destinations[i].CredentialsFile)
		c.Destinations[i].TokenFile = abs(c.Destinations[i].TokenFile)
	}
}
