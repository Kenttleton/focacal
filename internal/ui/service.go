package ui

import (
	"context"
	"fmt"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// ErrNoConfigFile is returned when no config file is in use.
var ErrNoConfigFile = fmt.Errorf("ui: no config file in use")

// Service is the Wails-bound UI backend.
type Service struct {
	store  store.Store
	cfg    *config.UserConfig
	remote bool
}

// NewService creates a Service for local mode.
func NewService(st store.Store, uc *config.UserConfig) *Service {
	return &Service{store: st, cfg: uc}
}

// Events returns events in [from, to) as DTOs (placeholder).
func (s *Service) Events(ctx context.Context, from, to string) ([]map[string]any, error) {
	if s.store == nil {
		return nil, fmt.Errorf("ui: no store (remote mode not implemented)")
	}
	_ = from
	_ = to
	return nil, nil
}

// BackendMode reports local or remote.
func (s *Service) BackendMode(ctx context.Context) string {
	if s.remote {
		return "remote"
	}
	return "local"
}
