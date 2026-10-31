package caldav

import (
	"fmt"
	"net/http"

	"github.com/emersion/go-webdav"
	gocaldav "github.com/emersion/go-webdav/caldav"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// Connector implements connector.Connector for CalDAV sources (iCloud, Nextcloud,
// Radicale, Baikal, Fastmail, etc.).
//
// Authentication:
//   - type: basic  — HTTP Basic Auth; for iCloud use an app-specific password
//   - type: bearer — Bearer token in Authorization header
//
// Push notifications are not supported universally across CalDAV servers; the
// connector relies on the polling loop in cmd/start.go. RegisterWebhook and
// ValidateWebhook are no-ops.
type Connector struct {
	cfg config.SourceConfig
	db  *store.DB
}

// New creates a CalDAV connector. The source config must have Type == "caldav"
// and a non-empty CalDAVURL.
func New(cfg config.SourceConfig, db *store.DB) (*Connector, error) {
	if cfg.CalDAVURL == "" {
		return nil, fmt.Errorf("caldav[%s]: caldav_url is required", cfg.ID)
	}
	return &Connector{cfg: cfg, db: db}, nil
}

// SourceID returns the unique identifier for this connector instance.
func (c *Connector) SourceID() string { return c.cfg.ID }

// caldavClient builds an authenticated CalDAV client.
func (c *Connector) caldavClient() (*gocaldav.Client, error) {
	var httpClient webdav.HTTPClient = http.DefaultClient
	switch c.cfg.AuthType {
	case "bearer":
		httpClient = &bearerHTTPClient{c: http.DefaultClient, token: c.cfg.Password}
	default: // "basic" or empty
		if c.cfg.Username != "" {
			httpClient = webdav.HTTPClientWithBasicAuth(http.DefaultClient, c.cfg.Username, c.cfg.Password)
		}
	}
	return gocaldav.NewClient(httpClient, c.cfg.CalDAVURL)
}

// bearerHTTPClient injects an Authorization: Bearer header on every request.
type bearerHTTPClient struct {
	c     *http.Client
	token string
}

func (b *bearerHTTPClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.c.Do(req)
}
