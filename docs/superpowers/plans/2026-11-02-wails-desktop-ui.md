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

## Tasks
1. Scaffold Wails app (`wails init -n focacal -t svelte`).
2. Add Go bindings: `GetEvents`, `GetConflicts`, `DismissConflict`, `GetSources`, `GetDestinations`, `PutSources`, `PutDestinations`, `GetConfig`, `UpdateConfig`.
3. Build calendar view component using event.color, conflict markers, side-by-side overlaps.
4. Conflicts panel with dismiss.
5. Settings tab: sources/destinations editors (JSON textarea first, guided form later).
6. Keep `rest/ui.html` for headless/web fallback or remove after parity.
7. Build pipeline: `wails build` for macOS/Windows/Linux; CI artifacts.
8. Docs: README update, compose unchanged (REST still serves web).

## Open decisions
- Frontend framework: Svelte (smaller) vs React (larger ecosystem/component libs).
- Component library: shadcn/ui (React) vs DaisyUI (Tailwind, framework-agnostic).
- Whether desktop replaces web UI entirely or coexists.

## Non-goals (for now)
- Mobile.
- Rewriting backend.
EOF