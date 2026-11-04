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

	"github.com/Kenttleton/focacal/internal/config"
)

// Middleware returns an HTTP middleware enforcing auth. credentialsPresent
// reports whether login credentials (credentials.yaml) exist. It is called
// once, here at construction (startup): when neither credentials nor
// auth.api_key are set, a one-off session key is generated and logged. A nil
// hook means no credentials. It is not consulted per request: /auth/setup,
// /auth/login and /auth/recover always pass through, and the setup handler
// itself refuses (per request) once credentials.yaml exists.
func Middleware(cfg config.AuthConfig, credentialsPresent func() bool, log *slog.Logger) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	key := cfg.APIKey
	if key == "" && (credentialsPresent == nil || !credentialsPresent()) {
		key = GenerateKey()
		log.Info("OSS auth: no credentials configured — generated a session key",
			"api_key", key,
			"hint", "set auth.api_key or run /auth/setup to create a password",
		)
		fmt.Printf("\n[focacal] Generated API key: %s\n\n", key)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow setup/login endpoints without auth.
			if r.URL.Path == "/auth/setup" || r.URL.Path == "/auth/login" || r.URL.Path == "/auth/recover" {
				next.ServeHTTP(w, r)
				return
			}
			candidate := extractKey(r)
			if candidate == "" {
				unauthorized(w)
				return
			}
			if key != "" && constantTimeEqual(candidate, key) {
				next.ServeHTTP(w, r)
				return
			}
			if IsValidToken(candidate) {
				next.ServeHTTP(w, r)
				return
			}
			if cfg.APIKey != "" && constantTimeEqual(candidate, cfg.APIKey) {
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
