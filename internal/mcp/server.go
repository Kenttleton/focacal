package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/Kenttleton/focacal/internal/bus"
	"github.com/Kenttleton/focacal/internal/config"
	"github.com/Kenttleton/focacal/internal/store"
)

// ── MCP Protocol Types ────────────────────────────────────────────────────────

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *mcpError `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ── Server ────────────────────────────────────────────────────────────────────

// Server implements an MCP server using streamable HTTP transport.
// It exposes focacal's planning primitives as tools that the Ollama agent
// (and any external MCP client) can call.
type Server struct {
	addr string
	srv  *http.Server
	log  *slog.Logger
}

// New creates an MCP server.
func New(addr string, log *slog.Logger) *Server {
	s := &Server{addr: addr, log: log}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Post("/mcp", s.handleMCP)
	r.Get("/mcp", s.handleMCPGet) // SSE keepalive / capability discovery

	s.srv = &http.Server{Addr: addr, Handler: r,
		ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	return s
}

// WrapHandler wraps the server's HTTP handler with the given middleware.
// Call before Start() — for example to apply auth middleware.
func (s *Server) WrapHandler(mw func(http.Handler) http.Handler) {
	s.srv.Handler = mw(s.srv.Handler)
}

// Name implements emitter.Emitter.
func (s *Server) Name() string { return "mcp" }

// Notify implements emitter.Emitter — no push needed, clients pull via tools.
func (s *Server) Notify(_ context.Context) error { return nil }

// Start begins listening. Blocks until ctx is cancelled.
// ctx must carry the injected Store, Dispatcher, and UserConfig so that
// all HTTP request contexts inherit those values via BaseContext.
func (s *Server) Start(ctx context.Context) error {
	s.srv.BaseContext = func(net.Listener) context.Context {
		return context.WithoutCancel(ctx)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mcp listen %s: %w", s.addr, err)
	}
	s.log.Info("MCP server listening", "addr", s.addr)

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

// handleMCPGet handles GET /mcp — returns server info for capability discovery.
func (s *Server) handleMCPGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"name":    "focacal",
		"version": "0.1.0",
		"tools":   len(tools()),
	})
}

// handleMCP is the main JSON-RPC 2.0 entry point for MCP clients.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: "read error"}})
		return
	}

	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, mcpResponse{JSONRPC: "2.0", Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}

	var result any
	var mcpErr *mcpError

	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "focacal", "version": "0.1.0"},
		}
	case "tools/list":
		result = map[string]any{"tools": tools()}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			mcpErr = &mcpError{Code: -32602, Message: "invalid params"}
		} else {
			res, err := s.callTool(r.Context(), params.Name, params.Arguments)
			if err != nil {
				res = &toolResult{IsError: true, Content: []toolContent{{Type: "text", Text: err.Error()}}}
			}
			result = res
		}
	default:
		mcpErr = &mcpError{Code: -32601, Message: "method not found: " + req.Method}
	}

	writeJSON(w, mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: mcpErr})
}

// tools returns the list of tool definitions exposed by focacal.
func tools() []toolDef {
	return []toolDef{
		{
			Name:        "focacal_list_events",
			Description: "List all calendar events (real meetings) from all connected sources within a date range.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{"type": "string", "description": "Start date ISO8601 (e.g. 2024-01-15)"},
					"to":   map[string]any{"type": "string", "description": "End date ISO8601 (e.g. 2024-01-22)"},
				},
				"required": []string{"from", "to"},
			},
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			Name:        "focacal_get_free_gaps",
			Description: "Compute free time gaps for a specific date, respecting working hours, existing meetings, and configured buffers around meetings.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"date": map[string]any{"type": "string", "description": "Date to compute gaps for (ISO8601, e.g. 2024-01-15)"},
				},
				"required": []string{"date"},
			},
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			Name:        "focacal_write_block",
			Description: "Create or update an AI-planned time block (focus session, lunch, or break).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "enum": []string{"focus", "lunch", "break"}},
					"title":    map[string]any{"type": "string", "description": "Display title for the block"},
					"start":    map[string]any{"type": "string", "description": "Start time RFC3339"},
					"end":      map[string]any{"type": "string", "description": "End time RFC3339"},
					"priority": map[string]any{"type": "integer", "description": "Priority 0-10; higher = more important to protect"},
				},
				"required": []string{"type", "title", "start", "end"},
			},
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
		{
			Name:        "focacal_delete_block",
			Description: "Remove an AI-planned block by its ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Block ID to delete"},
				},
				"required": []string{"id"},
			},
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true},
		},
		{
			Name:        "focacal_list_blocks",
			Description: "List all AI-planned blocks (focus, lunch, break) within a date range.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{"type": "string", "description": "Start date ISO8601"},
					"to":   map[string]any{"type": "string", "description": "End date ISO8601"},
				},
				"required": []string{"from", "to"},
			},
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			Name:        "focacal_get_preferences",
			Description: "Read the user's scheduling preferences: working hours, focus style, lunch window, buffer times.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": false},
		},
		{
			Name:        "focacal_trigger_emitters",
			Description: "Signal all output emitters (CalDAV, REST, write-back) to push the current planned block state to consumers. Call this after all write_block/delete_block operations are complete.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
		},
	}
}

// callTool dispatches a tool call by name.
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) (*toolResult, error) {
	switch name {
	case "focacal_list_events":
		return s.toolListEvents(ctx, args)
	case "focacal_get_free_gaps":
		return s.toolGetFreeGaps(ctx, args)
	case "focacal_write_block":
		return s.toolWriteBlock(ctx, args)
	case "focacal_delete_block":
		return s.toolDeleteBlock(ctx, args)
	case "focacal_list_blocks":
		return s.toolListBlocks(ctx, args)
	case "focacal_get_preferences":
		return s.toolGetPreferences(ctx)
	case "focacal_trigger_emitters":
		return s.toolTriggerEmitters(ctx)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) toolListEvents(ctx context.Context, args json.RawMessage) (*toolResult, error) {
	var p struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	from, err := time.Parse("2006-01-02", p.From)
	if err != nil {
		return nil, fmt.Errorf("invalid from date: %w", err)
	}
	to, err := time.Parse("2006-01-02", p.To)
	if err != nil {
		return nil, fmt.Errorf("invalid to date: %w", err)
	}

	events, err := store.FromContext(ctx).ListEvents(ctx, from, to)
	if err != nil {
		return nil, err
	}

	return jsonResult(events)
}

func (s *Server) toolGetFreeGaps(ctx context.Context, args json.RawMessage) (*toolResult, error) {
	var p struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	date, err := time.Parse("2006-01-02", p.Date)
	if err != nil {
		return nil, fmt.Errorf("invalid date: %w", err)
	}

	gaps, err := computeGaps(ctx, date)
	if err != nil {
		return nil, err
	}
	return jsonResult(gaps)
}

func (s *Server) toolWriteBlock(ctx context.Context, args json.RawMessage) (*toolResult, error) {
	var p struct {
		Type     string `json:"type"`
		Title    string `json:"title"`
		Start    string `json:"start"`
		End      string `json:"end"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	start, err := time.Parse(time.RFC3339, p.Start)
	if err != nil {
		return nil, fmt.Errorf("invalid start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, p.End)
	if err != nil {
		return nil, fmt.Errorf("invalid end: %w", err)
	}

	b := store.NewBlock(store.BlockType(p.Type), p.Title, start, end, p.Priority)
	if err := store.FromContext(ctx).UpsertBlock(ctx, b); err != nil {
		return nil, err
	}
	return jsonResult(map[string]string{"id": b.ID, "status": "created"})
}

func (s *Server) toolDeleteBlock(ctx context.Context, args json.RawMessage) (*toolResult, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	db := store.FromContext(ctx)
	b, err := db.GetBlock(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("block not found: %w", err)
	}
	if b.Locked {
		return nil, fmt.Errorf("block %q is locked by the user and cannot be deleted by the planner", p.ID)
	}
	if err := db.DeleteBlock(ctx, p.ID); err != nil {
		return nil, err
	}
	return jsonResult(map[string]string{"status": "deleted"})
}

func (s *Server) toolListBlocks(ctx context.Context, args json.RawMessage) (*toolResult, error) {
	var p struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	from, _ := time.Parse("2006-01-02", p.From)
	to, _ := time.Parse("2006-01-02", p.To)
	blocks, err := store.FromContext(ctx).ListBlocks(ctx, from, to)
	if err != nil {
		return nil, err
	}
	return jsonResult(blocks)
}

func (s *Server) toolGetPreferences(ctx context.Context) (*toolResult, error) {
	uc := config.UserConfigFromContext(ctx)
	prefs := map[string]any{
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
	}
	return jsonResult(prefs)
}

func (s *Server) toolTriggerEmitters(ctx context.Context) (*toolResult, error) {
	bus.FromContext(ctx).Publish(bus.Message{Type: bus.EventBlocksUpdated})
	return jsonResult(map[string]string{"status": "emitters notified"})
}

// ── Gap computation ───────────────────────────────────────────────────────────

// Gap represents a free time slot on a given day.
type Gap struct {
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	DurationMins   int       `json:"duration_minutes"`
	IsLunchWindow  bool      `json:"is_lunch_window,omitempty"`  // overlaps the configured lunch window
	PomodoroSets   int       `json:"pomodoro_sets,omitempty"`    // complete work+break cycles that fit (pomodoro mode)
	SuggestedSplit bool      `json:"suggested_split,omitempty"` // this slot was produced by pre-splitting a longer gap
}

func computeGaps(ctx context.Context, date time.Time) ([]Gap, error) {
	uc := config.UserConfigFromContext(ctx)
	db := store.FromContext(ctx)

	loc, err := uc.WorkingHours.Location()
	if err != nil {
		loc = time.UTC
	}

	startOff, _ := uc.WorkingHours.StartTime()
	endOff, _ := uc.WorkingHours.EndTime()
	midnight := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	dayStart := midnight.Add(startOff)
	dayEnd := midnight.Add(endOff)

	// Parse the lunch window for this specific day.
	lunchWindowStart, lunchWindowEnd, _ := lunchWindow(uc, midnight)

	events, err := db.ListEvents(ctx, dayStart, dayEnd)
	if err != nil {
		return nil, err
	}

	buf := time.Duration(uc.Focus.BufferAroundMeetings) * time.Minute
	minBlock := time.Duration(uc.Focus.MinBlockMinutes) * time.Minute
	maxBlock := time.Duration(uc.Focus.MaxBlockMinutes) * time.Minute

	// For fixed mode the minimum usable gap is a full max-block, not min-block.
	threshold := minBlock
	if uc.Focus.Style == "fixed" {
		threshold = maxBlock
	}

	// Build busy intervals from events, expanded by buffer.
	var busy []timeInterval
	for _, ev := range events {
		if ev.AllDay {
			continue
		}
		s := ev.Start.Add(-buf)
		e := ev.End.Add(buf)
		if s.Before(dayStart) {
			s = dayStart
		}
		if e.After(dayEnd) {
			e = dayEnd
		}
		busy = append(busy, timeInterval{s, e})
	}

	// Merge overlapping intervals.
	merged := mergeIntervals(busy)

	// Collect raw free slots that meet the threshold.
	var rawGaps []Gap
	cursor := dayStart
	for _, b := range merged {
		if b.s.After(cursor) {
			dur := b.s.Sub(cursor)
			if dur >= threshold {
				rawGaps = append(rawGaps, Gap{Start: cursor, End: b.s, DurationMins: int(dur.Minutes())})
			}
		}
		if b.e.After(cursor) {
			cursor = b.e
		}
	}
	if cursor.Before(dayEnd) {
		dur := dayEnd.Sub(cursor)
		if dur >= threshold {
			rawGaps = append(rawGaps, Gap{Start: cursor, End: dayEnd, DurationMins: int(dur.Minutes())})
		}
	}

	// Pre-split, annotate with lunch window and pomodoro counts.
	var gaps []Gap
	for _, g := range rawGaps {
		// Mark whether this raw gap overlaps the lunch window before splitting.
		g.IsLunchWindow = !lunchWindowStart.IsZero() &&
			g.Start.Before(lunchWindowEnd) && g.End.After(lunchWindowStart)

		for _, chunk := range splitGap(g, &uc.Focus, minBlock, maxBlock) {
			if uc.Focus.Style == "pomodoro" {
				setDur := time.Duration(uc.Focus.PomodoroWorkMinutes+uc.Focus.PomodoroBreakMinutes) * time.Minute
				if setDur > 0 {
					chunk.PomodoroSets = int(chunk.End.Sub(chunk.Start) / setDur)
				}
			}
			gaps = append(gaps, chunk)
		}
	}

	return gaps, nil
}

// lunchWindow returns the lunch window start/end times for the given day midnight,
// or zero times if the lunch config cannot be parsed.
func lunchWindow(uc *config.UserConfig, midnight time.Time) (start, end time.Time, err error) {
	wsOff, err := parseTOD(uc.Lunch.WindowStart)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	weOff, err := parseTOD(uc.Lunch.WindowEnd)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return midnight.Add(wsOff), midnight.Add(weOff), nil
}

// parseTOD mirrors the config helper but is accessible within this package.
func parseTOD(s string) (time.Duration, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("invalid time-of-day %q: %w", s, err)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute, nil
}

// splitGap breaks a gap longer than maxBlock into chunks.
// Each chunk inherits the gap's IsLunchWindow flag and is tagged SuggestedSplit.
// In fixed mode, remainder chunks smaller than maxBlock are discarded.
func splitGap(g Gap, fc *config.FocusConfig, minBlock, maxBlock time.Duration) []Gap {
	total := g.End.Sub(g.Start)
	if total <= maxBlock {
		return []Gap{g}
	}

	var chunks []Gap
	cursor := g.Start
	for {
		remaining := g.End.Sub(cursor)
		if remaining <= 0 {
			break
		}
		if remaining >= maxBlock {
			chunks = append(chunks, Gap{
				Start:          cursor,
				End:            cursor.Add(maxBlock),
				DurationMins:   fc.MaxBlockMinutes,
				IsLunchWindow:  g.IsLunchWindow,
				SuggestedSplit: true,
			})
			cursor = cursor.Add(maxBlock)
		} else {
			// Remainder: keep for flexible/pomodoro if it meets minBlock; discard for fixed.
			if fc.Style != "fixed" && remaining >= minBlock {
				chunks = append(chunks, Gap{
					Start:          cursor,
					End:            g.End,
					DurationMins:   int(remaining.Minutes()),
					IsLunchWindow:  g.IsLunchWindow,
					SuggestedSplit: true,
				})
			}
			break
		}
	}
	return chunks
}

type timeInterval struct{ s, e time.Time }

func mergeIntervals(intervals []timeInterval) []timeInterval {
	if len(intervals) == 0 {
		return nil
	}
	// Insertion sort by start time.
	for i := 1; i < len(intervals); i++ {
		for j := i; j > 0 && intervals[j].s.Before(intervals[j-1].s); j-- {
			intervals[j], intervals[j-1] = intervals[j-1], intervals[j]
		}
	}
	merged := []timeInterval{intervals[0]}
	for _, iv := range intervals[1:] {
		last := &merged[len(merged)-1]
		if !iv.s.After(last.e) {
			if iv.e.After(last.e) {
				last.e = iv.e
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

// ── Ollama agent loop ─────────────────────────────────────────────────────────

// Agent drives the Ollama model through a tool-use loop against the MCP server.
type Agent struct {
	cfg    *config.Config
	mcpURL string // internal MCP endpoint, e.g. http://localhost:5234/mcp
	log    *slog.Logger
}

// NewAgent creates an Ollama agent.
func NewAgent(cfg *config.Config, mcpPort int, log *slog.Logger) *Agent {
	return &Agent{
		cfg:    cfg,
		mcpURL: fmt.Sprintf("http://localhost:%d/mcp", mcpPort),
		log:    log,
	}
}

// Replan runs the scheduler agent to regenerate the schedule for the next 7 days.
// It uses the OpenAI chat completions format, which is supported by Ollama,
// OpenAI, Anthropic, Azure OpenAI, GitHub Models, Groq, and others.
// log overrides the agent's default logger for this invocation (attach request_id).
func (a *Agent) Replan(ctx context.Context, log *slog.Logger) error {
	if log == nil {
		log = a.log
	}

	cfg := a.cfg.Agent
	apiURL := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	timeout := time.Duration(cfg.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	client := &http.Client{Timeout: timeout}

	log.Info("agent: starting replan", "model", cfg.Model, "endpoint", apiURL)

	agentTools := a.buildTools()
	messages := []chatMessage{
		{Role: "system", Content: a.buildSystemPrompt()},
		{Role: "user", Content: fmt.Sprintf("Please plan my schedule for the next 7 days starting from %s.", time.Now().Format("2006-01-02"))},
	}

	// Agent loop: up to 30 tool-call rounds to prevent runaway.
	for round := 0; round < 30; round++ {
		body := map[string]any{
			"model":      cfg.Model,
			"messages":   messages,
			"tools":      agentTools,
			"stream":     false,
			"max_tokens": maxTokens,
		}
		reqBody, _ := json.Marshal(body)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("agent request: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("agent API %d: %s", resp.StatusCode, respBody)
		}

		var chatResp struct {
			Choices []struct {
				Message      chatMessage `json:"message"`
				FinishReason string      `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			return fmt.Errorf("parse agent response: %w", err)
		}
		if len(chatResp.Choices) == 0 {
			return fmt.Errorf("agent returned no choices")
		}

		choice := chatResp.Choices[0]
		messages = append(messages, choice.Message)

		// No tool calls — model is done.
		if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) == 0 {
			log.Info("agent: replan complete", "rounds", round+1)
			return nil
		}

		// Execute each tool call via the MCP server.
		for _, tc := range choice.Message.ToolCalls {
			result, err := a.callMCPTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				log.Warn("agent: tool call failed", "tool", tc.Function.Name, "err", err)
				result = fmt.Sprintf(`{"error": %q}`, err.Error())
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	return fmt.Errorf("agent: exceeded max tool-call rounds")
}

// callMCPTool calls a single tool on the MCP server and returns the result text.
func (a *Agent) callMCPTool(ctx context.Context, toolName string, arguments json.RawMessage) (string, error) {
	reqBody, _ := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustMarshal(toolCallParams{
			Name:      toolName,
			Arguments: arguments,
		}),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.mcpURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var mcpResp mcpResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return "", err
	}
	if mcpResp.Error != nil {
		return "", fmt.Errorf("mcp error %d: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	resultBytes, _ := json.Marshal(mcpResp.Result)
	return string(resultBytes), nil
}

// buildSystemPrompt produces a focus-style-aware system prompt for the agent.
func (a *Agent) buildSystemPrompt() string {
	var b strings.Builder

	b.WriteString(`You are a personal calendar scheduling assistant called Focacal.
Your job is to plan focus blocks, a lunch break, and short breaks across the next 7 days.
Always use the provided tools — never guess at calendar data.
Meetings take absolute priority; never place a block that overlaps an existing event.`)

	// Lunch always comes first within its window.
	b.WriteString(fmt.Sprintf(`

Lunch priority: For each day, place a lunch block FIRST inside the configured lunch window
(gaps with is_lunch_window=true) before placing any focus blocks. The lunch block should be
%d minutes long. If no lunch-window gap exists, place lunch in the nearest available slot.`,
		a.cfg.Lunch.DurationMinutes))

	switch a.cfg.Focus.Style {
	case "pomodoro":
		b.WriteString(fmt.Sprintf(`

Pomodoro mode: Each gap shows a pomodoro_sets count — the number of complete %d-min work +
%d-min break cycles that fit. For every gap where pomodoro_sets >= 1, place alternating
'focus' blocks of exactly %d minutes and 'break' blocks of exactly %d minutes until the
gap is filled. Never place a single large focus block in pomodoro mode.`,
			a.cfg.Focus.PomodoroWorkMinutes, a.cfg.Focus.PomodoroBreakMinutes,
			a.cfg.Focus.PomodoroWorkMinutes, a.cfg.Focus.PomodoroBreakMinutes))

	case "fixed":
		b.WriteString(fmt.Sprintf(`

Fixed mode: Every focus block must be exactly %d minutes long. The gaps returned by
focacal_get_free_gaps are already pre-split to this size (suggested_split=true).
Place one focus block per slot. Do not merge slots or create partial blocks.`,
			a.cfg.Focus.MaxBlockMinutes))

	default: // flexible
		b.WriteString(fmt.Sprintf(`

Flexible mode: Focus blocks should be between %d and %d minutes. Gaps with
suggested_split=true are pre-sized to the maximum; you may use them as-is or leave
shorter ones for natural breaks. Prefer longer uninterrupted blocks when available.`,
			a.cfg.Focus.MinBlockMinutes, a.cfg.Focus.MaxBlockMinutes))
	}

	b.WriteString(`

Locked blocks: focacal_list_blocks returns a "locked" field. Never attempt to delete a block
with locked=true — it is user-protected and focacal_delete_block will reject the call.
Treat locked blocks as immovable fixtures when planning around them.

Call focacal_trigger_emitters when done.
Respond only with tool calls until the schedule is complete, then confirm briefly.`)

	return b.String()
}

// buildTools converts MCP tool definitions to the OpenAI function-calling format,
// which is supported by Ollama (/v1/chat/completions), OpenAI, Anthropic's
// OpenAI-compatible endpoint, Azure OpenAI, GitHub Models, Groq, and others.
func (a *Agent) buildTools() []map[string]any {
	defs := tools()
	out := make([]map[string]any, 0, len(defs))
	for _, t := range defs {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}

// chatMessage is the OpenAI chat completions message format.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"` // for role=tool responses
}

// chatToolCall is the tool_call block returned by the model in assistant messages.
type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string (not a raw object)
	} `json:"function"`
}

// ── helpers ───────────────────────────────────────────────────────────────────

func jsonResult(v any) (*toolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &toolResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
