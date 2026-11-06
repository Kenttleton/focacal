package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/Kenttleton/focacal/internal/config"
	googleconn "github.com/Kenttleton/focacal/internal/connector/google"
	outlookconn "github.com/Kenttleton/focacal/internal/connector/outlook"
	"github.com/Kenttleton/focacal/internal/store"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth <provider> <account-or-calendar-id>",
	Short: "Authorize an OAuth account via OAuth2",
	Long: `Authorize a Google or Outlook account and persist the token.

Examples:
  focacal auth google work
  focacal auth outlook work-team

The id is an account id or the id of a calendar of that account in
calendars.yaml. The token is stored once per account, so every calendar of the
account shares it.
On first run this opens your browser for the OAuth2 consent screen.
The callback is handled on localhost — no internet exposure needed.`,
	Args: cobra.ExactArgs(2),
	RunE: runAuth,
}

func init() {
	rootCmd.AddCommand(authCmd)
}

func runAuth(_ *cobra.Command, args []string) error {
	provider := args[0]
	id := args[1]

	_, live, err := loadConfig(newLogger())
	if err != nil {
		return err
	}

	cfg := live.Current()

	srcCfg, err := findAuthSource(cfg, provider, id)
	if err != nil {
		return err
	}

	db, err := store.Open("/data/focacal.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	redirectBase := cfg.OAuth.RedirectBaseURL
	oauthPort := cfg.Server.OAuthPort
	callbackPath := "/oauth/callback/" + provider

	// Generate a random state token to prevent CSRF.
	state, err := randomState()
	if err != nil {
		return err
	}

	// Start a temporary HTTP server to receive the OAuth callback.
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{Addr: fmt.Sprintf(":%d", oauthPort)}
	http.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth state mismatch — possible CSRF")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no code in callback")
			return
		}
		fmt.Fprint(w, successHTML)
		codeCh <- code
	})

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Build the auth URL for the chosen provider.
	var authURL string
	switch provider {
	case "google":
		c, err := googleconn.New(*srcCfg, db, redirectBase)
		if err != nil {
			return err
		}
		authURL = c.AuthURL(state)
	case "outlook":
		c, err := outlookconn.New(*srcCfg, db, redirectBase, "common")
		if err != nil {
			return err
		}
		authURL = c.AuthURL(state)
	default:
		return fmt.Errorf("unknown provider %q — use 'google' or 'outlook'", provider)
	}

	fmt.Printf("\nOpening browser for %s OAuth consent...\n", provider)
	fmt.Printf("If the browser doesn't open, visit:\n\n  %s\n\n", authURL)
	openBrowser(authURL)

	// Wait for callback (2 minute timeout).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("auth timed out — no callback received within 2 minutes")
	}

	// Shutdown the temporary callback server.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)

	// Exchange the code for a token and persist it.
	switch provider {
	case "google":
		c, err := googleconn.New(*srcCfg, db, redirectBase)
		if err != nil {
			return err
		}
		if err := c.Exchange(context.Background(), code); err != nil {
			return fmt.Errorf("token exchange: %w", err)
		}
	case "outlook":
		c, err := outlookconn.New(*srcCfg, db, redirectBase, "common")
		if err != nil {
			return err
		}
		if err := c.Exchange(context.Background(), code); err != nil {
			return fmt.Errorf("token exchange: %w", err)
		}
	}

	fmt.Printf("✓ %s account %q authorized successfully.\n", provider, srcCfg.TokenKey())
	fmt.Println("  Run 'focacal sync' to perform the initial calendar sync.")
	return nil
}

// findAuthSource resolves the id given to `focacal auth` to the source whose
// connector performs the OAuth flow. id is an account id (an account id wins
// over a calendar id of the same spelling) or a calendar id of an OAuth
// account. For an account id the first resolved calendar of the account is
// used; an account without calendars gets a source built from the account
// itself. The saved token is keyed by TokenKey(), the account id.
func findAuthSource(cfg *config.Config, provider, id string) (*config.SourceConfig, error) {
	var src *config.SourceConfig
	for i := range cfg.Sources {
		if cfg.Sources[i].AccountID == id {
			src = &cfg.Sources[i]
			break
		}
	}
	if src == nil {
		for _, a := range cfg.Accounts {
			if a.ID == id {
				src = &config.SourceConfig{ID: a.ID, AccountID: a.ID, Type: a.Type,
					CredentialsFile: a.CredentialsFile, TokenFile: a.TokenFile}
				break
			}
		}
	}
	if src == nil {
		for i := range cfg.Sources {
			if cfg.Sources[i].ID == id {
				src = &cfg.Sources[i]
				break
			}
		}
	}
	if src == nil {
		return nil, fmt.Errorf("no account or calendar with id %q in calendars.yaml", id)
	}
	if src.Type != "google" && src.Type != "outlook" {
		return nil, fmt.Errorf("%q is not an OAuth account (type %q); only google and outlook need 'focacal auth'", id, src.Type)
	}
	if src.Type != provider {
		return nil, fmt.Errorf("%q is a %s account, not %s", id, src.Type, provider)
	}
	return src, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "linux":
		cmd, args = "xdg-open", []string{url}
	default:
		_ = url
		return
	}
	exec.Command(cmd, args...).Start()
}

const successHTML = `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:2rem">
<h2>Authorization successful</h2>
<p>You can close this tab and return to focacal.</p>
</body></html>`

// ── sync command ──────────────────────────────────────────────────────────────

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Trigger an immediate calendar sync and replan",
	RunE:  runSync,
}

func init() {
	rootCmd.AddCommand(syncCmd)
}

func runSync(_ *cobra.Command, _ []string) error {
	// POST to the REST API's /replan endpoint — the running server handles it.
	resp, err := http.Post("http://localhost:5233/replan", "application/json", nil)
	if err != nil {
		return fmt.Errorf("could not reach focacal REST API — is 'focacal start' running?\n%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		fmt.Println("✓ Replan triggered. Check logs for progress.")
		return nil
	}
	return fmt.Errorf("unexpected response: %s", resp.Status)
}

// ── status command ────────────────────────────────────────────────────────────

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show focacal service health",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, _ []string) error {
	resp, err := http.Get("http://localhost:5233/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "focacal REST API unreachable: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("✓ focacal is running")
	} else {
		fmt.Fprintf(os.Stderr, "focacal returned %s\n", resp.Status)
		os.Exit(1)
	}
	return nil
}
