package rest

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/Kenttleton/focacal/internal/auth/simple"
	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/conflict"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/connector"
	caldavemitter "github.com/Kenttleton/focacal/internal/emitter/caldav"
	"github.com/Kenttleton/focacal/internal/store"
)

// HealthState tracks runtime metrics that GET /health exposes.
// It is updated by the replan cycle in cmd/start.go after each successful run.
type HealthState struct {
	mu         sync.RWMutex
	lastReplan time.Time
}

// NewHealthState returns an initialised HealthState.
func NewHealthState() *HealthState { return &HealthState{} }

// SetLastReplan records the time of the most recent successful replan.
func (h *HealthState) SetLastReplan(t time.Time) {
	h.mu.Lock()
	h.lastReplan = t
	h.mu.Unlock()
}

// LastReplan returns the time of the most recent successful replan (zero if none yet).
func (h *HealthState) LastReplan() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastReplan
}

//go:embed ui.html
var uiHTML []byte

//go:embed openapi.json
var openapiJSON []byte

// Server serves the focacal REST API consumed by Raycast and other clients.
type Server struct {
	connectors map[string]connector.Connector
	health     *HealthState
	addr       string
	srv        *http.Server
	log        *slog.Logger
	// configDir is the config directory: the domain files and
	// credentials.yaml. Set by SetConfigDir; empty means the working directory.
	configDir string
	// configSvc reads and patches the domain files (nil in auth-only tests).
	configSvc *config.Service
	// recoveryKeyPath is the file /auth/recover checks the key against.
	recoveryKeyPath string
}

// DefaultRecoveryKeyPath is where the recovery key lives in the container.
const DefaultRecoveryKeyPath = "/data/recovery.key"

// SetConfigDir sets the config directory (domain files and
// credentials.yaml). cmd/start passes the directory resolved from --config or
// the search path; there is no implicit default.
func (s *Server) SetConfigDir(dir string) { s.configDir = dir }

// SetConfigService sets the Service over the config directory, built from the
// env-free startup snapshot so pending_restart is accurate.
func (s *Server) SetConfigService(svc *config.Service) { s.configSvc = svc }

// SetRecoveryKeyPath overrides DefaultRecoveryKeyPath (tests use a temp file).
func (s *Server) SetRecoveryKeyPath(p string) { s.recoveryKeyPath = p }

// New creates a REST API server.
func New(connectors map[string]connector.Connector, health *HealthState, addr string, log *slog.Logger) *Server {
	s := &Server{connectors: connectors, health: health, addr: addr, log: log, recoveryKeyPath: DefaultRecoveryKeyPath}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(corsMiddleware)

	r.Get("/health", s.handleHealth)
	r.Post("/auth/setup", s.handleAuthSetup)
	r.Post("/auth/login", s.handleAuthLogin)
	r.Post("/auth/reset", s.handleAuthReset)
	r.Post("/auth/recover", s.handleAuthRecover)
	r.Get("/today", s.handleToday)
	r.Get("/now", s.handleNow)
	r.Post("/replan", s.handleReplan)
	r.Get("/blocks", s.handleBlocks)
	r.Get("/events", s.handleEvents)
	r.Get("/conflicts", s.handleConflicts)
	r.Post("/conflicts/{key}/dismiss", s.handleDismissConflict)
	r.Get("/sources", s.handleSources)
	r.Get("/destinations", s.handleDestinations)
	r.Put("/config/sources", s.handlePutSources)
	r.Put("/config/destinations", s.handlePutDestinations)
	// Block mutations from the Web UI.
	r.Post("/blocks", s.handleCreateBlock)
	r.Patch("/blocks/{id}", s.handleUpdateBlock)
	r.Delete("/blocks/{id}", s.handleDeleteBlock)

	// Config read/write for the settings panel.
	r.Get("/config", s.handleGetConfig)
	r.Patch("/config", s.handleUpdateConfig)

	// Webcal / ICS feed — next 14 days of events and blocks.
	// Subscribe in any calendar app with: webcal://host:5233/calendar.ics
	r.Get("/calendar.ics", s.handleCalendarICS)

	// OpenAPI spec — import into ChatGPT Actions, Copilot, or any agent framework.
	r.Get("/openapi.json", s.handleOpenAPI)

	// Web UI — single self-contained HTML page.
	r.Get("/ui", s.handleUI)

	// Webhook endpoints: GET handles the Outlook subscription validation handshake;
	// POST handles push notifications from both Google and Outlook.
	r.Get("/webhooks/{sourceID}", s.handleWebhookValidation)
	r.Post("/webhooks/{sourceID}", s.handleWebhookNotification)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return s
}

