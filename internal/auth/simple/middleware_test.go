package simple

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

func generatedKey(t *testing.T, present bool) (string, http.Handler) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Middleware(func() bool { return true }, func() string { return "" }, func() bool { return present }, log)(okHandler)
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

func constStr(s string) func() string { return func() string { return s } }

func TestMiddlewareHonorsEnabledToggle(t *testing.T) {
	var enabled atomic.Bool
	var log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := Middleware(enabled.Load, constStr("k"), func() bool { return true }, log)(okHandler)
	if code := get(h, ""); code != http.StatusOK {
		t.Fatalf("disabled: unauthenticated = %d, want pass-through", code)
	}
	enabled.Store(true)
	if code := get(h, ""); code != http.StatusUnauthorized {
		t.Fatalf("enabled on next request: unauthenticated = %d, want 401", code)
	}
	if code := get(h, "k"); code != http.StatusOK {
		t.Fatalf("enabled: valid key = %d", code)
	}
	enabled.Store(false)
	if code := get(h, ""); code != http.StatusOK {
		t.Fatalf("disabled again = %d", code)
	}
}

// Flipping enabled to true must never let an unauthenticated request through,
// even under concurrent traffic.
func TestEnableFlipNeverLetsUnauthenticatedThrough(t *testing.T) {
	var enabled atomic.Bool
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := Middleware(enabled.Load, constStr("k"), func() bool { return true }, log)(okHandler)
	var flipped atomic.Bool
	var wg sync.WaitGroup
	var bad atomic.Int32
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2000 {
				wasOn := flipped.Load() // set only after enabled.Store(true)
				if code := get(h, ""); wasOn && code != http.StatusUnauthorized {
					bad.Add(1)
				}
			}
		}()
	}
	enabled.Store(true)
	flipped.Store(true)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d unauthenticated requests passed after enable", bad.Load())
	}
}

func TestMiddlewareFailsClosedOnPanic(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	boom := func() bool { panic("boom") }
	yes := func() bool { return true }
	cases := map[string]func(http.Handler) http.Handler{
		"enabled":     Middleware(boom, constStr("k"), yes, log),
		"credentials": Middleware(yes, constStr(""), boom, log),
		"apikey":      Middleware(yes, func() string { panic("boom") }, yes, log),
	}
	for name, mw := range cases {
		for _, path := range []string{"/today", "/auth/setup"} {
			called := false
			h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer k")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || called {
				t.Errorf("%s panic on %s: code=%d next called=%v, want 401 and not called", name, path, rec.Code, called)
			}
		}
	}
}

func TestMiddlewareFollowsAPIKeyChange(t *testing.T) {
	var key atomic.Value
	key.Store("old")
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	h := Middleware(func() bool { return true }, func() string { return key.Load().(string) }, func() bool { return true }, log)(okHandler)
	if get(h, "old") != http.StatusOK {
		t.Fatal("old key rejected")
	}
	key.Store("new")
	if get(h, "old") != http.StatusUnauthorized || get(h, "new") != http.StatusOK {
		t.Fatal("api key change not followed")
	}
}

// The generated session key works only while no credentials and no api_key
// exist, and is logged at most once.
func TestGeneratedKeyOnlyValidWhileNoCredentials(t *testing.T) {
	var present atomic.Bool
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Middleware(func() bool { return true }, constStr(""), present.Load, log)(okHandler)
	key := regexp.MustCompile(`api_key=([0-9a-f]+)`).FindStringSubmatch(buf.String())[1]
	if get(h, key) != http.StatusOK {
		t.Fatal("generated key rejected")
	}
	present.Store(true)
	if get(h, key) != http.StatusUnauthorized {
		t.Fatal("generated key still valid after credentials appeared")
	}
	present.Store(false)
	if get(h, key) != http.StatusOK {
		t.Fatal("same generated key should work again when credentials vanish")
	}
	if n := strings.Count(buf.String(), "api_key="); n != 1 {
		t.Fatalf("key logged %d times", n)
	}
}

// Lazily generated when credentials disappear after startup.
func TestGeneratedKeyLazyWhenCredentialsDeleted(t *testing.T) {
	var present atomic.Bool
	present.Store(true)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := Middleware(func() bool { return true }, constStr(""), present.Load, log)(okHandler)
	if buf.Len() != 0 {
		t.Fatal("key generated although credentials exist")
	}
	present.Store(false)
	get(h, "") // first request triggers generation
	m := regexp.MustCompile(`api_key=([0-9a-f]+)`).FindStringSubmatch(buf.String())
	if m == nil {
		t.Fatal("no key generated after credentials deleted")
	}
	if get(h, m[1]) != http.StatusOK {
		t.Fatal("lazily generated key rejected")
	}
}

// Deleting credentials.yaml reopens /auth/setup without a restart: the
// middleware lets setup through and the handler's own per-request check
// (modelled here) decides.
func TestSetupReopensWhenCredentialsDeleted(t *testing.T) {
	var present atomic.Bool
	present.Store(true)
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	setup := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if present.Load() {
			http.Error(w, "already configured", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := Middleware(func() bool { return true }, constStr("k"), present.Load, log)(setup)
	post := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/setup", nil))
		return rec.Code
	}
	if c := post(); c != http.StatusForbidden {
		t.Fatalf("setup with credentials = %d, want 403", c)
	}
	present.Store(false)
	if c := post(); c != http.StatusOK {
		t.Fatalf("setup after credentials deleted = %d, want 200", c)
	}
}
