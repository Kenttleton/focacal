package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

const (
	scopeCalendarReadonly = "https://www.googleapis.com/auth/calendar.readonly"
	scopeCalendarEvents   = "https://www.googleapis.com/auth/calendar.events"
)

// Connector implements connector.Connector for Google Calendar.
type Connector struct {
	cfg    config.SourceConfig
	db     *store.DB
	oauth  *oauth2.Config
	client *http.Client
}

// New creates a Google Connector from source config.
func New(cfg config.SourceConfig, db *store.DB, redirectURL string) (*Connector, error) {
	oauthCfg := &oauth2.Config{
		Scopes:      []string{scopeCalendarReadonly},
		Endpoint:    google.Endpoint,
		RedirectURL: redirectURL + "/oauth/callback/google",
	}

	// Load client credentials from environment (injected via .env / Docker secret).
	// The CredentialsFile field can optionally point to a credentials.json instead.
	clientID, clientSecret, err := loadClientCredentials(cfg)
	if err != nil {
		return nil, err
	}
	oauthCfg.ClientID = clientID
	oauthCfg.ClientSecret = clientSecret

	c := &Connector{cfg: cfg, db: db, oauth: oauthCfg}

	// Try to load a stored token and build an auto-refreshing HTTP client.
	if err := c.loadStoredToken(context.Background()); err != nil {
		// Not fatal at construction time — connector needs auth before syncing.
		fmt.Printf("google[%s]: no stored token yet, run 'focacal auth google %s'\n",
			cfg.ID, cfg.ID)
	}

	return c, nil
}

// SourceID returns the config ID for this connector.
func (c *Connector) SourceID() string { return c.cfg.ID }

// AuthURL returns the OAuth2 authorization URL for the PKCE flow.
func (c *Connector) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
	)
}

// Exchange completes the OAuth2 code exchange and persists the token.
func (c *Connector) Exchange(ctx context.Context, code string) error {
	token, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}
	return c.persistToken(ctx, token)
}

// IsAuthorized reports whether a valid token exists for this connector.
func (c *Connector) IsAuthorized() bool {
	return c.client != nil
}

func (c *Connector) loadStoredToken(ctx context.Context) error {
	tokenJSON, err := c.db.GetOAuthToken(ctx, c.cfg.ID)
	if err != nil || tokenJSON == "" {
		return fmt.Errorf("no stored token")
	}
	var token oauth2.Token
	if err := json.Unmarshal([]byte(tokenJSON), &token); err != nil {
		return fmt.Errorf("unmarshal token: %w", err)
	}
	c.client = c.oauth.Client(ctx, &token)
	return nil
}

func (c *Connector) persistToken(ctx context.Context, token *oauth2.Token) error {
	b, err := json.Marshal(token)
	if err != nil {
		return err
	}
	if err := c.db.SetOAuthToken(ctx, c.cfg.ID, string(b)); err != nil {
		return err
	}
	c.client = c.oauth.Client(ctx, token)
	return nil
}

// httpClient returns the authorized HTTP client, or an error if not yet authed.
func (c *Connector) httpClient() (*http.Client, error) {
	if c.client == nil {
		return nil, fmt.Errorf("google[%s]: not authorized — run 'focacal auth google %s'",
			c.cfg.ID, c.cfg.ID)
	}
	return c.client, nil
}

// loadClientCredentials resolves Google OAuth client ID/secret.
// Priority: GOOGLE_CLIENT_ID/SECRET env vars > credentials JSON file.
func loadClientCredentials(cfg config.SourceConfig) (clientID, clientSecret string, err error) {
	// Try bare environment variables first (simplest Docker secret pattern).
	if id := os.Getenv("GOOGLE_CLIENT_ID"); id != "" {
		return id, os.Getenv("GOOGLE_CLIENT_SECRET"), nil
	}
	// Per-source env vars: GOOGLE_CLIENT_ID_WORK_GOOGLE (source ID uppercased, hyphens→underscores).
	sourceKey := strings.ToUpper(strings.ReplaceAll(cfg.ID, "-", "_"))
	if id := os.Getenv("GOOGLE_CLIENT_ID_" + sourceKey); id != "" {
		return id, os.Getenv("GOOGLE_CLIENT_SECRET_" + sourceKey), nil
	}
	// Try credentials file if specified.
	if cfg.CredentialsFile != "" {
		creds, e := loadCredentialsFile(cfg.CredentialsFile)
		if e == nil {
			return creds.clientID, creds.clientSecret, nil
		}
	}
	return "", "", fmt.Errorf(
		"google[%s]: no credentials found — set GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET "+
			"in environment or provide credentials_file in config", cfg.ID)
}

type credentialsFile struct {
	clientID     string
	clientSecret string
}

func loadCredentialsFile(path string) (*credentialsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credentials file: %w", err)
	}

	// google credentials.json can be either "installed" or "web" app type.
	type creds struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	var wrapper struct {
		Installed creds `json:"installed"`
		Web       creds `json:"web"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse credentials file: %w", err)
	}

	if wrapper.Installed.ClientID != "" {
		return &credentialsFile{
			clientID:     wrapper.Installed.ClientID,
			clientSecret: wrapper.Installed.ClientSecret,
		}, nil
	}
	if wrapper.Web.ClientID != "" {
		return &credentialsFile{
			clientID:     wrapper.Web.ClientID,
			clientSecret: wrapper.Web.ClientSecret,
		}, nil
	}
	return nil, fmt.Errorf("no client_id found in credentials file %q", path)
}

// calendarIDs returns the list of calendar IDs to sync.
// Falls back to "primary" if none configured.
func (c *Connector) calendarIDs() []string {
	if len(c.cfg.CalendarIDs) > 0 {
		return c.cfg.CalendarIDs
	}
	return []string{"primary"}
}

// baseURL for the Google Calendar API.
const googleCalAPIBase = "https://www.googleapis.com/calendar/v3"

// timeToRFC3339 formats a time.Time for the Google Calendar API.
func timeToRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
