package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kenttleton/focacal/internal/auth/simple"
	"github.com/Kenttleton/focacal/internal/config"
)

// newAuthTestServer builds a Server over a temp config dir with the auth
// middleware applied. connectors are nil: auth handlers must not need the DB.
func newAuthTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Cleanup(simple.RevokeAll)
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(nil, NewHealthState(), "127.0.0.1:0", log)
	s.SetConfigDir(dir)
	s.SetRecoveryKeyPath(filepath.Join(dir, "recovery.key"))
	present := func() bool { _, ok, _ := config.LoadCredentials(dir); return ok }
	s.WrapHandler(simple.Middleware(config.AuthConfig{Enabled: true, APIKey: "static-test-key"}, present, log))
	return s, dir
}

func doJSON(t *testing.T, s *Server, path, token string, body any) (int, map[string]string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.srv.Handler.ServeHTTP(rec, req)
	out := map[string]string{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func setupAndLogin(t *testing.T, s *Server, user, pw string) string {
	t.Helper()
	code, out := doJSON(t, s, "/auth/setup", "", map[string]string{"username": user, "password": pw})
	if code != http.StatusOK || out["token"] == "" {
		t.Fatalf("setup = %d %v", code, out)
	}
	code, out = doJSON(t, s, "/auth/login", "", map[string]string{"username": user, "password": pw})
	if code != http.StatusOK || out["token"] == "" {
		t.Fatalf("login = %d %v", code, out)
	}
	return out["token"]
}

func TestSetupWritesCredentialsFileOnly(t *testing.T) {
	s, dir := newAuthTestServer(t)
	setupAndLogin(t, s, "kent", "pw1")
	c, ok, err := config.LoadCredentials(dir)
	if err != nil || !ok || c.Username != "kent" || !simple.VerifyPassword(c.PasswordHash, "pw1") {
		t.Fatalf("credentials = %+v %v %v", c, ok, err)
	}
	code, _ := doJSON(t, s, "/auth/setup", "", map[string]string{"username": "x", "password": "y"})
	if code != http.StatusForbidden {
		t.Fatalf("second setup = %d, want 403", code)
	}
	code, _ = doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "wrong"})
	if code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", code)
	}
}

func TestLoginDoesNotRewriteConfigFiles(t *testing.T) {
	s, dir := newAuthTestServer(t)
	files := []string{"config.yaml", "planner.yaml", "calendars.yaml"}
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("# "+f+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	setupAndLogin(t, s, "kent", "pw1")
	doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "pw1"})
	for _, f := range files {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if !st.ModTime().Equal(past) {
			t.Errorf("%s modified: mtime %v, want %v", f, st.ModTime(), past)
		}
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if string(b) != "# "+f+"\n" {
			t.Errorf("%s content changed: %q", f, b)
		}
	}
}

func TestResetAndRecoverRevokeExistingTokens(t *testing.T) {
	s, dir := newAuthTestServer(t)
	old := setupAndLogin(t, s, "kent", "pw1")

	code, out := doJSON(t, s, "/auth/reset", old, map[string]string{"new_password": "pw2"})
	if code != http.StatusOK || out["token"] == "" {
		t.Fatalf("reset = %d %v", code, out)
	}
	afterReset := out["token"]
	if simple.IsValidToken(old) {
		t.Fatal("pre-reset token still valid after reset")
	}
	if !simple.IsValidToken(afterReset) {
		t.Fatal("token issued by reset is not valid")
	}
	if code, _ := doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "pw2"}); code != http.StatusOK {
		t.Fatalf("login with new password = %d", code)
	}

	if err := os.WriteFile(filepath.Join(dir, "recovery.key"), []byte("rk-123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, s, "/auth/recover", "", map[string]string{"recovery_key": "bad", "username": "x", "new_password": "y"}); code != http.StatusUnauthorized {
		t.Fatalf("recover with bad key = %d, want 401", code)
	}
	code, out = doJSON(t, s, "/auth/recover", "", map[string]string{"recovery_key": "rk-123", "username": "admin", "new_password": "pw3"})
	if code != http.StatusOK || out["token"] == "" {
		t.Fatalf("recover = %d %v", code, out)
	}
	if simple.IsValidToken(afterReset) {
		t.Fatal("pre-recover token still valid after recover")
	}
	if !simple.IsValidToken(out["token"]) {
		t.Fatal("token issued by recover is not valid")
	}
	c, _, _ := config.LoadCredentials(dir)
	if c.Username != "admin" || !simple.VerifyPassword(c.PasswordHash, "pw3") {
		t.Fatalf("credentials after recover = %+v", c)
	}
}

func TestDeletingCredentialsReopensSetup(t *testing.T) {
	s, dir := newAuthTestServer(t)
	setupAndLogin(t, s, "kent", "pw1")
	if err := os.Remove(filepath.Join(dir, config.CredentialsFile)); err != nil {
		t.Fatal(err)
	}
	if code, _ := doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "pw1"}); code != http.StatusUnauthorized {
		t.Fatalf("login after delete = %d, want 401", code)
	}
	setupAndLogin(t, s, "new", "pw9")
	// Only credentials.yaml was created: no DB or other files in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != config.CredentialsFile {
			t.Errorf("unexpected file in config dir: %s", e.Name())
		}
	}
}

// Auth handlers must not return internal error text (paths, file errors) on
// 500; the details go to the server log.
func TestAuthInternalErrorsAreGeneric(t *testing.T) {
	s, dir := newAuthTestServer(t)
	// credentials.yaml as a directory: every read and write of it fails.
	if err := os.Mkdir(filepath.Join(dir, "credentials.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recovery.key"), []byte("rk"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, token string
		body        map[string]string
	}{
		{"/auth/setup", "", map[string]string{"username": "u", "password": "p"}},
		{"/auth/login", "", map[string]string{"username": "u", "password": "p"}},
		{"/auth/reset", "static-test-key", map[string]string{"new_password": "p2"}},
		{"/auth/recover", "", map[string]string{"recovery_key": "rk", "username": "u", "new_password": "p2"}},
	}
	for _, tc := range cases {
		code, out := doJSON(t, s, tc.path, tc.token, tc.body)
		if code != http.StatusInternalServerError {
			t.Errorf("%s = %d %v, want 500", tc.path, code, out)
			continue
		}
		msg := out["error"]
		if msg == "" || strings.Contains(msg, dir) || strings.Contains(msg, "credentials.yaml") || strings.Contains(msg, "directory") {
			t.Errorf("%s error leaks internals: %q", tc.path, msg)
		}
	}
}
