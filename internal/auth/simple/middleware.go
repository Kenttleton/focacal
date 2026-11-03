package simple

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Kenttleton/focacal/internal/config"
)

// Middleware returns an HTTP middleware enforcing auth.
func Middleware(cfg config.AuthConfig, log *slog.Logger) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	key := cfg.APIKey
	if key == "" && cfg.PasswordHash == "" {
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
			if cfg.SessionToken != "" {
				if err := bcrypt.CompareHashAndPassword([]byte(cfg.SessionToken), []byte(candidate)); err == nil {
					next.ServeHTTP(w, r)
					return
				}
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

// HashPassword hashes a password with bcrypt.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

// VerifyPassword checks a password against a bcrypt hash.
func VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// HashToken hashes a session token with bcrypt.
func HashToken(tok string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(tok), bcrypt.DefaultCost)
	return string(h), err
}
