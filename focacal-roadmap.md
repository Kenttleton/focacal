# Focacal — remaining phases roadmap

Phase 1 (scaffolding, core pipeline, Google connector, CalDAV, REST, MCP server, Ollama agent, Raycast extension) is complete. The following phases cover everything needed to reach a fully production-ready system.

---

## Phase 2 — Outlook connector completion

**Status:** Auth and sync are working. Write-back and webhook registration are stubbed.

### What to build

**Graph change notification subscription** (`internal/connector/outlook/sync.go → RegisterWebhook`). Microsoft Graph subscriptions expire after 3 days for calendar resources. The connector needs to POST to `/subscriptions`, store the subscription ID, and renew automatically on each sync cycle before expiry. The webhook handler must also respond to Graph's validation handshake (a GET with `validationToken` query param) before it will accept notifications.

**Write-back implementation** (`internal/connector/outlook/sync.go → WriteEvent / DeleteEvent`). Graph calendar event creation uses `POST /me/events` (or `/me/calendars/{id}/events`). Updates use `PATCH`. The payload shape differs from Google's — notably `showAs` maps to Graph's `showAs` field directly (`busy`, `free`, `tentative`, `oof`), and there is no `extendedProperties` equivalent; use `singleValueExtendedProperties` with a named property to tag focacal-managed events.

**Token refresh handling.** The `golang.org/x/oauth2` client handles refresh automatically, but Graph returns `401` with a specific error code when the refresh token itself has expired (common with short-lived app registrations). The connector should detect this and log a clear re-auth prompt rather than silently failing.

### Acceptance criteria
- Outlook events appear in the unified CalDAV feed
- Focus blocks are written to the Outlook calendar and carry the `X-FOCACAL-MANAGED` extended property equivalent
- Webhook notifications trigger a replan within 30 seconds of a calendar change
- Expired subscription is renewed without a container restart

---

## Phase 3 — Scheduling modes

**Status:** The planner always uses flexible mode. Pomodoro and fixed modes are configured but not implemented.

### What to build

**Pomodoro scheduler** (`internal/mcp/server.go → computeGaps`, agent system prompt). When `focus.style = "pomodoro"`, the agent should receive gap information annotated with how many complete 25-min work + 5-min break cycles fit. The agent writes alternating `focus` and `break` blocks rather than one large block per gap. The system prompt needs a pomodoro-specific instruction set so the model understands the pattern.

**Fixed scheduler.** When `focus.style = "fixed"`, every focus block is exactly `focus.max_block_minutes` long. Gaps shorter than that threshold are skipped rather than partially filled. This is useful for people who want predictable, bookable-looking blocks.

**Gap splitting logic** (`internal/mcp/server.go → computeGaps`). Currently a gap longer than `max_block_minutes` is returned as one large slot and left to the agent to split. This is unreliable — the agent sometimes places a single oversized block. The `get_free_gaps` tool should pre-split long gaps into chunks of `max_block_minutes` and return them as separate slots, with a `suggested_split` flag the agent can use or ignore.

**Lunch prioritisation.** The agent currently treats lunch like any other block. It should receive an explicit instruction to place lunch within the configured window before filling focus slots, and `get_free_gaps` should mark the lunch window separately in its response so the agent can see it at a glance.

### Acceptance criteria
- Pomodoro mode produces alternating focus/break blocks of the correct lengths
- Fixed mode produces uniform-length blocks with no partial-fill slots
- Lunch is always placed inside its configured window when a gap exists
- All three modes are selectable via `config.yaml` with no code changes

---

## Phase 4 — Web UI

**Status:** Not started. The REST API provides all the data; there is no visual management interface.

### What to build

**Today view.** A single-page app (served by the REST server at `/ui`) that shows the day's timeline — real events and AI blocks laid out on a visual timeline, colour-coded by type. The current time indicator scrolls into view on load.

