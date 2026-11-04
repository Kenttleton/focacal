package simple

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/Kenttleton/focacal/internal/config"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func generatedKey(t *testing.T, present bool) (string, http.Handler) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Middleware(config.AuthConfig{Enabled: true}, func() bool { return present }, log)(okHandler)
	m := regexp.MustCompile(`api_key=([0-9a-f]+)`).FindStringSubmatch(buf.String())
	if m == nil {
		return "", h
	}
	return m[1], h
}

func get(h http.Handler, key string) int {
	req := httptest.NewRequest(http.MethodGet, "/today", nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestMiddlewareGeneratesKeyWhenNoCredentials(t *testing.T) {
	key, h := generatedKey(t, false)
	if key == "" {
		t.Fatal("no session key generated when credentials are absent and api_key is empty")
	}
	if code := get(h, key); code != http.StatusOK {
		t.Fatalf("generated key rejected: %d", code)
	}
}

func TestMiddlewareNoKeyWhenCredentialsPresent(t *testing.T) {
	key, h := generatedKey(t, true)
	if key != "" {
		t.Fatalf("session key %q generated although credentials exist", key)
	}
	if code := get(h, "anything"); code != http.StatusUnauthorized {
		t.Fatalf("unknown key = %d, want 401", code)
	}
}

func TestMiddlewareSetupAlwaysReachable(t *testing.T) {
	_, h := generatedKey(t, true)
	req := httptest.NewRequest(http.MethodPost, "/auth/setup", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/auth/setup through middleware = %d, want pass-through", rec.Code)
	}
}

func TestStaleGenerationTokenRefused(t *testing.T) {
	t.Cleanup(RevokeAll)
	gen := Generation()
	RevokeAll()
	tok := GenerateKey()
	if StoreTokenAt(tok, gen) {
		t.Fatal("StoreTokenAt accepted a token from before RevokeAll")
	}
	if IsValidToken(tok) {
		t.Fatal("stale-generation token is valid")
	}
	cur := Generation()
	tok2 := GenerateKey()
	if !StoreTokenAt(tok2, cur) || !IsValidToken(tok2) {
		t.Fatal("current-generation token not stored")
	}
}