// handleOpenAPI serves the embedded OpenAPI 3.1 spec.
// Import at https://platform.openai.com/playground or any agent that reads OpenAPI.
func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(openapiJSON)
}

// handleUI serves the embedded single-page web application.
func (s *Server) handleUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(uiHTML)
}

// handleCalendarICS serves a webcal-compatible ICS feed covering the next 14 days.
// Subscribe in any calendar app with: webcal://host:5233/calendar.ics
func (s *Server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	from := now
	to := now.AddDate(0, 0, 14)

	db := store.FromContext(ctx)
	events, err := db.ListEvents(ctx, from, to)
	if err != nil {
		s.log.Error("list events for ICS feed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	blocks, err := db.ListBlocks(ctx, from, to)
	if err != nil {
		s.log.Error("list blocks for ICS feed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="focacal.ics"`)
	fmt.Fprint(w, caldavemitter.BuildICS(events, blocks))
}

// handleCreateBlock creates a user-placed block (always locked).
func (s *Server) handleCreateBlock(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		Start    string `json:"start"`
		End      string `json:"end"`
		Priority int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	start, err := time.Parse(time.RFC3339, p.Start)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start: "+err.Error())
		return
	}
	end, err := time.Parse(time.RFC3339, p.End)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid end: "+err.Error())
		return
	}
	btype := store.BlockType(p.Type)
	if btype == "" {
		btype = store.BlockFocus
	}
	title := p.Title
	if title == "" {
		title = "Focus block"
	}
	b := store.NewBlock(btype, title, start, end, p.Priority)
	b.Locked = true // user-created blocks are always locked
	if err := store.FromContext(r.Context()).UpsertBlock(r.Context(), b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, blockToDTO(b, time.Now()))
}

// handleUpdateBlock patches a block's end time, locked status, or title.
func (s *Server) handleUpdateBlock(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(chi.URLParam(r, "id"), "focacal-block-")
	db := store.FromContext(r.Context())
	b, err := db.GetBlock(r.Context(), id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "block not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var patch struct {
		End    *string `json:"end"`
		Locked *bool   `json:"locked"`
		Title  *string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if patch.End != nil {
		end, err := time.Parse(time.RFC3339, *patch.End)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid end: "+err.Error())
			return
		}
		b.End = end
	}
	if patch.Locked != nil {
		b.Locked = *patch.Locked
	}
	if patch.Title != nil {
		b.Title = *patch.Title
	}
	if err := db.UpsertBlock(r.Context(), b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, blockToDTO(b, time.Now()))
}

// handleDeleteBlock removes a block by ID.
func (s *Server) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(chi.URLParam(r, "id"), "focacal-block-")
	if err := store.FromContext(r.Context()).DeleteBlock(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetConfig returns the subset of config that the settings panel can edit.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	uc := config.UserConfigFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"working_hours": map[string]any{
			"start":    uc.WorkingHours.Start,
			"end":      uc.WorkingHours.End,
			"timezone": uc.WorkingHours.Timezone,
		},
		"display_timezone": uc.DisplayTimezone,
		"lunch": map[string]any{
			"preferred":        uc.Lunch.Preferred,
			"window_start":     uc.Lunch.WindowStart,
			"window_end":       uc.Lunch.WindowEnd,
			"duration_minutes": uc.Lunch.DurationMinutes,
		},
		"focus": map[string]any{
			"style":                  uc.Focus.Style,
			"min_block_minutes":      uc.Focus.MinBlockMinutes,
			"max_block_minutes":      uc.Focus.MaxBlockMinutes,
			"buffer_around_meetings": uc.Focus.BufferAroundMeetings,
		},
	})
}

