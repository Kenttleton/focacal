package outlook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/microsoft"

	"github.com/you/focacal/internal/config"
	"github.com/you/focacal/internal/store"
)

const (
	scopeCalendarsRead      = "Calendars.Read"
	scopeCalendarsReadWrite = "Calendars.ReadWrite"
	scopeOfflineAccess      = "offline_access"
	scopeOpenID             = "openid"
	scopeProfile            = "profile"
)

// Connector implements connector.Connector for Microsoft Outlook / M365.
type Connector struct {
	cfg    config.SourceConfig
	db     *store.DB
	oauth  *oauth2.Config
	client *http.Client
}

// New creates an Outlook Connector from source config.
// tenantID can be "common" for multi-tenant or a specific tenant UUID.
func New(cfg config.SourceConfig, db *store.DB, redirectURL, tenantID string) (*Connector, error) {
	if tenantID == "" {
		tenantID = "common"
	}

	scopes := []string{scopeOpenID, scopeProfile, scopeOfflineAccess, scopeCalendarsRead}
	if cfg.Writeback || cfg.CrossBusy {
		scopes = append(scopes, scopeCalendarsReadWrite)
	}

	clientID, clientSecret, err := loadClientCredentials(cfg)
	if err != nil {
		return nil, err
	}

	oauthCfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       scopes,
		Endpoint:     microsoft.AzureADEndpoint(tenantID),
		RedirectURL:  redirectURL + "/oauth/callback/outlook",
	}

	c := &Connector{cfg: cfg, db: db, oauth: oauthCfg}
	if err := c.loadStoredToken(context.Background()); err != nil {
		fmt.Printf("outlook[%s]: no stored token yet, run 'focacal auth outlook %s'\n",
			cfg.ID, cfg.ID)
	}
	return c, nil
}

// SourceID returns the config ID for this connector.
func (c *Connector) SourceID() string { return c.cfg.ID }

// AuthURL returns the OAuth2 authorization URL.
func (c *Connector) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Exchange completes the OAuth2 code exchange and persists the token.
func (c *Connector) Exchange(ctx context.Context, code string) error {
	token, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}
	return c.persistToken(ctx, token)
}

// IsAuthorized reports whether a valid token exists.
func (c *Connector) IsAuthorized() bool { return c.client != nil }

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

func (c *Connector) httpClient() (*http.Client, error) {
	if c.client == nil {
		return nil, fmt.Errorf("outlook[%s]: not authorized — run 'focacal auth outlook %s'",
			c.cfg.ID, c.cfg.ID)
	}
	return c.client, nil
}

func loadClientCredentials(cfg config.SourceConfig) (clientID, clientSecret string, err error) {
	if id := os.Getenv("OUTLOOK_CLIENT_ID"); id != "" {
		return id, os.Getenv("OUTLOOK_CLIENT_SECRET"), nil
	}
	sourceKey := strings.ToUpper(strings.ReplaceAll(cfg.ID, "-", "_"))
	if id := os.Getenv("OUTLOOK_CLIENT_ID_" + sourceKey); id != "" {
		return id, os.Getenv("OUTLOOK_CLIENT_SECRET_" + sourceKey), nil
	}
	return "", "", fmt.Errorf(
		"outlook[%s]: set OUTLOOK_CLIENT_ID / OUTLOOK_CLIENT_SECRET in environment", cfg.ID)
}

const graphAPIBase = "https://graph.microsoft.com/v1.0"

// isExpiredRefreshToken returns true when a Graph 401 response indicates that
// the refresh token itself has expired. This is distinct from a normal access
// token expiry, which golang.org/x/oauth2 handles transparently.
func isExpiredRefreshToken(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	var apiErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &apiErr) != nil {
		return false
	}
	return apiErr.Error.Code == "InvalidAuthenticationToken"
}
