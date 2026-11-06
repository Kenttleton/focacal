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
| `config.yaml` | App | `server` (ports, bind address, webhooks), `auth.enabled` | yes |
| `planner.yaml` | Planner | `working_hours` (with the planning time zone), `lunch`, `focus`, `colors` (generated-event palette), `agent` (scheduler endpoint and model, not `api_key`) | yes |
| `calendars.yaml` | Calendars | `accounts`, `calendars`, `destinations` and `oauth.redirect_base_url` (see [The calendar model](#the-calendar-model)) | yes; secrets are references (see below) |
| `credentials.yaml` | (none) | login `username` and bcrypt `password_hash`, mode 0600 | local only, gitignored |
| `*.bak` | (none) | previous file contents from a migration or a fallback write | local only, gitignored |

Each domain is read only from its own file. A `sources:` block left in
`config.yaml` is moved to `calendars.yaml` and converted on start (see
[Migration](#migration-to-the-calendar-model)). A `sources:` key in
`calendars.yaml` itself is converted the same way; if it is still there when
the file is loaded, loading fails with a message telling you to restart.

Values you can set by hand and from the UI are the same values. The UI writes
the file for the domain it edits. Changes apply live (see
[Live reload](#live-reload)), except the `server:` block.

### Live reload

Focacal watches the config directory. A save from the UI, or a hand edit of a
file, applies to the running process within about a second, with no restart.
Accounts, calendars, destinations, planner values (including `agent`), the OAuth redirect
base, `auth.enabled` and the login follow the live config.

- **`server:` needs a restart.** Ports, `bind_address` and `webhooks_enabled`
  are bound at startup. `pending_restart` is true only while the `server:`
  block on disk differs from the one the process started with; nothing else
  sets it. The UI shows "Restart to apply server changes".
- **A UI save rewrites the file.** Comments and unknown keys in the saved
  domain's file are dropped (`config.example/` stays commented). The first
  save of a file also writes every default of that domain into it.
- **A bad hand edit is not applied.** If a file no longer parses or fails
  validation, the previous config keeps running and `GET /config/{domain}`
  returns `load_error` with the reason (secrets are never included) next to the last good values. Fix the file and
  the next reload clears it.
- **Bind mounts that drop events.** Some mounts (certain Docker Desktop and
  network file systems) do not deliver file events. The watcher then falls
  back to polling every 5 seconds.
- **Removing a calendar purges its data.** Deleting a calendar (by hand or in
  the UI, including by cascade from its account) deletes its stored events and
  sync state, and the push ledger rows for it. OAuth tokens are kept per
  account, so re-adding the same account id does not need `focacal auth` again.
- **Resetting the login needs no restart.** Credentials are read per request,
  so deleting `credentials.yaml` reopens `/auth/setup` immediately. Login
  tokens already issued stay valid until the new setup completes (setup
  revokes them all) or the process restarts.

### REST API

Each domain has one read and one write, behind the same auth as the rest of
the API (`GET /openapi.json` has the full schema):

- `GET /config/{domain}` (`app`, `planner`, `calendars`) returns
  `{"values": {...}, "order": {...}, "pending_restart": false, "load_error": ""}`. `values` are
  the file values over the defaults (no environment overrides); collections are
  objects keyed by id and `order` lists their ids in file order (for
  `calendars`: `order.accounts`, `order.calendars`, `order.destinations`).
  Secrets are never returned; accounts and calendars have `has_secret`.
- `PATCH /config/{domain}` takes a JSON merge patch (RFC 7396): send only what
  changes, `null` deletes a key or an item, for example
  `{"working_hours": {"start": "08:30"}}` or
  `{"calendars": {"old-feed": null}}`. It returns the new view. Ids cannot be
  changed (see [Ids and names](#ids-and-names)).
- `POST /config/calendars/{collection}` (`accounts`, `calendars`,
  `destinations`) creates an item from a body with a `name` and no `id`. The
  server generates the id and returns `201 {"id": "...", "view": {...}}`.
- `PATCH /config/calendars?dry_run=true` validates a patch and returns the view
  it would produce, including what a delete would prune, without writing or
  reloading anything (see [Deleting](#deleting-and-cascades)).
  Errors are `{"error": "..."}`: 400 malformed body or unknown key, 404 unknown
  domain, 413 body over 1 MiB, 422 a value fails validation or a secret was
  sent inline (nothing is written), 500 a file on disk could not be read or
  parsed (details in the server log), 503 the server has no config service.
  `colors` keys must be non-empty and must not contain `.`.

### Secrets

Secrets never go into the domain files through the API:

- OAuth client secrets: `.env` (`GOOGLE_CLIENT_ID`, `OUTLOOK_CLIENT_SECRET`, ...)
  or a `credentials_file` reference.
- Feed URLs: a url calendar's `url_env: FOCACAL_<NAME>` with the URL in `.env`.
  An inline `url` or CalDAV `password` written by hand still works.
  docker-compose.yml passes `.env` into the container (`env_file`, optional).
  If the variable is not set, focacal still starts: it logs a warning naming
  the calendar and the variable (never a value), and only that calendar fails
  to sync, with an error naming the variable, until you set it (environment
  changes need a process restart).
  A calendar with neither an `account` nor a `url`/`url_env` is invalid. Pick
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
makes `pending_restart` true. Environment variables are read at process start.

## The calendar model

`calendars.yaml` has three lists, plus `oauth`:

- **`accounts`**: one entry per sign-in (`type` is `google`, `outlook` or
  `caldav`, with `credentials_file`, `token_file` or the CalDAV `caldav_url`
  and `username`). Calendars of one account share its OAuth token, which is
  stored under the account id (`focacal auth <provider> <account-id>`).
- **`calendars`**: what focacal reads. A calendar is either an account
  calendar (`account` names the account, `remote_id` picks the calendar inside
  it; empty means the connector's default calendar) or a read-only feed
  (`url` or `url_env`, no account). `plan` says whether the planner schedules
  around it; it defaults to true.
- **`destinations`**: where focacal publishes. Each has `inputs` and exactly
  one `target`: either `calendar` (push into a writable calendar) or
  `served: true` (serve a feed). A url calendar cannot be a push target.

There is also one built-in calendar, `focacal`. It is not listed in the file:
it stands for the planner's blocks (focus, lunch, break). It can be an input of
a destination, never a target, and no calendar can use the id `focacal`.

The sample ships one active destination, `focacal`: `inputs: all` into a served
private feed. See `config.example/calendars.yaml` for commented examples of an
account, calendars, a push destination and a second served feed.

### Ids and names

- New ids are server-generated slugs of the `name`: lowercase letters, digits
  and dashes (`^[a-z0-9][a-z0-9-]{0,62}$`), with `-2`, `-3` appended on a
  collision. Uniqueness is case-insensitive and per list: accounts, calendars
  and destinations are separate namespaces.
- Ids are immutable. A patch that changes an `id` is refused; delete and
  recreate instead. `id` is not accepted in a create body.
- `name` is the display name and replaces the old `label`. A hand-edited file
  may still say `label`; it loads as `name` (`name` wins if both are present)
  and a save writes `name` only. A hand-written id that does not match the
  pattern still loads, edits and deletes; only creating a new one through the
  API enforces it.
- A reference to an id that does not exist (a calendar naming a missing
  account, an input naming a missing calendar) is a warning in the log on load
  and the entry is skipped; it is not a load error. The API is stricter: a
  patch that leaves a dangling reference is rejected with 422.

### Deleting and cascades

Deleting an account deletes its calendars; deleting a calendar deletes the
destinations that target it and removes it from the `inputs` of other
destinations. Add `?dry_run=true` to the same `PATCH` to see the result first:
the response is the candidate view, with a `pruned` list of
`{"kind": "calendar" | "destination" | "destination_input", "id": "..."}`
(an input has the id `<destination>/<calendar>`). Nothing is written or
reloaded on a dry run. A real delete applies the delete and the cascade in one
write. Only entries that change are touched (a destination left with no inputs after
a delete stays in the file, empty); references you broke by hand
elsewhere are left alone, and a delete still fails while such a reference
exists.

### Inputs and visibility

`inputs` is the string `all` or a list. Each item names a `calendar` (or
`focacal`) and may carry:

- `kinds` (the `focacal` input only): which planner blocks pass, from `focus`,
  `lunch`, `break`. Absent means all kinds.
- `event`: what is shown of that input's events. **Absent means full**, the
  event is published unchanged. **A block that is present is taken literally**,
  with every field you leave out treated as hidden, so
  `title: {visibility: true, rename: Busy}` alone also hides the body.
  `title.visibility: false` shows `Busy`, or `title.rename` when set; a
  `rename` on a visible title replaces it. `body.visibility: false` empties
  the description.

Hiding the title or the body also hides the location and the meeting URL, and
removes the original ids: the published event gets an opaque, stable id and no
source calendar marker, so nothing identifies the origin. The same title rules
apply to planner blocks from the `focacal` input.

`inputs: all` expands to every calendar plus `focacal`, minus the
destination's own target. Within one destination the first item per calendar
wins.

### Cycles

A push destination writes into a calendar that other destinations may read, so
a loop is possible. Validation builds a graph with an edge from each input
calendar to the destination's target (with `all` expanded) and rejects a
config that contains a cycle, naming the destinations involved. The cost is
that **two-way mirroring is not expressible**: `work -> home` plus
`home -> work` is a cycle, and so are two `inputs: all` destinations that
write to two different calendars. A served destination has no outgoing edge,
and `focacal` has no incoming edge, so they never form a cycle.

### Planning

`plan` (default true) on a calendar controls whether the planner schedules
around its events. A calendar that exists only as a push target should be
`plan: false`, so focacal does not plan around its own published copies.

### Served feeds

A served destination publishes a CalDAV and ICS feed at `/calendar/{id}/`
(`GET /calendar/` lists them). `/focacal/` and `/focacal/calendar/` are aliases
for the destination whose id is `focacal` (404 if you removed it), and
`/.well-known/caldav` redirects to `/calendar/`. Feeds are independent: each
one applies only its own inputs and visibility. Changes apply without a
restart.

`auth` on a served target is `private` (the default) or `public`. `private`
does not itself enforce anything: feeds are open when `auth.enabled` is `false`.
**`auth: public` is not available yet** and is rejected by validation until the
auth work ships. Today, whether a feed needs a login is decided by the global
auth middleware (`auth.enabled` in `config.yaml`), the same as the rest of the
API; there is no per-feed authentication.

### Push destinations

A destination with a `calendar` target writes its projected events and blocks
into that calendar after each sync or replan, and records each write in the
`write_ledger` table. The safeguards:

- **Focacal only touches events it wrote.** Updates and deletes go by the
  ledger; events you created in that calendar are never modified.
- **Own writes are not read back.** An event matching a ledger entry is not
  ingested as a new event (this also tolerates providers that truncate bodies
  to 255 characters).
- **History is never deleted.** An item that drops out of the projection is
  deleted remotely only if it starts no earlier than 7 days ago (the push
  window reaches 14 days ahead). Items that started earlier stay, and ledger rows are forgotten without a
  remote delete once the event ended more than 30 days ago.
- **Mass-delete guard.** If one push would delete more than 5 items and more
  than half of the in-window ledger rows, no deletes happen and the destination
  reports a failure. The guard is lifted only for the single push that first
  sees a changed destination config (inputs or target); the next push is
  guarded again. The first push after the process starts is always guarded.
- **Deleted remote events are recreated on change.** If you delete a pushed
  event remotely, focacal creates it again the next time that item changes
  (the update gets "not found", the ledger row is dropped and the item is
  written anew). An unchanged item is not recreated.
- **All-day events are not pushed.** A failing destination does not stop the
  others.
- Removing a destination does not delete what it wrote. Retargeting one
  (changing its target calendar) does: the next push deletes its events in the
  old target calendar that start no earlier than 7 days ago, and writes them to
  the new one.

### Migration to the calendar model

On start, a `calendars.yaml` that has the old `sources` key (and neither
`accounts` nor `calendars`) is converted in place, after the single-file
migration above:

- Each source becomes an account and a calendar. The account id and the first
  calendar id are the old source id; extra `calendar_ids` become further
  calendars with slug ids (`<source>-<remote>`) named `<label> (<remote id>)`.
  No `calendar_ids` means `primary` for Google and Outlook and the default
  calendar for CalDAV. Each calendar gets `plan: true`.
- `ics` sources become account-less url calendars (`url` / `url_env` kept).
- Old destinations become new ones. A destination reuses the calendar of the
  source it pointed at, or, for a Google, Outlook or CalDAV destination with
  its own details, gets a new account and a `plan: false` calendar. Inputs are
  mapped to the new calendar ids, and **inputs are least privilege**: a
  destination that listed no usable inputs gets only `focacal`, never `all`,
  and an input that names the destination's own target is dropped. `ics`
  destinations cannot be converted and are dropped with a warning naming the
  destination id, as are destinations of any other unknown type.
- **`writeback` and `cross_busy` are not converted.** The per-source
  `writeback` / `cross_busy` keys and any top-level `writeback:` block are
  ignored by the migration (and by the loader). If your planned blocks or
  mirrored busy time were written to a real calendar, that stops until you
  create a push destination. For planned blocks, use the built-in `focacal`
  calendar as the input and the calendar to write to as the target:

  ```yaml
  destinations:
    - id: focus-to-work
      name: Focus blocks on my work calendar
      inputs:
        - calendar: focacal
          kinds: [focus, lunch]    # omit to push every kind
      target:
        calendar: work             # a calendar of an account you authorized
  ```

  To mirror busy time from another calendar, add it as an input with an
  `event` block (for example `title: {visibility: false}` for "Busy"). The
  target should be `plan: false`.
- A `focacal` served destination is added if there is none.
- Header comments and `oauth` are kept; comments inside the old entries are
  lost. The output is validated before anything is written, and the original
  is saved as `calendars.yaml.bak` (mode 0600).
- The store's caches are reset once, tied to the database schema version
  (`PRAGMA user_version` 2) rather than to the YAML conversion: events and sync
  state are cleared (so the next sync rebuilds them under the new ids), and each
  block's `source_event_id` and write-back record are cleared. OAuth tokens and
  planner blocks are kept.

A duplicate source id (case-insensitive) stops the migration with an error and
changes nothing.

## What the database holds

`/data/focacal.db` holds rebuildable state only:

| Table | Contents | Rebuilt by |
|---|---|---|
| `events` | events synced from calendars | the next sync |
| `sync_state` | per-calendar sync cursors | a full sync |
| `oauth_tokens` | OAuth refresh tokens | `focacal auth <provider> <account-id>` |
| `blocks` | planner output, plus blocks added or edited in the UI | a replan (UI edits are lost) |
| `conflict_dismissals` | which conflicts you marked as seen | nothing: they show again |
| `write_ledger` | what push destinations wrote, so focacal does not read its own writes back and only ever touches events it wrote | nothing: losing it means pushed events are no longer recognized (see [Push destinations](#push-destinations)) |

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

1. If it holds `working_hours`, `lunch`, `focus`, `colors`, `agent`,
   `sources`, `destinations` or `oauth`, those keys move to `planner.yaml` and `calendars.yaml`,
   comments included. The result is loaded and validated first; if it would
   not load (for example a YAML alias pointing at an anchor in another
   section) or would change any value, nothing is written.
2. If it holds `auth.username` / `auth.password_hash`, they move to
   `credentials.yaml` (created 0600). An existing `credentials.yaml` is never
   overwritten. `auth.session_token` is dropped.
3. The original is kept as `config.yaml.bak` (mode 0600, because it may
   contain the password hash). Unknown keys stay in `config.yaml`.

The migration runs once. After it, nothing is left to move and later starts
do nothing. If `planner.yaml` or `calendars.yaml` already exists, the keys it
lacks are merged into it. If it already holds one of the moved keys with a
different value, startup stops with an error and changes nothing, rather than
silently keep one copy. If the existing file is the sample from the repo,
delete it and start again; otherwise reconcile the values by hand and remove the
keys from `config.yaml`. (A key already present with the same value, left by
an interrupted earlier run, counts as done, so an interrupted migration
resumes.)

### Upgrading a Docker install

The old compose file mounted a single file (`./config.yaml:/data/config.yaml`).
Focacal now refuses to start on that layout: it would otherwise start with no
login, which reopens first-run setup. To upgrade:

```bash
docker compose down
# take the new docker-compose.yml (mounts ./config:/data/config)
# config/ is gitignored and starts empty, so the migration has nothing to
# collide with. If you already ran the new version once, config/ holds
# sample planner.yaml and calendars.yaml: delete those first.
mkdir -p config && mv config.yaml config/config.yaml
docker compose up -d     # migrates config/config.yaml on first start
```

On Linux, make `config/` writable by uid 1001 (`sudo chown -R 1001 config`).

## Porting an instance

1. Copy the config directory (`config/`) to the new host. Include
   `credentials.yaml` to keep the same login, or leave it out to set a new one.
2. Copy `.env` (secrets are not in `config/`).
3. Start focacal, then run `focacal auth <provider> <account-id>` for each
   OAuth account (tokens live in the DB, which you do not copy).
4. The first sync and replan rebuild the DB.

To reset the login, delete `credentials.yaml`: `/auth/setup`
opens again, no restart needed (already issued login tokens stay valid until
the new setup). If you no longer trust the old DB (for example, the instance was
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
