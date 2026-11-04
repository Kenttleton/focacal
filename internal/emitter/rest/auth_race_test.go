package rest

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kenttleton/focacal/internal/auth/simple"
)

// withLoginHook installs fn to run once inside login, between password
// verification and token registration.
func withLoginHook(t *testing.T, fn func()) {
	t.Helper()
	orig := loginVerifiedHook
	done := false
	loginVerifiedHook = func() {
		if !done {
			done = true
			fn()
		}
	}
	t.Cleanup(func() { loginVerifiedHook = orig })
}

func TestLoginRacingResetIssuesNoStaleToken(t *testing.T) {
	s, _ := newAuthTestServer(t)
	tok := setupAndLogin(t, s, "kent", "pw1")
	withLoginHook(t, func() {
		if code, _ := doJSON(t, s, "/auth/reset", tok, map[string]string{"new_password": "pw2"}); code != http.StatusOK {
			t.Errorf("reset inside login = %d", code)
		}
	})
	code, out := doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "pw1"})
	if code == http.StatusOK || (out["token"] != "" && simple.IsValidToken(out["token"])) {
		t.Fatalf("login with old password across a reset = %d, token valid=%v; want 401 and no valid token",
			code, simple.IsValidToken(out["token"]))
	}
}

func TestLoginRacingRecoverIssuesNoStaleToken(t *testing.T) {
	s, dir := newAuthTestServer(t)
	setupAndLogin(t, s, "kent", "pw1")
	if err := os.WriteFile(filepath.Join(dir, "recovery.key"), []byte("rk"), 0600); err != nil {
		t.Fatal(err)
	}
	withLoginHook(t, func() {
		if code, _ := doJSON(t, s, "/auth/recover", "", map[string]string{"recovery_key": "rk", "username": "admin", "new_password": "pw2"}); code != http.StatusOK {
			t.Errorf("recover inside login = %d", code)
		}
	})
	code, out := doJSON(t, s, "/auth/login", "", map[string]string{"username": "kent", "password": "pw1"})
	if code == http.StatusOK || (out["token"] != "" && simple.IsValidToken(out["token"])) {
		t.Fatalf("login with old credentials across a recover = %d; want 401 and no valid token", code)
	}
}

func TestSetupRevokesExistingTokens(t *testing.T) {
	s, dir := newAuthTestServer(t)
	old := setupAndLogin(t, s, "kent", "pw1")
	if err := os.Remove(filepath.Join(dir, "credentials.yaml")); err != nil {
		t.Fatal(err)
	}
	setupAndLogin(t, s, "new", "pw2")
	if simple.IsValidToken(old) {
		t.Fatal("token from previous credentials still valid after a new /auth/setup")
	}
}