**Block management.** Clicking a focus or lunch block opens a small popover with options: extend, shorten, delete, or lock (mark as protected so the next replan won't move it). Locked blocks are stored with a `locked: true` flag in SQLite and skipped by the agent's delete pass.

**Manual block creation.** A drag-to-create interaction on the timeline lets the user place a block in any free slot. The block is written directly via `focacal_write_block` and marked as user-created so the agent treats it as locked.

**Settings panel.** A simple form for editing working hours, lunch window, focus style, and buffer durations. Changes are written to `config.yaml` (mounted as a volume) and take effect on the next replan.

**Replan button.** A prominent button that calls `POST /replan` and shows a loading state while the agent loop runs. The timeline refreshes automatically when `EventBlocksUpdated` is received (poll `/health` or open a WebSocket on the REST server).

### Technology recommendation
A single self-contained HTML file using Alpine.js and plain CSS, served directly from the Go REST server. No build step, no node_modules, no separate container. The file is embedded into the binary using `go:embed`.

### Acceptance criteria
- The timeline correctly renders all events and blocks for the current day
- Block edits persist across container restarts
- The settings form round-trips correctly to `config.yaml`
- The UI is usable on a 1280-wide screen without horizontal scrolling

---

## Phase 5 — Reliability and production hardening

**Status:** Not started. The current implementation is suitable for personal use but has several rough edges for a long-running deployment.

### What to build

**Webhook renewal cron.** Google push channels expire after 7 days and must be renewed with a new `watch` call. Outlook subscriptions expire after 3 days. A background goroutine should check expiry times on startup and every hour, renewing any channel within 24 hours of expiry. Failure to renew should fall back to polling rather than going silent.

**Retry and back-off on API errors.** The connectors currently fail fast on any non-200 response. Google and Graph both return `429 Too Many Requests` and `503 Service Unavailable` under load. All outbound API calls should use an exponential back-off retry (max 3 attempts, starting at 2s) with `Retry-After` header respect.

**Agent timeout and fallback.** The Ollama agent loop has a hard 5-minute timeout but no fallback behaviour. If the agent fails or times out, the existing blocks should be left in place rather than cleared. A `last_successful_replan` timestamp should be exposed on `GET /health` so users and monitoring tools can detect a stuck planner.

**Structured logging improvements.** All log lines should include `source_id` and a `request_id` (propagated from the webhook or poll trigger) so a complete replan cycle can be traced end-to-end in a log aggregator.

**SQLite WAL checkpoint.** Long-running WAL files grow unboundedly under write load. A daily `PRAGMA wal_checkpoint(TRUNCATE)` should be scheduled during off-hours (configurable, default 3 AM in the user's timezone).

**`go.sum` and dependency pinning.** The initial scaffold omits `go.sum` because `go mod tidy` requires network access. CI should run `go mod tidy` and commit the result. Dependabot or Renovate should be configured to keep `golang.org/x/oauth2` and `modernc.org/sqlite` current, as both receive regular security patches.

### Acceptance criteria
- The container runs for 30 days without manual intervention (webhook renewal tested)
- A simulated Google `429` response causes a retry with correct back-off, not a sync failure
- `GET /health` exposes `last_successful_replan`, `last_sync` per source, and webhook expiry times
- Log lines for a single replan cycle share a common `request_id`

---

## Phase 6 — iCloud and additional CalDAV sources

**Status:** Not started. The connector interface is designed for this extension.

### What to build

**iCloud CalDAV connector.** Apple does not offer an OAuth2 flow for iCloud Calendar. Authentication uses app-specific passwords and HTTP Basic Auth against `caldav.icloud.com`. The connector reads and writes events using the standard CalDAV/WebDAV protocol (`PROPFIND`, `REPORT`, `PUT`, `DELETE`). The `go-webdav` library already in `go.mod` covers the CalDAV client side.

**Generic CalDAV connector.** Any server that speaks RFC 4791 (Nextcloud, Radicale, Baikal, Fastmail, Proton Calendar's export endpoint) can be supported with the same connector. Configuration adds a `caldav_url` and `auth` block (basic or bearer token) to the source entry.

**Source priority and conflict resolution.** When multiple sources are active, events from the same underlying meeting (e.g. a Google Meet invite accepted in both Google Calendar and iCloud) may appear as duplicates. A deduplication pass keyed on `UID` should run after each sync, keeping the most recently updated copy.

### Acceptance criteria
- iCloud events appear in the CalDAV feed and are factored into gap computation
- A Nextcloud CalDAV source can be configured with `type: caldav` and a URL
- Duplicate events from the same UID across two sources collapse to one entry

---

## Phase 7 — Mobile and additional client integrations

**Status:** Not started.

### What to build

**Web based mobile app** A web based mobile to manage your calendar. This should use the web UI delivered by the server to manage your account. This may require an overhaul of the web UI or a secondary implementation to achieve.

**Obsidian daily note plugin.** A small Obsidian community plugin that calls `GET /today` at note-open time and inserts the day's focus schedule as a callout block at the top of the daily note. Useful for people who plan their day in Obsidian.

**Claude Desktop MCP integration.** The MCP server on `:5234` is already spec-compliant. Adding focacal as a server in Claude Desktop's `claude_desktop_config.json` exposes all seven tools to Claude's chat interface. Users can ask "move my afternoon focus block earlier" in natural language and Claude executes the tool calls. No additional code needed — this is a documentation and configuration task.

**Menu bar app (native).** The Raycast extension covers the menu bar use case for Raycast users. For users without Raycast, a small native SwiftUI menu bar app (a separate target in the repo) polls `GET /now` every 60 seconds and shows the current or next event with a countdown. Clicking opens the web UI. This replaces the original daemon-era status bar concept with a lightweight, focused native app.

**Windows tray application (native)** Covering other operating systems common for work workflows. This app should work similar to the Menu bar for Mac but be native to windows using an icon in the system tray that shows a popup on click to perform the same management.

### Acceptance criteria

- The Obsidian plugin inserts a correctly formatted schedule block without overwriting existing note content
- The native menu bar app shows the correct event on macOS Sequoia without requiring Raycast

---

## Dependency and compatibility notes

| Dependency | Current version | Notes |
| --- | --- | --- |
| `golang.org/x/oauth2` | v0.21.0 | Covers Google, Microsoft, and generic OAuth2. Pin to a release tag in CI. |
| `modernc.org/sqlite` | v1.31.1 | Pure Go, no CGo — required for the Alpine Docker image. Do not swap for `mattn/go-sqlite3`. |
| `go-chi/chi` | v5.1.0 | Router for REST and MCP HTTP servers. |
| `emersion/go-ical` | v0.0.0-20240127 | ICS parsing and generation for the CalDAV feed. |
| `emersion/go-webdav` | v0.5.0 | WebDAV/CalDAV client for the iCloud connector (Phase 6). |
| Ollama | any recent | Requires a model with reliable tool-use: `qwen2.5:14b` (recommended), `qwen2.5:7b` (low-RAM). `llama3.1:8b` works but is less consistent with multi-step tool chains. |
| Raycast | 1.79.0+ | Extension API version used by the `@raycast/api` dependency. |
