package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/Kenttleton/focacal/internal/auth/simple"
	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	caldavconn "github.com/Kenttleton/focacal/internal/connector/caldav"
	googleconn "github.com/Kenttleton/focacal/internal/connector/google"
	outlookconn "github.com/Kenttleton/focacal/internal/connector/outlook"
	"github.com/Kenttleton/focacal/internal/emitter/caldav"
	"github.com/Kenttleton/focacal/internal/emitter/rest"
	"github.com/Kenttleton/focacal/internal/emitter/writeback"
	"github.com/Kenttleton/focacal/internal/mcp"
	"github.com/Kenttleton/focacal/internal/store"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start all focacal services (CalDAV, REST, MCP, sync)",
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(_ *cobra.Command, _ []string) error {
	log := newLogger()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Open the SQLite store.
	db, err := store.Open("/data/focacal.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	// Build the event bus (500ms debounce — collapses rapid webhook bursts).
	b := bus.New(500 * time.Millisecond)

	// Build connectors for each configured source.
	connectors := make(map[string]connector.Connector)
	redirectBase := cfg.OAuth.RedirectBaseURL

	for _, src := range cfg.Sources {
		switch src.Type {
		case "google":
			c, err := googleconn.New(src, db, redirectBase)
			if err != nil {
				log.Warn("init google connector", "id", src.ID, "err", err)
				continue
			}
			connectors[src.ID] = c
		case "outlook":
			c, err := outlookconn.New(src, db, redirectBase, "common")
			if err != nil {
				log.Warn("init outlook connector", "id", src.ID, "err", err)
				continue
			}
			connectors[src.ID] = c
		case "caldav":
			c, err := caldavconn.New(src, db)
			if err != nil {
				log.Warn("init caldav connector", "id", src.ID, "err", err)
				continue
			}
			connectors[src.ID] = c
		default:
			log.Warn("unknown source type", "type", src.Type, "id", src.ID)
		}
	}

	// Build emitters.
	caldavAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.CalDAVPort)
	restAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.RESTPort)
	mcpAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.MCPPort)

	health := rest.NewHealthState()
	caldavSrv := caldav.New(caldavAddr, log)
	restSrv := rest.New(connectors, health, restAddr, log)
	mcpSrv := mcp.New(mcpAddr, log)

	// Apply OSS auth middleware to all servers (no-op when auth.enabled = false).
	authMW := simple.Middleware(cfg.Auth, log)
	caldavSrv.WrapHandler(authMW)
	restSrv.WrapHandler(authMW)
	mcpSrv.WrapHandler(authMW)
	wbEmitter := writeback.New(db, cfg.Writeback, connectors, cfg.Sources, log)

	// Build the Ollama agent.
	agent := mcp.NewAgent(cfg, cfg.Server.MCPPort, log)

	// Wire up the bus: calendar changes → replan → notify emitters.
	b.Subscribe(func(msg bus.Message) {
		switch msg.Type {
		case bus.EventCalendarChanged, bus.EventReplanRequested:
			reqLog := log.With("request_id", msg.RequestID, "reason", msg.Type, "source", msg.SourceID)
			reqLog.Info("bus: replan triggered")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if err := agent.Replan(ctx, reqLog); err != nil {
				reqLog.Error("agent replan failed", "err", err)
				return
			}
			health.SetLastReplan(time.Now())
			b.Publish(bus.Message{
				Type:      bus.EventBlocksUpdated,
				RequestID: msg.RequestID,
			})
		case bus.EventBlocksUpdated:
			log.Info("bus: notifying emitters", "request_id", msg.RequestID)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, e := range []interface{ Notify(context.Context) error }{caldavSrv, restSrv, mcpSrv, wbEmitter} {
				if err := e.Notify(ctx); err != nil {
					log.Error("emitter notify failed", "err", err)
				}
			}
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Inject the single-user store, config, and bus into the root context so that
	// all HTTP request contexts (via BaseContext) and background goroutines can
	// retrieve them without holding direct references.
	ctx = store.WithStore(ctx, db)
	ctx = config.WithUserConfig(ctx, &cfg.UserConfig)
	ctx = bus.WithDispatcher(ctx, b)

	// Start all servers concurrently.
	errCh := make(chan error, 4)
	go func() { errCh <- caldavSrv.Start(ctx) }()
	go func() { errCh <- restSrv.Start(ctx) }()
	go func() { errCh <- mcpSrv.Start(ctx) }()

	// Background sync: initial sync then poll every 5 minutes.
	// Webhook registration is attempted on each cycle; RegisterWebhook is
	// idempotent and renews subscriptions only when they are about to expire.
	webhookBase := cfg.OAuth.RedirectBaseURL
	go func() {
		registerWebhooks(ctx, log, connectors, webhookBase)
		syncAll(ctx, log, connectors, b, db)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				registerWebhooks(ctx, log, connectors, webhookBase)
				syncAll(ctx, log, connectors, b, db)
			}
		}
	}()

	// WAL checkpoint: runs daily at 3 AM to reclaim WAL file space.
	go func() {
		for {
			now := time.Now()
			next3AM := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, now.Location())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next3AM)):
				if err := db.WALCheckpoint(ctx); err != nil {
					log.Warn("WAL checkpoint failed", "err", err)
				} else {
					log.Info("WAL checkpoint complete")
				}
			}
		}
	}()

	log.Info("focacal started",
		"caldav", caldavAddr,
		"rest", restAddr,
		"mcp", mcpAddr,
	)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("focacal shutting down")
		return nil
	}
}

// registerWebhooks calls RegisterWebhook on every connector using the public
// base URL so each source gets a dedicated /webhooks/{sourceID} endpoint.
// Errors are logged but do not block the sync cycle — polling covers the gap.
func registerWebhooks(ctx context.Context, log *slog.Logger, connectors map[string]connector.Connector, baseURL string) {
	if baseURL == "" {
		return
	}
	for id, c := range connectors {
		webhookURL := fmt.Sprintf("%s/webhooks/%s", baseURL, id)
		if err := c.RegisterWebhook(ctx, webhookURL); err != nil {
			log.Warn("register webhook", "source", id, "err", err)
		}
	}
}

// syncAll runs Sync on every connector, deduplicates cross-source events by UID,
// and publishes a change event with a fresh request ID.
func syncAll(ctx context.Context, log *slog.Logger, connectors map[string]connector.Connector, b *bus.Bus, db *store.DB) {
	changed := false
	for id, c := range connectors {
		if err := c.Sync(ctx); err != nil {
			log.Error("sync failed", "source", id, "err", err)
			continue
		}
		log.Info("synced", "source", id)
		changed = true
	}

	if changed {
		if n, err := db.DeduplicateByUID(ctx); err != nil {
			log.Warn("dedup failed", "err", err)
		} else if n > 0 {
			log.Info("deduped cross-source events", "removed", n)
		}

		b.Publish(bus.Message{
			Type:      bus.EventCalendarChanged,
			RequestID: newRequestID(),
		})
	}
}

// newRequestID returns a short random hex string for log correlation.
func newRequestID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("FOCACAL_DEBUG") == "true" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
