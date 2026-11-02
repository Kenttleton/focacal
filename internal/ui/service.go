package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/Kenttleton/focacal/internal/calendar"
	"github.com/Kenttleton/focacal/internal/conflict"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/emitter/rest"
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

// Events returns events in [from, to).
func (s *Service) Events(ctx context.Context, from, to time.Time) ([]rest.EventDTO, error) {
	if s.store == nil {
		return nil, fmt.Errorf("ui: no store (remote mode not implemented)")
	}
	evs, err := calendar.MergedEvents(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("ui: events: %w", err)
	}
	out := make([]rest.EventDTO, 0, len(evs))
	now := time.Now()
	for _, e := range evs {
		out = append(out, rest.EventToDTO(e, now))
	}
	return out, nil
}

// Conflicts returns conflicts in [from, to).
func (s *Service) Conflicts(ctx context.Context, from, to time.Time) ([]conflict.Conflict, error) {
	if s.store == nil {
		return nil, fmt.Errorf("ui: no store")
	}
	return conflict.Fetch(ctx, from, to)
}

// DismissConflict records a dismissal.
func (s *Service) DismissConflict(ctx context.Context, key string) error {
	if s.store == nil {
		return fmt.Errorf("ui: no store")
	}
	return s.store.DismissConflict(ctx, key)
}

// Sources returns source summaries.
func (s *Service) Sources(ctx context.Context) ([]map[string]any, error) {
	if s.cfg == nil {
		return nil, fmt.Errorf("ui: no config")
	}
	out := make([]map[string]any, 0, len(s.cfg.Sources))
	for _, src := range s.cfg.Sources {
		out = append(out, map[string]any{
			"id":           src.ID,
			"label":        src.Label,
			"type":         src.Type,
			"calendar_ids": src.EffectiveCalendarIDs(),
		})
	}
	return out, nil
}

// BackendMode reports local or remote.
func (s *Service) BackendMode(ctx context.Context) string {
	if s.remote {
		return "remote"
	}
	return "local"
}
