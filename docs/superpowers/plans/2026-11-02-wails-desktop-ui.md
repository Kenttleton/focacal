# Dual UI Plan: Web + Wails Desktop (replaces Task 12 embedded web UI)

## Goal
Replace the embedded `rest/ui.html` with a shared frontend (Svelte or React + component library) used by BOTH:
- A web build served by the REST server (static assets), and
- A Wails v2 desktop binary.

One codebase, two shells. Web build talks to REST API; desktop build uses Wails Go bindings (no HTTP hop) but can fall back to REST.

## Why
- Go backend already exposes a REST API + CalDAV + MCP; a Wails app can reuse it.
- One frontend codebase for desktop and web.
- Component library (shadcn/ui or DaisyUI) speeds up the calendar/conflicts/sources editor.

## Architecture
- New top-level `desktop/` (or `wails/`) module using Wails v2.
- Frontend in `desktop/frontend/` (Svelte + Tailwind + a component library).
- Backend bindings call into existing `internal/` packages directly (store, config, calendar, conflict) via Wails-bound Go methods, OR call the REST API. Prefer direct bindings for desktop (no HTTP hop); keep REST for web/external clients.
- Reuse the existing REST handlers as the contract for DTOs.

## Contracts & TDD (idiomatic Go)
Before scaffolding, define and test these:

### Wails-bound service (Go)
```go
type UIService struct {
    store  store.Store
    cfg    *config.UserConfig
    remote *RemoteClient // nil in local mode
}
func (s *UIService) Events(ctx context.Context, from, to time.Time) ([]rest.EventDTO, error)
func (s *UIService) Conflicts(ctx context.Context, from, to time.Time) ([]ConflictDTO, error)
func (s *UIService) DismissConflict(ctx context.Context, key string) error
func (s *UIService) Sources(ctx context.Context) ([]SourceDTO, error)
func (s *UIService) Destinations(ctx context.Context) ([]config.DestinationConfig, error)
func (s *UIService) PutSources(ctx context.Context, list []config.SourceConfig) error
func (s *UIService) PutDestinations(ctx context.Context, list []config.DestinationConfig) error
func (s *UIService) BackendMode(ctx context.Context) string // "local" | "remote"
func (s *UIService) SetBackend(ctx context.Context, url, apiKey string) error
```
- Local mode: bind directly to store/config (no HTTP).
- Remote mode: delegate to REST client; same DTOs.
- Tests: `internal/ui/service_test.go` using in-memory store + temp config; table-driven.

### Error handling
- Return wrapped errors: `fmt.Errorf("ui: <op>: %w", err)`.
- Sentinel: `ErrNoConfigFile`, `ErrRemoteUnreachable`.
- UI maps to toast via error string; no panics.

### Context injection
- Local mode injects store/config at startup (mirrors cmd/start.go).
- Remote mode has no store; service uses HTTP client.

### DTOs
- Reuse `rest.EventDTO` and a new `ConflictDTO` (move to `internal/ui/dto.go` or `internal/emitter/rest`).

## Tasks
1. Scaffold Wails app (`wails init -n focacal -t react`).
2. Add Go bindings: `GetEvents`, `GetConflicts`, `DismissConflict`, `GetSources`, `GetDestinations`, `PutSources`, `PutDestinations`, `GetConfig`, `UpdateConfig`.
3. Build calendar view component using event.color, conflict markers, side-by-side overlaps.
4. Conflicts panel with dismiss.
5. Settings tab: sources/destinations editors (JSON textarea first, guided form later).
6. Keep `rest/ui.html` for headless/web fallback or remove after parity.
7. Build pipeline: `wails build` for macOS/Windows/Linux; CI artifacts.
8. Docs: README update, compose unchanged (REST still serves web).

## Decisions
- Frontend framework: **React** (ecosystem, component libraries).
- Component library: **Mantine** (batteries-included date/calendar components: DatePicker, Calendar, TimeInput) plus **FullCalendar** (or react-big-calendar) for the week view; Tailwind optional.
- Web and desktop coexist; same frontend, two shells.

## Desktop-specific: remote backend support
- Desktop app settings: configurable backend URL (default http://localhost:5233) + API key.
- When backend URL is set and reachable, desktop uses REST API (like web build) instead of in-process bindings.
- Allows a desktop client to point at a remote focacal deployment.
- Store config in Wails app config; allow switching between "local embedded" and "remote" mode.

## Non-goals (for now)
- Mobile.
- Rewriting backend.
EOF