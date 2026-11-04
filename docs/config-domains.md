# Config domains

Focacal's configuration is a directory of plain YAML files. The directory is
the whole portable setup: copy it to another machine and you have the same
instance. The SQLite database holds only state that can be rebuilt.

## Where the directory is

| How focacal runs | Config directory |
|---|---|
| `docker compose up` | `./config` on the host, mounted at `/data/config` |
| `focacal --config <path>` | `<path>` if it is a directory; if it is a file (the old `config.yaml` form), its directory. The directory must exist: a mistyped path is an error, not a fresh instance |
| no `--config` | the first of `/data/config`, `$HOME/.config/focacal`, `.` that holds a config file |
| nothing found | the first of `/data/config`, `$HOME/.config/focacal` that exists as a directory; otherwise `/data/config` when `/data` exists (the container image), else `./config` |

Without `--config`, the directory is created if it is missing, and focacal
then starts from the built-in defaults; startup logs a warning that first-run
`/auth/setup` is open. `.` is searched but never chosen as an empty default, so a
bare `focacal start` does not scatter files into whatever directory you ran it
from.

## What lives where

| File | Domain | Holds | Portable |
|---|---|---|---|
| `config.yaml` | App | `server` (ports, bind address, webhooks), `agent` (scheduler endpoint and model, not `api_key`), `oauth.redirect_base_url`, `auth.enabled` | yes |
| `planner.yaml` | Planner | `working_hours` (with the planning time zone), `lunch`, `focus`, `colors` (generated-event palette) | yes |
| `calendars.yaml` | Calendars | `sources` (accounts read from) and `destinations` | yes; secrets are references (see below) |
| `credentials.yaml` | (none) | login `username` and bcrypt `password_hash`, mode 0600 | local only, gitignored |
| `*.bak` | (none) | previous file contents from a migration or a fallback write | local only, gitignored |

Each domain is read only from its own file. A `sources:` block left in
`config.yaml` is ignored, and startup logs a warning naming it.

Values you can set by hand and from the UI are the same values. The UI writes
the file for the domain it edits, keeping comments and unknown keys, and the
response says `pending_restart: true` until the process restarts with the new
values.

### REST API

Each domain has one read and one write, behind the same auth as the rest of
the API (`GET /openapi.json` has the full schema):

- `GET /config/{domain}` (`app`, `planner`, `calendars`) returns
  `{"values": {...}, "order": {...}, "pending_restart": false}`. `values` are
  the file values over the defaults (no environment overrides); collections are
  objects keyed by id (destinations by `id|calendar_id`) and `order` lists
  their ids in file order. Secrets are never returned; each source has
  `has_secret`.
- `PATCH /config/{domain}` takes a JSON merge patch (RFC 7396): send only what
  changes, `null` deletes a key or an item, for example
  `{"working_hours": {"start": "08:30"}}` or
  `{"sources": {"old-feed": null}}`. It returns the new view.
  Errors are `{"error": "..."}`: 400 malformed body or unknown key, 404 unknown
  domain, 413 body over 1 MiB, 422 a value fails validation or a secret was
  sent inline (nothing is written), 500 a file on disk could not be read or
  parsed (details in the server log), 503 the server has no config service.
  `colors` keys must be non-empty and must not contain `.`.

### Secrets

Secrets never go into the domain files through the API:

- OAuth client secrets: `.env` (`GOOGLE_CLIENT_ID`, `OUTLOOK_CLIENT_SECRET`, ...)
  or a `credentials_file` reference.
- ICS feed URLs: `ics_url_env: FOCACAL_<NAME>` with the URL in `.env`. An
  inline `ics_url` or CalDAV `password` written by hand still works.
  docker-compose.yml passes `.env` into the container (`env_file`, optional).
  If the variable is not set, focacal still starts: it logs a warning naming
  the source and the variable (never a value), and only that calendar fails
  to sync, with an error naming the variable, until you set it and restart.
  An ics source with neither `ics_url_env` nor `ics_url` is invalid. Pick
  names that are not config overrides (`FOCACAL_ICS_<NAME>` is safe).
- CalDAV URLs must not embed credentials (`https://user:pw@host`). The API
  rejects them and never returns that part; set `username` instead.
- Agent API key: `FOCACAL_AGENT_API_KEY` in `.env`.

A relative `credentials_file` or `token_file` resolves under the config
directory, so `credentials_file: google-client.json` means
`config/google-client.json` and travels with the directory. Absolute paths
are used as written.

### Environment overrides

