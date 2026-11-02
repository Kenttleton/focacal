package ics

import (
	"context"
	"fmt"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

// Connector implements connector.Connector for an ICS feed.
type Connector struct {
	cfg    config.SourceConfig
	db     *store.DB
	etag   string
	lastMod string
}

// New creates an ICS connector.
func New(cfg config.SourceConfig, db *store.DB) (*Connector, error) {
	return &Connector{cfg: cfg, db: db}, nil
}

func (c *Connector) SourceID() string { return c.cfg.ID }

func (c *Connector) RegisterWebhook(ctx context.Context, webhookURL string) error {
	return fmt.Errorf("not supported")
}

func (c *Connector) ValidateWebhook(ctx context.Context, payload []byte, headers map[string]string) (string, error) {
	return "", fmt.Errorf("not supported")
}

func (c *Connector) WriteEvent(ctx context.Context, e *connector.RemoteEvent) (string, error) {
	return "", fmt.Errorf("not supported")
}

func (c *Connector) DeleteEvent(ctx context.Context, remoteID string) error {
	return fmt.Errorf("not supported")
}
