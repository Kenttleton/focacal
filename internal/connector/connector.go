package connector

import (
	"context"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// Connector is implemented by each calendar provider (Google, Outlook, …).
type Connector interface {
	// SourceID returns the unique identifier for this connector instance.
	SourceID() string

	// Sync performs a full or incremental sync of events into the store.
	// It updates sync_state when complete.
	Sync(ctx context.Context) error

	// RegisterWebhook registers a push notification channel with the provider.
	// The webhookURL must be publicly reachable by the provider.
	RegisterWebhook(ctx context.Context, webhookURL string) error

	// ValidateWebhook verifies an incoming webhook payload and returns
	// the source ID it belongs to. Returns an error if the payload is invalid.
	ValidateWebhook(ctx context.Context, payload []byte, headers map[string]string) (sourceID string, err error)

	// WriteEvent creates or updates a remote event, returning its remote ID.
	WriteEvent(ctx context.Context, e *RemoteEvent) (remoteID string, err error)

	// DeleteEvent removes a remote event by its remote ID.
	DeleteEvent(ctx context.Context, remoteID string) error
}

// RemoteEvent is the sanitized representation written to a source calendar.
type RemoteEvent struct {
	RemoteID    string // empty = create new; non-empty = update existing
	Title       string
	Description string
	Location    string
	Start       interface{} // time.Time
	End         interface{} // time.Time
	ShowAs      string      // busy | free | tentative
	ManagedTag  bool        // mark with X-FOCACAL-MANAGED extended property
}

// Factory builds a Connector from a SourceConfig and a store reference.
type Factory func(cfg config.SourceConfig, db *store.DB) (Connector, error)
