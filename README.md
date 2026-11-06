# focacal

AI-powered calendar scheduling assistant. Aggregates Google Calendar, Outlook
and CalDAV calendars, uses a local Ollama model to plan focus time and lunch
around your meetings, and serves everything as a CalDAV feed, REST API and web UI.

**fo-ca-cal** — focus · calendar · AI.

---

## How it works

1. focacal pulls events from your Google and Outlook calendars on startup and every 5 minutes (plus webhook push when available).
2. A local Ollama model (Qwen 2.5 recommended) uses MCP tools to read your calendar and preferences, then plans focus blocks and lunch around your meetings.
3. The planned schedule is served as a CalDAV calendar that Mac Calendar, Google Calendar, and any other CalDAV client can subscribe to.
4. A REST API at `:5233` powers the Raycast extension.
5. Optionally, a push destination writes a trimmed copy of your planned blocks (and any calendars you choose) into a real calendar so coworkers see you as busy.

---

## Requirements

- Docker (with Compose v2)
- Ollama running somewhere (locally, on another machine, or in the same Compose stack)
- Google and/or Microsoft OAuth app credentials

---

## Quick start

### 1. Clone and configure

```bash
git clone https://github.com/Kenttleton/focacal.git
cd focacal
cp .env.example .env
```

Edit `.env` and fill in your OAuth credentials. See [OAuth setup](#oauth-setup) below.
docker-compose.yml passes `.env` into the container, so ICS feed URLs referenced
by a calendar's `url_env` and `FOCACAL_AGENT_API_KEY` go there too. An unset feed
variable only stops that calendar from syncing (startup logs a warning).

The first start creates `config/` from the samples in `config.example/`
(it never overwrites a file that is already there). Edit it: working hours and timezone in
`config/planner.yaml` (which also holds the scheduler agent), accounts,
calendars, destinations and the OAuth redirect in `config/calendars.yaml`, ports in `config/config.yaml`. See
[docs/config-domains.md](docs/config-domains.md) for what lives where.

```bash
$EDITOR config/planner.yaml config/calendars.yaml
```

Upgrading from a single `config.yaml`? Move it into the config directory
(`mkdir -p config && mv config.yaml config/config.yaml`).
focacal splits it into the new files on first start and keeps `config.yaml.bak`;
it refuses to start, changing nothing, if `config/` already holds a
`planner.yaml` or `calendars.yaml` that differs from your settings.
If that file has a login (`auth.password_hash`), the hash moves to
`config/credentials.yaml`. Hashes from this repository's earlier commits are
public, so rotate the login right after upgrading (`POST /auth/reset`, or
delete `config/credentials.yaml` and run setup again). See
[docs/config-domains.md](docs/config-domains.md#note-on-git-history).

### 2. Start the stack

**Option A — Ollama runs separately (already installed locally or remotely):**

```bash
# Set FOCACAL_AGENT_BASE_URL in .env to your Ollama instance, with /v1
# (e.g. http://host.docker.internal:11434/v1)
docker compose up -d
```

**Option B — Ollama managed by Compose (pulls the model automatically):**

```bash
docker compose -f docker-compose.yml -f docker-compose.ollama.yml up -d
```

The Ollama override pulls `qwen2.5:14b` on first start. This may take a few minutes.

### 3. Authorize accounts

Install the CLI wrapper so you can run focacal commands from your host:

```bash
chmod +x focacal.sh
sudo cp focacal.sh /usr/local/bin/focacal
```

Authorize each Google or Outlook account listed in `config/calendars.yaml`:

```bash
focacal auth google work-google
# Opens browser → complete OAuth consent → token stored in Docker volume
```

```bash
focacal auth outlook personal-outlook
```

### 4. Trigger the first sync

```bash
focacal sync
```

focacal syncs all calendars, runs the AI planner, and updates the CalDAV feed.
Subsequent syncs happen automatically every 5 minutes and on webhook notifications.

### 5. Subscribe your calendar apps

**Mac Calendar:**
1. Calendar → File → New Calendar Subscription
2. URL: `http://localhost:5232/calendar/focacal/`
3. Set refresh to "Every 5 minutes"

**Google Calendar:**
1. Other calendars → `+` → From URL
2. URL: `http://localhost:5232/calendar/focacal/`

Both apps will show your real meetings and AI-planned focus/lunch blocks in one unified calendar.

### 6. Install the Raycast extension

```bash
cd raycast-extension
npm install
npm run dev     # opens Raycast in development mode
```

Or build and sideload:

```bash
npm run build
```

In Raycast: Extensions → `+` → Import Extension → select `raycast-extension/`.

Set the API URL preference to `http://localhost:5233` (the default).

---

## CLI reference

All commands run inside the container via the `focacal.sh` wrapper:

| Command | Description |
|---|---|
| `focacal start` | Start all services (this is the container's default CMD) |
| `focacal auth google <account-id>` | Authorize a Google Calendar account |
| `focacal auth outlook <account-id>` | Authorize an Outlook account |
| `focacal sync` | Trigger an immediate sync + replan |
| `focacal status` | Check service health |

---

## OAuth setup

### Google Calendar

1. Go to [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
2. Create a project → Enable the **Google Calendar API**
3. Create an OAuth 2.0 Client ID (type: **Web application**)
4. Add authorized redirect URI: `http://localhost:8080/oauth/callback/google`
   - For deployed instances: `https://yourdomain.com/oauth/callback/google`
5. Copy the Client ID and Secret into `.env`

### Microsoft Outlook / M365

1. Go to [Azure App Registrations](https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps)
2. New registration → Accounts in any organizational directory + personal Microsoft accounts
3. Add redirect URI (Web): `http://localhost:8080/oauth/callback/outlook`
4. API permissions → Add `Calendars.Read` (and `Calendars.ReadWrite` if a push destination writes to an Outlook calendar)
5. Certificates & secrets → New client secret
6. Copy the Application (client) ID and secret into `.env`

---

## Configuration reference

The `config/` directory holds all non-secret settings, one file per domain.
Edits (from the UI or by hand) apply live within about a second; only the
`server:` block (ports, bind address, webhooks) needs a restart. A UI save
rewrites the file without its comments.

| File | Holds |
|---|---|
| `config/config.yaml` | App: `server`, `auth.enabled` |
| `config/planner.yaml` | Planner: `working_hours`, `lunch`, `focus`, `colors`, `agent` |
| `config/calendars.yaml` | Calendars: `accounts`, `calendars`, `destinations`, `oauth` |
| `config/credentials.yaml` | login, created by first-run setup (local only, gitignored) |

The directory is portable: copy `config/` (and `.env`) to another host, run
`focacal auth` for each OAuth account, and start. The SQLite DB holds only
rebuildable state (synced events, OAuth tokens, planner output). To reset the
login, delete `config/credentials.yaml`; setup reopens with no restart. Full
details, legacy keys and migration: [docs/config-domains.md](docs/config-domains.md).

`lunch` and `focus` are legacy keys, to be replaced by planner rules (Routine,
Focus, Transition).

### Working hours

In `config/planner.yaml`:

```yaml
working_hours:
  start: "09:00"
  end: "18:00"
  timezone: "America/New_York"   # any IANA timezone name
```

### Focus techniques

Focus time comes in more than one shape. A flexible focus block (a target amount
of time floated into your free gaps) is the common case. Focacal also plans
around focus *techniques* such as pomodoro (time-boxed work and break cycles),
which is where it goes beyond a plain focus-time scheduler. More techniques can
be added without a new rule kind.

A block at the same time on set days is not focus planning, it is a Routine, and
`fixed` is moving there in the planner refactor.

```yaml
focus:
  style: "flexible"          # flexible | pomodoro  (fixed is moving to Routine)
  min_block_minutes: 30      # ignore gaps shorter than this
  max_block_minutes: 120     # split longer gaps here
  buffer_around_meetings: 10 # minutes of breathing room around meetings
```

### Lunch

```yaml
lunch:
  preferred: "12:00"
  window_start: "11:30"
  window_end: "13:30"
  duration_minutes: 60
```

### Accounts, calendars and destinations

In `config/calendars.yaml` (full reference: [docs/config-domains.md](docs/config-domains.md#the-calendar-model)):

```yaml
accounts:                      # one per sign-in
  - id: work-google
    name: Work
    type: google               # google | outlook | caldav

calendars:                     # what focacal reads
  - id: work
    name: Work
    account: work-google
    remote_id: primary         # empty = the connector's default calendar
    plan: true                 # false = shown, but not planned around
  - id: team-feed              # a read-only feed instead of an account
    name: Team feed
    url_env: FOCACAL_ICS_TEAM_FEED

destinations:                  # where focacal publishes
  - id: focacal                # the default: a served feed of everything
    name: Focacal
    inputs: all
    target: {served: true, auth: private}
  - id: share-focus            # a push destination
    name: Focus on the team calendar
    inputs:
      - calendar: focacal      # the built-in calendar of planned blocks
        kinds: [focus]         # focus | lunch | break
      - calendar: work
        event:                 # absent = full; a present block is literal
          title: {visibility: true, rename: Busy}
    target: {calendar: team-shared}   # a calendar of an account
```

- **Ids** are generated from the name (lowercase slugs), never change, and
  `name` replaces the old `label` (`label` still loads). `focacal` is reserved
  for the built-in calendar of planned blocks.
- **Deleting** an account or calendar cascades to what depends on it. Send the
  `PATCH` with `?dry_run=true` first to list everything that would be pruned.
- **Served feeds** live at `/calendar/{id}/`; `/focacal/` is an alias for the
  destination with id `focacal`, and `/.well-known/caldav` redirects to
  `/calendar/`. `auth: public` is **not available yet**; until the auth work
  ships, feeds are protected by the same global auth setting as the API.
- **Visibility** is per input. Hiding the title or the body also hides the
  location, meeting link and original ids.
- **No two-way mirroring.** A config in which calendars feed each other in a
  loop is rejected, so `work -> home` together with `home -> work` is not
  allowed.
- **Migration.** An old `sources:` config is converted on first start (a
  `calendars.yaml.bak` is kept, migrated destinations get least-privilege
  inputs, and cached events are re-synced once).

### Push destinations

A destination with a calendar target writes sanitized events and planned
blocks into that calendar. Focacal only updates or deletes events it wrote
(tracked in a ledger), never deletes history, refuses a push that would delete
most of what it wrote, and recreates an event you deleted remotely when it
next changes. Details: [docs/config-domains.md](docs/config-domains.md#push-destinations).

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│ Docker Compose                                           │
│                                                          │
│  ┌──────────────────────────────────────────────────┐   │
│  │ focacal container                                 │   │
│  │                                                   │   │
│  │  Connectors ──► Event bus ──► SQLite store       │   │
│  │  (Google, Outlook)    (debounce)                  │   │
│  │                                 │                 │   │
│  │                           MCP server :5234        │   │
│  │                                 │                 │   │
│  │                         Ollama agent              │   │
│  │                       (tool-use loop)             │   │
│  │                                 │                 │   │
│  │            ┌────────────────────┼──────────┐      │   │
│  │            ▼                    ▼          ▼      │   │
│  │      CalDAV :5232        REST :5233   Push dest.  │   │
│  └──────────────────────────────────────────────────┘   │
│                                                          │
│  ┌──────────────────┐  (optional)                        │
│  │ ollama container │◄── focacal agent calls tools       │
│  │ :11434           │                                    │
│  └──────────────────┘                                    │
└─────────────────────────────────────────────────────────┘

Consumers:
  Mac Calendar / Google Cal ──► subscribe to CalDAV :5232
  Raycast extension         ──► polls REST API :5233
  Claude Desktop / other    ──► MCP client on :5234
```

### Key design decisions

**Single binary, multiple goroutines.** All services (CalDAV, REST, MCP, sync loop) run in one container process. SQLite handles the single-writer constraint cleanly.

**MCP as the AI interface.** The Ollama model never calls Google or Outlook APIs directly — it calls focacal's MCP tools (`focacal_list_events`, `focacal_get_free_gaps`, `focacal_write_block`, etc.). This keeps the model's job simple: reason about schedules, call tools, done.

**Meetings always win.** When a new meeting appears that overlaps a planned focus block, the planner removes or trims the block on the next replan cycle. The AI is instructed to never place blocks over existing events.

**Sanitized push.** What a destination publishes is trimmed per input: a hidden title becomes "Busy" (or your rename), a hidden body is dropped, and hiding either also drops the location, meeting link and original ids. Pushed events are written busy.

---

## Development

### First-time Go setup (outside Docker)

```bash
go mod tidy          # generates go.sum, downloads dependencies
go build ./...       # verifies the build
```

### Run locally without Docker

```bash
# Start Ollama separately
ollama serve

# Create /data directory for the SQLite store
mkdir -p /data

# Run from the repo root, naming the config directory (without --config,
# an empty search falls back to /data/config because /data exists):
go run . start --config ./config
```

### Desktop app (Wails)

The desktop UI lives in `desktop/focacal/` and shares the React frontend with the web build.

```bash
# Install Wails CLI (once)
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Dev mode (hot reload)
cd desktop/focacal
wails dev

# Production build
wails build
```

#### Connecting desktop to a backend
- **Local embedded mode** (default): desktop uses the in-process store/config (run alongside `go run . start` or standalone).
- **Remote mode**: open Settings → Backend, set the backend URL (e.g. `https://focacal.example.com`) and API key. The desktop then talks to that deployment's REST API.

### Docker deploy

```bash
cp .env.example .env   # fill in OAuth creds
# edit config/*.yaml as needed
docker compose up -d --build
```

`./config` is mounted at `/data/config` and is writable so the UI's config
editors persist (on Linux, make it writable by uid 1001). Restart the
container after editing.

### Project layout

```
focacal/
├── cmd/                        # cobra CLI commands
│   ├── root.go                 # config dir resolution + migration, global flags
│   ├── start.go                # launches all services
│   └── auth.go                 # OAuth flow + sync/status commands
├── internal/
│   ├── config/                 # typed config, domain files, migration, credentials
│   ├── connector/
│   │   ├── connector.go        # Connector interface
│   │   ├── google/             # Google Calendar OAuth + sync + push writes
│   │   ├── outlook/            # Outlook Graph API OAuth + sync
│   │   └── caldav/             # generic CalDAV account (e.g. iCloud)
│   ├── store/                  # SQLite: events, blocks, sync state, tokens
│   ├── bus/                    # in-process pub/sub with debounce
│   ├── mcp/                    # MCP server + tool implementations + Ollama agent
│   └── emitter/
│       ├── caldav/             # RFC 4791 CalDAV server (real + AI events merged)
│       ├── rest/               # REST API + web UI (ui.html)
│       └── (push destinations: internal/destination)
├── raycast-extension/          # TypeScript Raycast extension
├── mac-menubar/                # macOS menu bar app
├── windows-tray/               # Windows tray app
├── Dockerfile
├── docker-compose.yml
├── docker-compose.ollama.yml   # optional: Ollama in-stack
├── config.example/             # sample config, copied to config/ on first start
├── config/                     # live config directory, gitignored (docs/config-domains.md)
│   ├── config.yaml             # App: servers, auth switch
│   ├── planner.yaml            # Planner: working hours, lunch, focus, agent, colors
│   └── calendars.yaml          # Calendars: accounts, calendars, destinations, oauth
├── .env.example                # secret template (never commit .env)
└── focacal.sh                  # host-side CLI wrapper
```

---

## MCP tools reference

External MCP clients (Claude Desktop, custom scripts) can connect to `:5234`:

| Tool | Description |
|---|---|
| `focacal_list_events` | List real calendar events in a date range |
| `focacal_get_free_gaps` | Compute free slots for a specific date |
| `focacal_write_block` | Create a focus/lunch/break block |
| `focacal_delete_block` | Remove a planned block |
| `focacal_list_blocks` | List all AI-planned blocks in a date range |
| `focacal_get_preferences` | Read user scheduling preferences |
| `focacal_trigger_emitters` | Push current state to all outputs |

---

## Ports

| Port | Service | Notes |
|---|---|---|
| `5232` | CalDAV server | Subscribe from Mac Calendar / Google Calendar |
| `5233` | REST API | Raycast extension, curl, custom clients |
| `5234` | MCP server | Claude Desktop, external MCP clients (optional) |
| `8080` | OAuth callback | Used only during `focacal auth` — safe to close otherwise |

Override host-side port mappings in `.env` (e.g. `CALDAV_PORT=5232`).

---

## Troubleshooting

**`focacal auth` doesn't open a browser**
The CLI prints the auth URL — copy it into your browser manually.

**Events not appearing in CalDAV feed**
Run `focacal sync` and check `docker compose logs focacal` for sync errors.
Ensure the account is authorized (`focacal auth` completed successfully).

**Ollama model not responding**
Check `FOCACAL_AGENT_BASE_URL` in `.env` (it must end in `/v1`). Test with:
```bash
curl http://localhost:11434/api/tags
```

**CalDAV subscription shows empty in Mac Calendar**
Mac Calendar requires the CalDAV URL to respond correctly to PROPFIND.
Try subscribing to `http://localhost:5232/calendar/focacal/` (trailing slash matters).

**Raycast extension shows "focacal unreachable"**
Verify the container is running: `docker compose ps`
Check the REST API: `curl http://localhost:5233/health`
