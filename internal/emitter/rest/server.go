package rest

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
	"github.com/you/focacal/internal/bus"
	"github.com/you/focacal/internal/config"
	"github.com/you/focacal/internal/connector"
	caldavemitter "github.com/you/focacal/internal/emitter/caldav"
	"github.com/you/focacal/internal/store"
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
}

// New creates a REST API server.
func New(connectors map[string]connector.Connector, health *HealthState, addr string, log *slog.Logger) *Server {
	s := &Server{connectors: connectors, health: health, addr: addr, log: log}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(corsMiddleware)

	r.Get("/health", s.handleHealth)
	r.Get("/today", s.handleToday)
	r.Get("/now", s.handleNow)
	r.Post("/replan", s.handleReplan)
	r.Get("/blocks", s.handleBlocks)
	r.Get("/events", s.handleEvents)

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

// handleUpdateConfig writes changed settings back to config.yaml via Viper and
// updates the in-memory config so GET /config reflects the new values immediately.
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var u struct {
		WorkingHours *struct {
			Start    string `json:"start"`
			End      string `json:"end"`
			Timezone string `json:"timezone"`
		} `json:"working_hours"`
		Lunch *struct {
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
			viper.Set("working_hours.start", wh.Start)
			uc.WorkingHours.Start = wh.Start
		}
		if wh.End != "" {
			viper.Set("working_hours.end", wh.End)
			uc.WorkingHours.End = wh.End
		}
		if wh.Timezone != "" {
			viper.Set("working_hours.timezone", wh.Timezone)
			uc.WorkingHours.Timezone = wh.Timezone
		}
	}
	if l := u.Lunch; l != nil {
		if l.Preferred != "" {
			viper.Set("lunch.preferred", l.Preferred)
			uc.Lunch.Preferred = l.Preferred
		}
		if l.WindowStart != "" {
			viper.Set("lunch.window_start", l.WindowStart)
			uc.Lunch.WindowStart = l.WindowStart
		}
		if l.WindowEnd != "" {
			viper.Set("lunch.window_end", l.WindowEnd)
			uc.Lunch.WindowEnd = l.WindowEnd
		}
		if l.DurationMinutes > 0 {
			viper.Set("lunch.duration_minutes", l.DurationMinutes)
			uc.Lunch.DurationMinutes = l.DurationMinutes
		}
	}
	if f := u.Focus; f != nil {
		if f.Style != "" {
			viper.Set("focus.style", f.Style)
			uc.Focus.Style = f.Style
		}
		if f.MinBlockMinutes > 0 {
			viper.Set("focus.min_block_minutes", f.MinBlockMinutes)
			uc.Focus.MinBlockMinutes = f.MinBlockMinutes
		}
		if f.MaxBlockMinutes > 0 {
			viper.Set("focus.max_block_minutes", f.MaxBlockMinutes)
			uc.Focus.MaxBlockMinutes = f.MaxBlockMinutes
		}
		if f.BufferAroundMeetings >= 0 {
			viper.Set("focus.buffer_around_meetings", f.BufferAroundMeetings)
			uc.Focus.BufferAroundMeetings = f.BufferAroundMeetings
		}
	}

	if err := viper.WriteConfig(); err != nil {
		s.log.Warn("write config", "err", err)
		writeError(w, http.StatusInternalServerError, "could not write config.yaml: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
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
	ID            string    `json:"id"`
	Type          string    `json:"type"`   // "real" | "focus" | "lunch" | "break"
	Source        string    `json:"source"` // source_id or "focacal"
	Title         string    `json:"title"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	AllDay        bool      `json:"all_day,omitempty"`
	ConferenceURL string    `json:"conference_url,omitempty"`
	Locked        bool      `json:"locked,omitempty"`
	IsNow         bool      `json:"is_now"`
	MinutesUntil  int       `json:"minutes_until"` // negative = already started
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
		dtos = append(dtos, eventToDTO(e, now))
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
		dtos = append(dtos, eventToDTO(e, now))
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
		dtos = append(dtos, eventToDTO(e, now))
	}
	writeJSON(w, http.StatusOK, dtos)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func eventToDTO(e *store.Event, now time.Time) EventDTO {
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
