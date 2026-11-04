package simple

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// Middleware returns an HTTP middleware enforcing auth, following live config.
//
//   - enabled is evaluated on every request; when it reports false the request
//     passes through. A nil enabled counts as enabled (fail closed).
//   - apiKey returns the current static auth.api_key ("" when unset).
//   - credentialsPresent reports whether credentials.yaml exists (an
//     unreadable file counts as present, the caller's choice). A nil hook
//     means no credentials. It is evaluated per request, and only when no
//     api_key is set.
//
// Session key rule: when auth is enabled, no api_key is set and no
// credentials exist, a one-off session key is generated (once per process,
// logged once) and accepted. It is generated at construction if that state
// already holds, otherwise lazily on the first request that finds it. It is
// accepted only while that state holds, so setting an api_key or creating
// credentials retires it immediately.
//
// /auth/setup, /auth/login and /auth/recover always pass through when auth is
// enabled; the setup handler itself refuses (per request) once credentials
// exist, so deleting credentials.yaml reopens setup without a restart.
//
// The middleware fails closed: if any hook panics the request gets 401.
func Middleware(enabled func() bool, apiKey func() string, credentialsPresent func() bool, log *slog.Logger) func(http.Handler) http.Handler {
	if enabled == nil {
		enabled = func() bool { return true }
	}
	if apiKey == nil {
		apiKey = func() string { return "" }
	}
	if credentialsPresent == nil {
		credentialsPresent = func() bool { return false }
	}

	var (
		genMu  sync.Mutex
		genKey string
	)
	// sessionKey returns the generated session key, creating and logging it on
	// first use.
	sessionKey := func() string {
		genMu.Lock()
		defer genMu.Unlock()
		if genKey == "" {
			genKey = GenerateKey()
			log.Info("OSS auth: no credentials configured — generated a session key",
				"api_key", genKey,
				"hint", "set auth.api_key or run /auth/setup to create a password",
			)
			fmt.Printf("\n[focacal] Generated API key: %s\n\n", genKey)
		}
		return genKey
	}

	// Startup: keep today's behaviour of printing the key right away.
	func() {
		defer func() { _ = recover() }()
		if enabled() && apiKey() == "" && !credentialsPresent() {
			sessionKey()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, pass := func() (allowed, pass bool) {
				defer func() {
					if rec := recover(); rec != nil {
						log.Error("auth: hook panicked; denying request", "panic", fmt.Sprint(rec))
						allowed, pass = false, false
					}
				}()
				if !enabled() {
					return true, true
				}
				// Allow setup/login endpoints without auth.
				if r.URL.Path == "/auth/setup" || r.URL.Path == "/auth/login" || r.URL.Path == "/auth/recover" {
					return true, true
				}
				staticKey := apiKey()
				session := ""
				if staticKey == "" && !credentialsPresent() {
					session = sessionKey() // generated (and logged) on first need
				}
				candidate := extractKey(r)
				if candidate == "" {
					return false, false
				}
				if staticKey != "" && constantTimeEqual(candidate, staticKey) {
					return true, true
				}
				if session != "" && constantTimeEqual(candidate, session) {
					return true, true
				}
				if IsValidToken(candidate) {
					return true, true
				}
				return false, false
			}()
			if allowed && pass {
				next.ServeHTTP(w, r)
				return
			}
			unauthorized(w)
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="focacal"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func extractKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "ApiKey "); ok {
			return strings.TrimSpace(after)
		}
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return r.URL.Query().Get("api_key")
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// GenerateKey returns a random session key.
func GenerateKey() string {
	return generateKeyImpl()
}

func generateKeyImpl() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("auth: cannot generate API key: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// BcryptCost is the bcrypt cost HashPassword uses. It stays
// bcrypt.DefaultCost in production; tests may lower it (TestMain) for speed.
var BcryptCost = bcrypt.DefaultCost

// HashPassword hashes a password with bcrypt at BcryptCost.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(h), err
}

// VerifyPassword checks a password against a bcrypt hash.
func VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// In-memory token registry (survives for process lifetime). Guarded by
// tokensMu: handlers and middleware run on concurrent goroutines. generation
// counts RevokeAll calls so a token minted against credentials that were
// replaced meanwhile can be refused (see StoreTokenAt).
var (
	tokensMu    sync.RWMutex
	validTokens = make(map[string]bool)
	generation  uint64
)

// StoreToken registers a newly issued token unconditionally.
func StoreToken(tok string) {
	tokensMu.Lock()
	validTokens[tok] = true
	tokensMu.Unlock()
}

// Generation returns the current revocation generation. Capture it before
// reading the credentials a token will be issued against.
func Generation() uint64 {
	tokensMu.RLock()
	defer tokensMu.RUnlock()
	return generation
}

// StoreTokenAt registers tok only if no RevokeAll happened since gen was
// read; it reports whether the token was stored.
func StoreTokenAt(tok string, gen uint64) bool {
	tokensMu.Lock()
	defer tokensMu.Unlock()
	if gen != generation {
		return false
	}
	validTokens[tok] = true
	return true
}

// IsValidToken reports whether tok was issued this process and not revoked.
func IsValidToken(tok string) bool {
	tokensMu.RLock()
	defer tokensMu.RUnlock()
	return validTokens[tok]
}

// RevokeAll invalidates every issued token and advances the generation
// (used when credentials change: setup, reset, recover).
func RevokeAll() {
	tokensMu.Lock()
	clear(validTokens)
	generation++
	tokensMu.Unlock()
}
