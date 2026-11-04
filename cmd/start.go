package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/Kenttleton/focacal/internal/auth/simple"
	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	caldavconn "github.com/Kenttleton/focacal/internal/connector/caldav"
	googleconn "github.com/Kenttleton/focacal/internal/connector/google"
	icsconn "github.com/Kenttleton/focacal/internal/connector/ics"
	outlookconn "github.com/Kenttleton/focacal/internal/connector/outlook"
	"github.com/Kenttleton/focacal/internal/emitter/caldav"
	"github.com/Kenttleton/focacal/internal/emitter/rest"
	"github.com/Kenttleton/focacal/internal/mcp"
	"github.com/Kenttleton/focacal/internal/store"
	"github.com/spf13/cobra"
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

	configDir, live, err := loadConfig(log)
	if err != nil {
		return err
	}
	// Consumers still read this one snapshot; they move to live in a later step.
	cfg := live.Current()
	log.Info("config loaded", "dir", configDir)
	configSvc, err := config.NewService(configDir, live)
	if err != nil {
		return fmt.Errorf("config service: %w", err)
	}

	// Ensure a recovery key exists for hard-reset of forgotten credentials.
	recoveryPath := "/data/recovery.key"
	if _, err := os.Stat(recoveryPath); os.IsNotExist(err) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		key := hex.EncodeToString(b)
		_ = os.WriteFile(recoveryPath, []byte(key), 0600)
		fmt.Printf("\n[focacal] Recovery key (store safely): %s\n\n", key)
	}

	// Open the SQLite store.
	db, err := store.Open("/data/focacal.db")
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	// Build the event bus (500ms debounce — collapses rapid webhook bursts).
	b := bus.New(500 * time.Millisecond)

	// Build connectors for each configured source. The registry is kept in
	// step with live config by reconcileConnectors (see OnChange below).
	reg := connector.NewRegistry()
	build := func(src config.SourceConfig, redirectBase string) (connector.Connector, error) {
		return buildConnector(src, db, redirectBase)
	}
	reconcileConnectors(reg, nil, live.Current(), build, log)

	// Build emitters.
	caldavAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.CalDAVPort)
	restAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.RESTPort)
	mcpAddr := fmt.Sprintf("%s:%d", cfg.Server.BindAddress, cfg.Server.MCPPort)

	health := rest.NewHealthState()
	caldavSrv := caldav.New(caldavAddr, log)
	restSrv := rest.New(reg, health, restAddr, log)
	// The resolved config directory holds the domain files and
	// credentials.yaml, whichever --config form was used.
	restSrv.SetConfigDir(configDir)
	restSrv.SetConfigService(configSvc)
	restSrv.SetRecoveryKeyPath(recoveryPath)
	mcpSrv := mcp.New(mcpAddr, log)

	// Apply OSS auth middleware to all servers (no-op when auth.enabled = false).
	credentialsPresent := func() bool {
		_, ok, err := config.LoadCredentials(configDir)
		return ok || err != nil // an unreadable file still means credentials were set up
	}
	authMW := simple.Middleware(cfg.GlobalConfig.Auth, credentialsPresent, log)
	caldavSrv.WrapHandler(authMW)
	restSrv.WrapHandler(authMW)
	mcpSrv.WrapHandler(authMW)
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
			for _, e := range []interface{ Notify(context.Context) error }{caldavSrv, restSrv, mcpSrv} {
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
	// Resolved per call so planner/sync callers always see the live values.
	ctx = config.WithUserConfigFunc(ctx, func() *config.UserConfig { return &live.Current().UserConfig })
	ctx = bus.WithDispatcher(ctx, b)

	// Background sync: initial sync then poll every 5 minutes. The connector
	// set and webhook base URL are re-read from live config on every tick.
	// Webhook registration is attempted on each cycle; RegisterWebhook is
	// idempotent and renews subscriptions only when they are about to expire.
	// server.webhooks_enabled is restart-required, so it is read once.
	webhooksEnabled := cfg.Server.WebhooksEnabled
	syncCycle := func() {
		if webhooksEnabled {
			registerWebhooks(ctx, log, reg.Snapshot(), live.Current().OAuth.RedirectBaseURL)
		}
		syncAll(ctx, log, reg, b)
	}

	// React to live config changes. Handlers must not call live.Reload (it
	// would deadlock); live.Current is fine.
	live.OnChange(func(old, cur *config.Config) {
		added := reconcileConnectors(reg, old, cur, build, log)
		if len(added) > 0 {
			// Off the notification goroutine: syncing can take a while.
			go func() {
				if webhooksEnabled {
					snap := reg.Snapshot()
					only := make(map[string]connector.Connector, len(added))
					for _, id := range added {
						if c, ok := snap[id]; ok {
							only[id] = c
						}
					}
					registerWebhooks(ctx, log, only, live.Current().OAuth.RedirectBaseURL)
				}
				syncOnly(ctx, log, reg, added, b)
			}()
		}
		if plannerInputsChanged(old, cur) {
			b.Publish(bus.Message{Type: bus.EventReplanRequested, RequestID: newRequestID()})
		}
	})

	// Registered before the watcher and the servers start so no reload is missed.
	go func() { _ = live.Watch(ctx) }()

	// Start all servers concurrently.
	errCh := make(chan error, 4)
	go func() { errCh <- caldavSrv.Start(ctx) }()
	go func() { errCh <- restSrv.Start(ctx) }()
	go func() { errCh <- mcpSrv.Start(ctx) }()

	go func() {
		syncCycle()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncCycle()
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

// syncAll syncs every connector currently in the registry.
func syncAll(ctx context.Context, log *slog.Logger, reg *connector.Registry, b *bus.Bus) {
	syncConnectors(ctx, log, reg, reg.Snapshot(), b)
}

// syncOnly syncs just the named sources (those still registered).
func syncOnly(ctx context.Context, log *slog.Logger, reg *connector.Registry, ids []string, b *bus.Bus) {
	snap := reg.Snapshot()
	only := make(map[string]connector.Connector, len(ids))
	for _, id := range ids {
		if c, ok := snap[id]; ok {
			only[id] = c
		}
	}
	syncConnectors(ctx, log, reg, only, b)
}

// syncConnectors runs Sync on the given connectors (a snapshot, so registry
// changes mid-sync cannot affect iteration) and publishes one change event
// with a fresh request ID if any succeeded. A connector that was removed or
// replaced while its sync was in flight is allowed to finish, but its result
// is discarded: no log of success and no change event on its behalf.
func syncConnectors(ctx context.Context, log *slog.Logger, reg *connector.Registry, conns map[string]connector.Connector, b *bus.Bus) {
	changed := false
	for id, c := range conns {
		err := syncInstance(ctx, c)
		if cur, ok := reg.Get(id); !ok || cur != c {
			log.Info("sync result discarded, source removed or replaced during sync", "source", id)
			continue
		}
		if err != nil {
			log.Error("sync failed", "source", id, "err", err)
			continue
		}
		log.Info("synced", "source", id)
		changed = true
	}

	if changed {
		b.Publish(bus.Message{
			Type:      bus.EventCalendarChanged,
			RequestID: newRequestID(),
		})
	}
}

// managed wraps a connector instance with what the sync loop needs: a mutex
// that serialises Sync calls on this instance, and a context cancelled when
// reconcile drops or replaces the instance.
type managed struct {
	connector.Connector
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func newManaged(c connector.Connector) *managed {
	ctx, cancel := context.WithCancel(context.Background())
	return &managed{Connector: c, ctx: ctx, cancel: cancel}
}

// syncInstance runs Sync on c. Concurrent requests for the same instance wait
// their turn (so a requested sync always runs after the one in flight). The
// Sync context is cancelled if the instance is dropped or replaced meanwhile.
func syncInstance(ctx context.Context, c connector.Connector) error {
	m, ok := c.(*managed)
	if !ok {
		return c.Sync(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	return m.Connector.Sync(sctx)
}

// buildFunc constructs a connector for one source.
type buildFunc func(src config.SourceConfig, redirectBase string) (connector.Connector, error)

// buildConnector is the production buildFunc: one connector per source type.
func buildConnector(src config.SourceConfig, db *store.DB, redirectBase string) (connector.Connector, error) {
	switch src.Type {
	case "google":
		return googleconn.New(src, db, redirectBase)
	case "outlook":
		return outlookconn.New(src, db, redirectBase, "common")
	case "caldav":
		return caldavconn.New(src, db)
	case "ics":
		return icsconn.New(src, db)
	default:
		return nil, fmt.Errorf("unknown source type %q", src.Type)
	}
}

// usesRedirectBase reports whether connectors of this type are built with the
// OAuth redirect base URL.
func usesRedirectBase(sourceType string) bool {
	return sourceType == "google" || sourceType == "outlook"
}

// reconcileConnectors brings reg in line with cur.Sources. Sources that are
// new, or whose SourceConfig differs from old (or, for OAuth source types,
// whose redirect base URL changed), are built afresh; untouched sources keep
// their existing instance; sources no longer configured are dropped. A source
// that fails to build is logged and left out of the registry (a failed rebuild
// drops the stale connector rather than running it on outdated settings); the
// others are unaffected. old may be nil (initial startup). It returns the ids
// that were added or rebuilt, in config order.
func reconcileConnectors(reg *connector.Registry, old, cur *config.Config, build buildFunc, log *slog.Logger) (added []string) {
	existing := reg.Snapshot()
	prev := map[string]config.SourceConfig{}
	oldRedirect := ""
	if old != nil {
		for _, s := range old.Sources {
			prev[s.ID] = s
		}
		oldRedirect = old.OAuth.RedirectBaseURL
	}
	redirectBase := cur.OAuth.RedirectBaseURL

	next := make(map[string]connector.Connector, len(cur.Sources))
	for _, src := range cur.Sources {
		if c, ok := existing[src.ID]; ok {
			if p, known := prev[src.ID]; known && reflect.DeepEqual(p, src) &&
				!(usesRedirectBase(src.Type) && oldRedirect != redirectBase) {
				next[src.ID] = c
				continue
			}
		}
		raw, err := build(src, redirectBase)
		if err != nil {
			// Constructor errors may embed URLs, paths or credentials: log the source
			// only, never the error text.
			log.Warn("init connector failed; check this source's settings", "type", src.Type, "id", src.ID)
			continue
		}
		next[src.ID] = newManaged(raw)
		added = append(added, src.ID)
	}
	reg.Replace(next)
	// Cancel in-flight syncs of every instance that was dropped or replaced so
	// it stops writing to the store.
	for id, c := range existing {
		if next[id] != c {
			if m, ok := c.(*managed); ok {
				m.cancel()
			}
		}
	}
	return added
}

// plannerInputsChanged reports whether anything the planner or the source set
// depends on differs between old and cur.
func plannerInputsChanged(old, cur *config.Config) bool {
	return !reflect.DeepEqual(old.Sources, cur.Sources) ||
		!reflect.DeepEqual(old.Destinations, cur.Destinations) ||
		!reflect.DeepEqual(old.WorkingHours, cur.WorkingHours) ||
		!reflect.DeepEqual(old.Lunch, cur.Lunch) ||
		!reflect.DeepEqual(old.Focus, cur.Focus) ||
		!reflect.DeepEqual(old.Agent, cur.Agent)
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
