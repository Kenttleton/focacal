package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestSaveCredentialsMode0600(t *testing.T) {
	dir := t.TempDir()
	want := Credentials{Username: "kent", PasswordHash: "$2a$10$abc"}
	if err := SaveCredentials(dir, want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, CredentialsFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
	got, ok, err := LoadCredentials(dir)
	if err != nil || !ok {
		t.Fatalf("LoadCredentials = %v, %v", ok, err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Overwrite keeps 0600 and replaces values.
	want.PasswordHash = "$2a$10$def"
	if err := SaveCredentials(dir, want); err != nil {
		t.Fatal(err)
	}
	st, _ = os.Stat(filepath.Join(dir, CredentialsFile))
	if got := st.Mode().Perm(); got != 0600 {
		t.Fatalf("mode after overwrite = %o, want 0600", got)
	}
	got, _, _ = LoadCredentials(dir)
	if got != want {
		t.Fatalf("after overwrite got %+v, want %+v", got, want)
	}
}

func TestLoadCredentialsAbsent(t *testing.T) {
	_, ok, err := LoadCredentials(t.TempDir())
	if err != nil || ok {
		t.Fatalf("LoadCredentials on empty dir = %v, %v; want false, nil", ok, err)
	}
}

func TestSetupCredentialsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = SetupCredentials(dir, Credentials{Username: "u", PasswordHash: string(rune('a' + i))})
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrAlreadyConfigured):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d callers succeeded, want exactly 1", wins)
	}
	st, err := os.Stat(filepath.Join(dir, CredentialsFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 0600", st.Mode().Perm())
	}
	c, ok, err := LoadCredentials(dir)
	if err != nil || !ok || c.Username != "u" || c.PasswordHash == "" {
		t.Fatalf("LoadCredentials = %+v, %v, %v", c, ok, err)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir has %v, want only %s", names, CredentialsFile)
	}
}

func TestSetupCredentialsAfterSaveFails(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCredentials(dir, Credentials{Username: "a", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := SetupCredentials(dir, Credentials{Username: "b", PasswordHash: "h2"}); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("err = %v, want ErrAlreadyConfigured", err)
	}
}

// Legacy auth keys left in config.yaml (until T8 migrates them) must never
// appear through the Service projection.
func TestGetAppOmitsLegacyAuthCredentials(t *testing.T) {
	dir := t.TempDir()
	legacy := "auth:\n  enabled: true\n  username: kent\n  password_hash: $2a$10$x\n  session_token: $2a$10$y\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.Get(DomainApp)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	for _, bad := range []string{"password_hash", "session_token", "kent", "$2a$"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("app view leaks %q: %s", bad, b)
		}
	}
}

func TestSaveCredentialsTightensExistingMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, CredentialsFile)
	if err := os.WriteFile(p, []byte("username: a\npassword_hash: old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0644)
	if err := SaveCredentials(dir, Credentials{Username: "a", PasswordHash: "new"}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 0600", st.Mode().Perm())
	}
}

func TestSaveCredentialsBackupMode0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, CredentialsFile)
	if err := os.WriteFile(p, []byte("username: a\npassword_hash: old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0644)
	// A stale world-readable .bak from an earlier write.
	if err := os.WriteFile(p+".bak", []byte("password_hash: older\n"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p+".bak", 0644)
	orig := renameFile
	renameFile = func(string, string) error { return syscall.EBUSY }
	defer func() { renameFile = orig }()
	if err := SaveCredentials(dir, Credentials{Username: "a", PasswordHash: "new"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p, p + ".bak"} {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %o, want 0600", filepath.Base(f), st.Mode().Perm())
		}
	}
	c, _, _ := LoadCredentials(dir)
	if c.PasswordHash != "new" {
		t.Fatalf("hash = %q, want new", c.PasswordHash)
	}
}