Any value can be overridden at runtime with `FOCACAL_<SECTION>_<KEY>` (for
example `FOCACAL_SERVER_REST_PORT=6233`), and `OAUTH_REDIRECT_BASE_URL`
overrides `oauth.redirect_base_url`. Overrides change what the process runs
with, not the files. The UI shows the file values, and an override never
makes `pending_restart` true.

## What the database holds

`/data/focacal.db` holds rebuildable state only:

| Table | Contents | Rebuilt by |
|---|---|---|
| `events` | events synced from sources | the next sync |
| `sync_state` | per-source sync cursors | a full sync |
| `oauth_tokens` | OAuth refresh tokens | `focacal auth <provider> <source-id>` |
| `blocks` | planner output, plus blocks added or edited in the UI | a replan (UI edits are lost) |
| `conflict_dismissals` | which conflicts you marked as seen | nothing: they show again |

`/data/recovery.key` (next to the DB) is the key for `/auth/recover`. It is
generated on first start and printed once.

## Legacy keys

- `lunch` and `focus` in `planner.yaml` are marked
  `# Legacy: replaced by planner rules (Routine, Focus, Transition). Do not extend.`
  They still drive planning until planner rules ship. Add new planning
  behaviour as rules, not as new keys here.
- `auth.username`, `auth.password_hash` in `config.yaml`: moved to
  `credentials.yaml` by the migration below.
- `auth.session_token` was never read. The migration drops it.
- `ollama:` (in old `config.yaml` files) was never read. The scheduler reads
  `agent:`. It is left in place as an unknown key; delete it when convenient.
- `display_timezone` (in old `planner.yaml` or `config.yaml` files) is no
  longer read. The display time zone is a client setting; planning uses
  `working_hours.timezone`. Delete the key when convenient.

## Migration from a single `config.yaml`

On start, before loading, focacal checks the config directory's `config.yaml`:

1. If it holds `working_hours`, `lunch`, `focus`, `colors`, `sources` or
   `destinations`, those keys move to `planner.yaml` and `calendars.yaml`,
   comments included. The result is loaded and validated first; if it would
   not load (for example a YAML alias pointing at an anchor in another
   section) or would change any value, nothing is written.
2. If it holds `auth.username` / `auth.password_hash`, they move to
   `credentials.yaml` (created 0600). An existing `credentials.yaml` is never
   overwritten. `auth.session_token` is dropped.
3. The original is kept as `config.yaml.bak` (mode 0600, because it may
   contain the password hash). Unknown keys stay in `config.yaml`.

The migration runs once. After it, nothing is left to move and later starts
do nothing. If `planner.yaml` or `calendars.yaml` already exists while
`config.yaml` still holds keys for it, startup stops with an error and
changes nothing, rather than silently keep one copy. If the existing file is
the sample from the repo, delete it and restart; otherwise merge the keys
into it by hand and remove them from `config.yaml`. (A target file that
already holds exactly what the migration would write, left by an interrupted
earlier run, counts as done, so an interrupted migration resumes.)

### Upgrading a Docker install

The old compose file mounted a single file (`./config.yaml:/data/config.yaml`).
Focacal now refuses to start on that layout: it would otherwise start with no
login, which reopens first-run setup. To upgrade:

```bash
docker compose down
# take the new docker-compose.yml (mounts ./config:/data/config)
# The repo's config/ ships sample planner.yaml and calendars.yaml: delete
# them first, or the migration refuses to overwrite them.
rm -f config/planner.yaml config/calendars.yaml
mkdir -p config && mv config.yaml config/config.yaml
docker compose up -d     # migrates config/config.yaml on first start
```

On Linux, make `config/` writable by uid 1001 (`sudo chown -R 1001 config`).

## Porting an instance

1. Copy the config directory (`config/`) to the new host. Include
   `credentials.yaml` to keep the same login, or leave it out to set a new one.
2. Copy `.env` (secrets are not in `config/`).
3. Start focacal, then run `focacal auth <provider> <source-id>` for each
   OAuth source (tokens live in the DB, which you do not copy).
4. The first sync and replan rebuild the DB.

To reset the login, delete `credentials.yaml` and restart: `/auth/setup`
opens again. If you no longer trust the old DB (for example, the instance was
shared), stop focacal, delete `/data/focacal.db` and start again. It is
rebuilt as above.

## Note on git history

Before this layout, the repository tracked a root `config.yaml` with a real
bcrypt `password_hash` and `session_token`. The file is gone from the tree,
but that hash is public: it is in earlier commits that are already pushed,
and a bcrypt hash can be attacked offline. Migrating a legacy `config.yaml`
copies that hash into `credentials.yaml`, so **rotate the login right after
migrating**: `POST /auth/reset` with a new password (signed in), or delete
`config/credentials.yaml` and run `/auth/setup` again. Do not reuse that
password anywhere.
