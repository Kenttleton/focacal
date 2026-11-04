package writeback

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	"github.com/Kenttleton/focacal/internal/store"
)

// Config controls how planned blocks appear on source calendars.
type Config struct {
	CrossBusyTitle string // "Meeting"
	Focus          BlockConfig
	Lunch          BlockConfig
	Break          BlockConfig
}

// BlockConfig describes how a single block type is written back.
type BlockConfig struct {
	Enabled     bool
	Title       string
	ShowAs      string // busy | free | tentative
	Description string
}

// Emitter writes AI-planned blocks and cross-calendar busy blocks back to
// the user's real source calendars according to writeback config.
type Emitter struct {
	db         *store.DB
	cfg        Config
	connectors map[string]connector.Connector // keyed by source ID
	sources    []config.SourceConfig
	log        *slog.Logger
}

// New creates a write-back emitter.
func New(
	db *store.DB,
	cfg Config,
	connectors map[string]connector.Connector,
	sources []config.SourceConfig,
	log *slog.Logger,
) *Emitter {
	return &Emitter{
		db:         db,
		cfg:        cfg,
		connectors: connectors,
		sources:    sources,
		log:        log,
	}
}

// Name implements emitter.Emitter.
func (e *Emitter) Name() string { return "writeback" }

// Notify syncs planned blocks and cross-busy events to all configured sources.
// Notify is a documented no-op until the outputs piece (piece 3) lands.
// Write-back to external calendars is intentionally disabled in the
// read-only foundation; see the design record section 3.
func (e *Emitter) Notify(ctx context.Context) error {
	return nil
}

// writeBlocks writes AI-planned blocks to the given source.
func (e *Emitter) writeBlocks(ctx context.Context, conn connector.Connector, src config.SourceConfig, blocks []*store.Block) error {
	for _, b := range blocks {
		wbCfg := e.writebackCfgForBlock(b.Type)
		if !wbCfg.Enabled {
			continue
		}

		remoteEvent := &connector.RemoteEvent{
			RemoteID:    b.WrittenBack[src.ID],
			Title:       wbCfg.Title,
			Description: wbCfg.Description,
			Start:       b.Start,
			End:         b.End,
			ShowAs:      wbCfg.ShowAs,
			ManagedTag:  true,
		}

		remoteID, err := conn.WriteEvent(ctx, remoteEvent)
		if err != nil {
			e.log.Warn("write block to calendar", "block", b.ID, "source", src.ID, "err", err)
			continue
		}

		// Persist the remote ID so we can update it later.
		if b.WrittenBack == nil {
			b.WrittenBack = make(map[string]string)
		}
		b.WrittenBack[src.ID] = remoteID
		if err := e.db.UpsertBlock(ctx, b); err != nil {
			e.log.Warn("persist written_back", "block", b.ID, "err", err)
		}
	}
	return nil
}

// writeCrossBusy writes events from other sources as sanitized "Busy" blocks.
func (e *Emitter) writeCrossBusy(ctx context.Context, conn connector.Connector, src config.SourceConfig, events []*store.Event) error {
	for _, ev := range events {
		// Skip events that belong to this source — no point writing them back.
		if ev.SourceID == src.ID {
			continue
		}
		// Skip all-day events — they're usually OOO markers and can cause noise.
		if ev.AllDay {
			continue
		}

		remoteEvent := &connector.RemoteEvent{
			Title:  e.cfg.CrossBusyTitle,
			Start:  ev.Start,
			End:    ev.End,
			ShowAs: "busy",
		}

		if _, err := conn.WriteEvent(ctx, remoteEvent); err != nil {
			e.log.Warn("write cross-busy", "event", ev.ID, "source", src.ID, "err", err)
		}
	}
	return nil
}

func (e *Emitter) writebackCfgForBlock(t store.BlockType) BlockConfig {
	switch t {
	case store.BlockFocus:
		return e.cfg.Focus
	case store.BlockLunch:
		return e.cfg.Lunch
	case store.BlockBreak:
		return e.cfg.Break
	default:
		return BlockConfig{}
	}
}

// ── Sanitizer ────────────────────────────────────────────────────────────────

var videoURLRe = regexp.MustCompile(
	`https?://[^\s]*(zoom\.us|meet\.google\.com|teams\.microsoft\.com|webex\.com)[^\s]*`,
)

// SanitizeDescription strips video links and trims whitespace from an event description.
func SanitizeDescription(s string) string {
	s = videoURLRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return strings.Join(out, "\n")
}
