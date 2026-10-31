package simple

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Kenttleton/focacal/internal/config"
)

// Middleware returns an HTTP middleware that enforces the static API key
// configured in auth.api_key.
//
// Requests must supply the key as:
//   - Header:      Authorization: ApiKey <key>
//   - Query param: ?api_key=<key>  (for CalDAV clients that cannot set headers)
//
// If auth.enabled = false the middleware is a no-op — all requests pass through.
// If auth.api_key is empty, a key is auto-generated, printed to stdout once,
// and used for the lifetime of the process (restart to get a new key).
func Middleware(cfg config.AuthConfig, log *slog.Logger) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	key := cfg.APIKey
	if key == "" {
		key = generateKey()
		log.Info("OSS auth: no api_key configured — generated one for this session",
			"api_key", key,
			"hint", "set auth.api_key in config.yaml to make it persistent",
		)
		fmt.Printf("\n[focacal] Generated API key: %s\n\n", key)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			candidate := extractKey(r)
			if candidate == "" || !constantTimeEqual(candidate, key) {
				w.Header().Set("WWW-Authenticate", `ApiKey realm="focacal"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractKey reads the API key from the Authorization header or api_key query param.
func extractKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "ApiKey "); ok {
			return strings.TrimSpace(after)
		}
		// Also accept Bearer for clients that only support OAuth-style headers.
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	return r.URL.Query().Get("api_key")
}

// constantTimeEqual compares two strings in constant time to prevent timing attacks.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// generateKey returns a 32-character random hex string (128 bits of entropy).
func generateKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("auth: cannot generate API key: " + err.Error())
	}
	return hex.EncodeToString(b)
}
