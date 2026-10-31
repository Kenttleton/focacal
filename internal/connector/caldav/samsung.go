package caldav

import (
	"fmt"

	"github.com/you/focacal/internal/config"
	"github.com/you/focacal/internal/store"
)

// NewForSamsung creates a CalDAV connector pre-configured for Samsung Calendar.
//
// Samsung Calendar is a local calendar app on Galaxy devices. It does not
// expose a proprietary cloud API. Events are stored in one of three backends,
// and the right integration depends on which sync account the user has set up:
//
//   - Google account (most common): Samsung Calendar mirrors Google Calendar.
//     Use the google source type instead and skip this connector entirely.
//
//   - CalDAV account (Nextcloud, Radicale, iCloud, Fastmail, etc.): Samsung
//     Calendar connects to a standard CalDAV server. Configure caldav_url,
//     username, and password in the source config and this connector handles it.
//
//   - Exchange / EWS account (corporate): Samsung Calendar syncs with an
//     Exchange server. Use the outlook source type with an Office 365 account,
//     or a dedicated Exchange connector if your deployment is on-premises.
//
// If the user's Samsung Calendar is backed by Google Calendar, set type: google
// in config.yaml instead. This connector only covers the CalDAV-backed case.
func NewForSamsung(cfg config.SourceConfig, db *store.DB) (*Connector, error) {
	if cfg.CalDAVURL == "" {
		return nil, fmt.Errorf(
			"samsung[%s]: caldav_url is required.\n"+
				"Samsung Calendar stores events in Google Calendar, Exchange, or a CalDAV\n"+
				"server. If your Samsung Calendar syncs with Google, use type: google instead.\n"+
				"If it syncs with a CalDAV server (Nextcloud, iCloud, Fastmail, etc.), set\n"+
				"caldav_url to that server's URL and configure username/password.",
			cfg.ID,
		)
	}
	// Samsung Calendar uses standard CalDAV auth — Basic Auth is the default.
	if cfg.AuthType == "" {
		cfg.AuthType = "basic"
	}
	return &Connector{cfg: cfg, db: db}, nil
}