// handleUpdateConfig writes changed settings back to planner.yaml and
// updates the in-memory config so GET /config reflects the new values immediately.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var u struct {
		WorkingHours *struct {
			Start    string `json:"start"`
			End      string `json:"end"`
			Timezone string `json:"timezone"`
		} `json:"working_hours"`
		// DisplayTimezone is a pointer so an explicit "" (reset to detect) can
		// be told apart from "not provided".
		DisplayTimezone *string `json:"display_timezone"`
		Lunch           *struct {
			Preferred       string `json:"preferred"`
			WindowStart     string `json:"window_start"`
			WindowEnd       string `json:"window_end"`
			DurationMinutes int    `json:"duration_minutes"`
		} `json:"lunch"`
		Focus *struct {
			Style                string `json:"style"`
			MinBlockMinutes      int    `json:"min_block_minutes"`
			MaxBlockMinutes      int    `json:"max_block_minutes"`
			BufferAroundMeetings int    `json:"buffer_around_meetings"`
		} `json:"focus"`
	}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	uc := config.UserConfigFromContext(r.Context())

	if wh := u.WorkingHours; wh != nil {
		if wh.Start != "" {
			uc.WorkingHours.Start = wh.Start
		}
		if wh.End != "" {
			uc.WorkingHours.End = wh.End
		}
		if wh.Timezone != "" {
			uc.WorkingHours.Timezone = wh.Timezone
		}
	}
	if u.DisplayTimezone != nil {
		tz := *u.DisplayTimezone
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				writeError(w, http.StatusBadRequest, "unknown timezone: "+tz)
				return
			}
		}
		uc.DisplayTimezone = tz
	}
	if l := u.Lunch; l != nil {
		if l.Preferred != "" {
			uc.Lunch.Preferred = l.Preferred
		}
		if l.WindowStart != "" {
			uc.Lunch.WindowStart = l.WindowStart
		}
		if l.WindowEnd != "" {
			uc.Lunch.WindowEnd = l.WindowEnd
		}
		if l.DurationMinutes > 0 {
			uc.Lunch.DurationMinutes = l.DurationMinutes
		}
	}
	if f := u.Focus; f != nil {
		if f.Style != "" {
			uc.Focus.Style = f.Style
		}
		if f.MinBlockMinutes > 0 {
			uc.Focus.MinBlockMinutes = f.MinBlockMinutes
		}
		if f.MaxBlockMinutes > 0 {
			uc.Focus.MaxBlockMinutes = f.MaxBlockMinutes
		}
		if f.BufferAroundMeetings >= 0 {
			uc.Focus.BufferAroundMeetings = f.BufferAroundMeetings
		}
	}

	// Interim (replaced by the domain handlers): these keys live in planner.yaml.
	cfgPath := filepath.Join(s.configDir, config.DomainPlanner.File())
	// Write only the sections this request touched: WriteSections re-encodes each
	// one, which drops its inline comments, so untouched sections are left alone.
	sections := map[string]any{}
	if u.WorkingHours != nil {
		sections["working_hours"] = map[string]any{
			"start":    uc.WorkingHours.Start,
			"end":      uc.WorkingHours.End,
			"timezone": uc.WorkingHours.Timezone,
		}
	}
	if u.DisplayTimezone != nil {
		sections["display_timezone"] = uc.DisplayTimezone
	}
	if u.Lunch != nil {
		sections["lunch"] = map[string]any{
			"preferred":        uc.Lunch.Preferred,
			"window_start":     uc.Lunch.WindowStart,
			"window_end":       uc.Lunch.WindowEnd,
			"duration_minutes": uc.Lunch.DurationMinutes,
		}
	}
	if u.Focus != nil {
		sections["focus"] = map[string]any{
			"style":                  uc.Focus.Style,
			"min_block_minutes":      uc.Focus.MinBlockMinutes,
			"max_block_minutes":      uc.Focus.MaxBlockMinutes,
			"buffer_around_meetings": uc.Focus.BufferAroundMeetings,
		}
	}
	if err := config.WriteSections(cfgPath, sections); err != nil {
		s.log.Warn("write config", "err", err)
		writeError(w, http.StatusInternalServerError, "could not write planner.yaml: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "restart_required": true})
}

// handleWebhookValidation responds to the Outlook/Graph subscription validation
// handshake, which sends GET ?validationToken=<token> to confirm the endpoint.
func (s *Server) handleWebhookValidation(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("validationToken")
	if token == "" {
		http.Error(w, "missing validationToken", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, token)
}

// handleWebhookNotification receives push notifications from Google or Outlook,
// verifies the payload against the matching connector, and triggers a sync.
func (s *Server) handleWebhookNotification(w http.ResponseWriter, r *http.Request) {
	sourceID := chi.URLParam(r, "sourceID")

	conn, ok := s.connectors[sourceID]
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}

	payload, _ := io.ReadAll(r.Body)

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}

	if _, err := conn.ValidateWebhook(r.Context(), payload, headers); err != nil {
		s.log.Warn("webhook validation failed", "source", sourceID, "err", err)
		http.Error(w, "invalid webhook", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	bus.FromContext(r.Context()).Publish(bus.Message{
		Type:      bus.EventCalendarChanged,
		SourceID:  sourceID,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

// WrapHandler wraps the server's HTTP handler with the given middleware.
// Call before Start() — for example to apply auth middleware.
func (s *Server) WrapHandler(mw func(http.Handler) http.Handler) {
	s.srv.Handler = mw(s.srv.Handler)
}

// Name implements emitter.Emitter.
func (s *Server) Name() string { return "rest" }

// Notify implements emitter.Emitter — REST is pull-based, nothing to push.
func (s *Server) Notify(_ context.Context) error { return nil }

// Start begins listening. Blocks until ctx is cancelled.
// ctx must carry the injected Store, Dispatcher, and UserConfig so that
// all HTTP request contexts inherit those values via BaseContext.
func (s *Server) Start(ctx context.Context) error {
	// Use a non-cancellable copy so that graceful shutdown via srv.Shutdown
	// can drain in-flight requests instead of having their contexts killed immediately.
	s.srv.BaseContext = func(net.Listener) context.Context {
		return context.WithoutCancel(ctx)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("rest listen %s: %w", s.addr, err)
	}
	s.log.Info("REST API listening", "addr", s.addr)

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// handleHealth returns service status including last successful replan time,
// per-source last-sync times, and webhook expiry times.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	sources, _ := store.FromContext(r.Context()).ListSyncStates(r.Context())

	lastReplan := s.health.LastReplan()
	var lastReplanStr string
	if !lastReplan.IsZero() {
		lastReplanStr = lastReplan.UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":                 "ok",
		"time":                   time.Now().UTC().Format(time.RFC3339),
		"last_successful_replan": lastReplanStr,
		"sources":                sources,
	})
}

// EventDTO is the unified event shape returned by the REST API.
type EventDTO struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`   // "real" | "focus" | "lunch" | "break"
	Source         string    `json:"source"` // source_id or "focacal"
	Title          string    `json:"title"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	AllDay         bool      `json:"all_day,omitempty"`
	ConferenceURL  string    `json:"conference_url,omitempty"`
	Locked         bool      `json:"locked,omitempty"`
	IsNow          bool      `json:"is_now"`
	MinutesUntil   int       `json:"minutes_until"` // negative = already started
	Kind           string    `json:"kind,omitempty"`
	Color          string    `json:"color,omitempty"`
	CalendarID     string    `json:"calendar_id,omitempty"`
	ResponseStatus string    `json:"response_status,omitempty"`
	ShowAs         string    `json:"show_as,omitempty"`
	LinkedIDs      []string  `json:"linked_ids,omitempty"`
	HeuristicLink  bool      `json:"heuristic_link,omitempty"`
	ConflictKeys   []string  `json:"conflict_keys,omitempty"`
}

// handleToday returns all events and blocks for today, sorted by start time.
func (s *Server) handleToday(w http.ResponseWriter, r *http.Request) {
	loc := locationFromQuery(r)
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	db := store.FromContext(r.Context())
	events, err := db.ListEvents(r.Context(), dayStart, dayEnd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list events: "+err.Error())
		return
	}
	blocks, err := db.ListBlocks(r.Context(), dayStart, dayEnd)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list blocks: "+err.Error())
		return
	}

	dtos := make([]EventDTO, 0, len(events)+len(blocks))
	for _, e := range events {
		dtos = append(dtos, EventToDTO(e, now))
	}
	for _, b := range blocks {
		dtos = append(dtos, blockToDTO(b, now))
	}

	// Sort by start time.
	sortDTOs(dtos)
	writeJSON(w, http.StatusOK, dtos)
}

// handleNow returns the currently active event/block or the next upcoming one.
func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	loc := locationFromQuery(r)
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.Add(24 * time.Hour)

	db := store.FromContext(r.Context())
	events, _ := db.ListEvents(r.Context(), dayStart, dayEnd)
	blocks, _ := db.ListBlocks(r.Context(), dayStart, dayEnd)

	dtos := make([]EventDTO, 0, len(events)+len(blocks))
	for _, e := range events {
		dtos = append(dtos, EventToDTO(e, now))
	}
	for _, b := range blocks {
		dtos = append(dtos, blockToDTO(b, now))
	}
	sortDTOs(dtos)

	// Find the active item first, then the next upcoming.
	for _, d := range dtos {
		if d.IsNow {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	for _, d := range dtos {
		if d.MinutesUntil > 0 {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}

	writeJSON(w, http.StatusOK, nil)
}

// handleReplan triggers a manual replan via the event bus.
func (s *Server) handleReplan(w http.ResponseWriter, r *http.Request) {
	bus.FromContext(r.Context()).PublishImmediate(bus.Message{
		Type:      bus.EventReplanRequested,
		RequestID: middleware.GetReqID(r.Context()),
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "replan triggered"})
}

// handleBlocks returns AI-planned blocks for a date range.
func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	from, to := dateRangeFromQuery(r)
	blocks, err := store.FromContext(r.Context()).ListBlocks(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	dtos := make([]EventDTO, 0, len(blocks))
	for _, b := range blocks {
		dtos = append(dtos, blockToDTO(b, now))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// handleEvents returns real calendar events for a date range.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	from, to := dateRangeFromQuery(r)
	events, err := store.FromContext(r.Context()).ListEvents(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	dtos := make([]EventDTO, 0, len(events))
	for _, e := range events {
		dtos = append(dtos, EventToDTO(e, now))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func EventToDTO(e *store.Event, now time.Time) EventDTO {
	return EventDTO{
		ID:            e.ID,
		Type:          "real",
		Source:        e.SourceID,
		Title:         e.Title,
		Start:         e.Start,
		End:           e.End,
		AllDay:        e.AllDay,
		ConferenceURL: e.ConferenceURL,
		IsNow:         !now.Before(e.Start) && now.Before(e.End),
		MinutesUntil:  int(e.Start.Sub(now).Minutes()),
	}
}

func blockToDTO(b *store.Block, now time.Time) EventDTO {
	return EventDTO{
		ID:           "focacal-block-" + b.ID,
		Type:         string(b.Type),
		Source:       "focacal",
		Title:        b.Title,
		Start:        b.Start,
		End:          b.End,
		Locked:       b.Locked,
		IsNow:        !now.Before(b.Start) && now.Before(b.End),
		MinutesUntil: int(b.Start.Sub(now).Minutes()),
	}
}

func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	from, to := parseRange(r)
	confs, err := conflict.Fetch(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(confs))
	for _, c := range confs {
		out = append(out, map[string]any{
			"key":       c.Key,
			"tentative": c.Tentative,
			"dismissed": c.Dismissed,
			"a":         EventToDTO(c.A.Canonical, time.Now()),
			"b":         EventToDTO(c.B.Canonical, time.Now()),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDismissConflict(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if err := store.FromContext(r.Context()).DismissConflict(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	uc := config.UserConfigFromContext(r.Context())
	out := make([]map[string]any, 0, len(uc.Sources))
	for _, src := range uc.Sources {
		out = append(out, map[string]any{
			"id":           src.ID,
			"label":        src.Label,
			"type":         src.Type,
			"calendar_ids": src.EffectiveCalendarIDs(),
			"conflicts": map[string]bool{
				"declined":  src.Conflicts.CountsDeclined(),
				"tentative": src.Conflicts.CountsTentative(),
				"free":      src.Conflicts.CountsFree(),
				"all_day":   src.Conflicts.CountsAllDay(),
			},
			"ics_url_env": src.ICSURLEnv,
			"has_secret":  src.Password != "" || src.ICSURL != "",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDestinations(w http.ResponseWriter, r *http.Request) {
	uc := config.UserConfigFromContext(r.Context())
	writeJSON(w, http.StatusOK, uc.Destinations)
}

func (s *Server) handlePutSources(w http.ResponseWriter, r *http.Request) {
	var list []config.SourceConfig
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uc := config.UserConfigFromContext(r.Context())
	// sentinel handling
	for i := range list {
		if list[i].Password == "********" {
			// find existing
			for _, old := range uc.Sources {
				if old.ID == list[i].ID {
					list[i].Password = old.Password
					list[i].ICSURL = old.ICSURL
				}
			}
		}
		if list[i].ICSURL == "********" {
			for _, old := range uc.Sources {
				if old.ID == list[i].ID {
					list[i].ICSURL = old.ICSURL
				}
			}
		}
	}
	candidate := *uc
	candidate.Sources = list
	cfg := &config.Config{UserConfig: candidate}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Interim (replaced by the domain handlers): sources and destinations live in calendars.yaml.
	path := filepath.Join(s.configDir, config.DomainCalendars.File())
	if err := config.WriteSections(path, map[string]any{"sources": list}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "restart_required": true})
}

func (s *Server) handlePutDestinations(w http.ResponseWriter, r *http.Request) {
	var list []config.DestinationConfig
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uc := config.UserConfigFromContext(r.Context())
	candidate := *uc
	candidate.Destinations = list
	cfg := &config.Config{UserConfig: candidate}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Interim (replaced by the domain handlers): sources and destinations live in calendars.yaml.
	path := filepath.Join(s.configDir, config.DomainCalendars.File())
	if err := config.WriteSections(path, map[string]any{"destinations": list}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "restart_required": true})
}

func parseRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now()
	from := now.AddDate(0, 0, -7)
	to := now.AddDate(0, 0, 14)
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	return from, to
}

// Auth handlers read and write only credentials.yaml in s.configDir; they
// never touch the domain files (config.yaml, planner.yaml, calendars.yaml).

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var u struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.Username == "" || u.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if _, ok, err := config.LoadCredentials(s.configDir); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if ok {
		writeError(w, http.StatusForbidden, "already configured")
		return
	}
	h, err := simple.HashPassword(u.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := config.SetupCredentials(s.configDir, config.Credentials{Username: u.Username, PasswordHash: h}); err != nil {
		if errors.Is(err, config.ErrAlreadyConfigured) {
			writeError(w, http.StatusForbidden, "already configured")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// New credentials: tokens from any earlier (deleted) credentials die.
	simple.RevokeAll()
	writeJSON(w, http.StatusOK, map[string]string{"token": issueToken()})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var u struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Capture the revocation generation before reading credentials: if a
	// reset/recover/setup replaces them (and calls RevokeAll) while bcrypt
	// runs, the token below is refused instead of outliving the old hash.
	gen := simple.Generation()
	c, ok, err := config.LoadCredentials(s.configDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	userOK := ok && subtle.ConstantTimeCompare([]byte(c.Username), []byte(u.Username)) == 1
	hash := c.PasswordHash
	if !userOK {
		hash = dummyHash() // always run bcrypt: no username timing oracle
	}
	pwOK := simple.VerifyPassword(hash, u.Password)
	if !userOK || !pwOK {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	loginVerifiedHook()
	tok := simple.GenerateKey()
	if !simple.StoreTokenAt(tok, gen) {
		writeError(w, http.StatusUnauthorized, "credentials changed; log in again")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

// dummyHash is a bcrypt hash of a random secret, verified against when the
// username is unknown so failed logins cost the same either way.
var dummyHash = sync.OnceValue(func() string {
	h, err := simple.HashPassword(simple.GenerateKey())
	if err != nil {
		panic("auth: cannot build dummy hash: " + err.Error())
	}
	return h
})

// loginVerifiedHook runs in login after the password is verified and before
// the token is registered. Test seam for forcing a reset/recover into that
// window; a no-op in production.
var loginVerifiedHook = func() {}

func (s *Server) handleAuthReset(w http.ResponseWriter, r *http.Request) {
	var u struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new_password is required")
		return
	}
	c, ok, err := config.LoadCredentials(s.configDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "not configured; use /auth/setup")
		return
	}
	h, err := simple.HashPassword(u.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.PasswordHash = h
	if err := config.SaveCredentials(s.configDir, c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	simple.RevokeAll()
	writeJSON(w, http.StatusOK, map[string]string{"token": issueToken()})
}

func (s *Server) handleAuthRecover(w http.ResponseWriter, r *http.Request) {
	var u struct {
		RecoveryKey string `json:"recovery_key"`
		Username    string `json:"username"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(s.recoveryKeyPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "no recovery key on server")
		return
	}
	key := strings.TrimSpace(string(data))
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(u.RecoveryKey)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid recovery key")
		return
	}
	if u.Username == "" || u.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "username and new_password are required")
		return
	}
	h, err := simple.HashPassword(u.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := config.SaveCredentials(s.configDir, config.Credentials{Username: u.Username, PasswordHash: h}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	simple.RevokeAll()
	writeJSON(w, http.StatusOK, map[string]string{"token": issueToken()})
}

// issueToken generates and registers a new session token.
func issueToken() string {
	tok := simple.GenerateKey()
	simple.StoreToken(tok)
	return tok
}

func sortDTOs(dtos []EventDTO) {
	for i := 1; i < len(dtos); i++ {
		for j := i; j > 0 && dtos[j].Start.Before(dtos[j-1].Start); j-- {
			dtos[j], dtos[j-1] = dtos[j-1], dtos[j]
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func locationFromQuery(r *http.Request) *time.Location {
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func dateRangeFromQuery(r *http.Request) (from, to time.Time) {
	now := time.Now()
	from = now.AddDate(0, 0, -1)
	to = now.AddDate(0, 0, 8)

	if f := r.URL.Query().Get("from"); f != "" {
		if t, err := time.Parse("2006-01-02", f); err == nil {
			from = t
		}
	}
	if t := r.URL.Query().Get("to"); t != "" {
		if pt, err := time.Parse("2006-01-02", t); err == nil {
			to = pt
		}
	}
	return
}
