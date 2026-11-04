# Focacal: accounts, calendars and destinations — design

Date: 2026-11-04
Status: written from a brainstorming session. Items marked **[call]** were proposed by the assistant and not explicitly confirmed by the owner. Everything else was stated or confirmed by the owner. Not yet approved as a full spec.

## 1. Intent and why

Today `calendars.yaml` has `sources` (one entry per account, with a `calendar_ids` list) and `destinations` (never consumed by the runtime; they are validated, shown in the API and compared for reload only). That shape makes the user enter connection details per role, cannot express "the same calendar is read for planning and also written to", and gives the CalDAV emitter no place in the model.

The owner is a contractor whose calendars change often. Rolling off one client and onto another must be a small edit: removing one calendar must not break everything that references it.

The model below separates what a calendar *is* (entered once) from what focacal *does* with it.

## 2. Goals, non-goals, anti-goals

Goals
- Enter an account's connection details once; add calendars from it and from public links without repeating them.
- Express "read for planning", "written to" and "exported" as relations over the same calendar, not as types.
- Make the CalDAV emitter an ordinary destination, so feeds get the same filtering and visibility as every other destination.
- Removing an account or calendar prunes what depended on it and leaves everything else intact.
- No feedback loops between what focacal writes and what it reads.

Non-goals
- The Settings, Planner and Connections UI rework (separate effort; this spec fixes only the API contract the UI will use).
- The credential store and session design, and how served feeds are authenticated (the auth spec, written next). This spec only reserves the destination field `auth: private | public`.
- Per-calendar colors and the generated-event palette.
- Fan-out: one destination has exactly one target.
- Two-way mirroring between two calendars (rejected by the cycle check, section 7).

Anti-goals
- Silently rewriting or deleting a user's config on a hand edit.
- A single dangling reference rejecting the whole file, which would leave the last good config running forever.

## 3. The model

Three lists in `calendars.yaml`, plus one built-in calendar.

- **accounts** hold authentication and expose zero or more calendars (`google`, `outlook`, `caldav`). They never carry events.
- **calendars** are single calendars. A calendar names either an `account` plus a `remote_id`, or a direct `url` / `url_env` (published ICS or webcal link, no account). Exactly one of the two.
- **destinations** are the relations: `inputs` (calendars, with per-input visibility) flow to one `target`.
- **The built-in `focacal` calendar** is the planner's generated output. It is always present, read-only, and is a valid destination input. The id `focacal` is reserved among calendars.

Roles are relations, not types:
- A calendar feeds the planner when `plan: true` (default `true`, which preserves today's behavior where every source feeds planning). **[call]** The owner accepted "plan in or out" as enough for now; finer per-calendar planning behavior is deferred to the planner phase.
- A calendar is written to when a destination targets it.
- A calendar is exported when it is an input of a destination.
- A calendar can do all three.

```yaml
accounts:
  - id: work-google
    name: Work Google
    type: google                  # google | outlook | caldav
    # connection fields: credentials_file, token_file, caldav_url,
    # username, password, auth_type (unchanged from today's sources)
calendars:
  - id: work
    name: Work
    account: work-google
    remote_id: primary
    plan: true
    conflicts: {all_day: false}   # moved here from sources
  - id: us-holidays
    name: US Holidays
    url: https://example.com/us.ics    # or url_env: HOLIDAYS_URL
    plan: false
destinations:
  - id: focus-to-work             # replaces the old writeback emitter
    name: Focus blocks on Work
    inputs: [{calendar: focacal}]
    target: {calendar: work}
  - id: share-freebusy
    name: Free/busy feed
    inputs:
      - calendar: work
        event:
          title: {visibility: false, rename: Busy}
          body: {visibility: false}
    target: {served: true, auth: private}   # private | public
```

`auth: public` means anyone who can reach the URL. No further rules for now. `private` is the default.

Destination `inputs` may be the shorthand `inputs: all` meaning every calendar plus `focacal`, each with full visibility. **[call]** This lets the seeded default feed stay correct as calendars are added without being rewritten.

A **seeded default destination** `id: focacal`, `target: {served: true, auth: private}`, `inputs: all`, keeps today's CalDAV feed alive. Served feeds are exposed at `/calendar/{id}/`. The older `/focacal/` and `/focacal/calendar/` paths remain as aliases to it. How feeds are authenticated and routed is specified in the auth spec.

## 4. Writability and visibility

- A calendar with only a `url` is read-only. A destination targeting it fails validation.
- An account calendar is writable if the account type supports writing and the remote permission allows it. A target that turns out not to be writable at runtime marks that destination as failed, logged by destination id, without affecting any other destination.
- Per-input visibility reuses today's `EventVisibility` (`title` and `body`, each with `visibility` and optional `rename`). Absent means full visibility.

## 5. Ids and names

- `id` is a server-generated slug, unique within its list. Accounts, calendars and destinations are separate namespaces. `calendars` additionally reserves `focacal`.
- On create, the client sends only `name`. The server derives the slug (`Work Calendar` becomes `work-calendar`, with `-2`, `-3`… on collision) and returns the final id.
- Slug rule for new ids: `[a-z0-9][a-z0-9-]{0,62}`, unique case-insensitively. Existing hand-written ids that do not match keep loading but cannot be created through the API. Today's UUID ids already satisfy the rule.
- Ids are immutable. An edit may change `name` but never `id`. In the API an `id` in an update body must match the target or the request is rejected.
- `name` is free text and may repeat. Anywhere a user sees an entry, show `name`; fall back to the id only when no name is set. Everything that links, keys or looks up uses the id.
- `name` replaces `label`. The loader accepts `label` as an input alias and reads it as `name`; saves write `name` only.
- The API-patch id check that currently rejects ids containing `:` or `|` is re-evaluated: the null-delete check runs before the slug check, so existing odd ids can always be deleted through the API.

The Settings UI changes that follow from this (id removed from create, read-only on edit) belong to the Settings refactor.

## 6. Referential rules

- A calendar's `account` must name an existing account. A destination input's `calendar` and a destination target's `calendar` must name existing calendars (or `focacal` for inputs).
- **Delete through the API** cascades in one atomic file update, and the response lists everything pruned:
  - Deleting a **calendar** removes it from every destination's `inputs`. A destination that targets it is removed too. All other destinations are left exactly as they are. A destination left with no inputs stays (it produces an empty result). **[call]**
  - Deleting an **account** deletes its calendars, then prunes as above.
  - Deleting a **destination** removes only that destination.
  - Stored events and sync state of a removed calendar are purged, using the existing serialized purge: the purge is skipped if the id is configured again.
- A dry run (`?dry_run=true` on the same PATCH) returns what would be pruned without writing, so the UI can show it before confirming. **[call]**
- **Hand edits** are never rewritten by focacal. A dangling reference found on load produces a logged warning and is skipped at runtime: a missing input is ignored, and a destination whose target is missing is inactive. The file stays as written and the last good config is not blocked. **[call]**

## 7. Cycles and feedback loops

Two separate protections.

1. **Config-time cycle check.** Build a graph with an edge from each input calendar to the destination's target calendar. A cycle, including a destination whose target is also one of its own inputs, fails validation with the destination ids on the cycle. `focacal` is a node that has no incoming edges. Cost: two-way mirroring between two calendars (work to personal and back) is not expressible. **[call]**
2. **Runtime write ledger.** The store records, for each destination and each remote event it wrote, a hash of the fields focacal wrote. When ingest later sees a change, it recomputes that hash over the same fields and, if it matches the ledger, treats the change as our own write and ignores it. A real remote edit has a different hash and is processed normally. This stops focacal's own focus blocks from being read back as meetings when the target calendar also has `plan: true`, and covers chains.
   - Hash only the fields focacal writes (start, end, title, body as applied by that destination's visibility), because remotes rewrite timezones, ordering and whitespace.
   - The ledger is runtime state in the database, not config, and is rebuildable.

## 8. Runtime behavior

- Connectors reconcile per account (shared token and rate limit). Sync state and purge are per calendar id.
- The invariants from the live-config work carry over, now keyed by calendar id: sync and purge are serialized per id; a purge is skipped if the id is configured again; a failed rebuild drops the old connector; logs carry ids only, never error text; subscribers never call `Reload`.
- Destinations reconcile as the sources do. A push destination is rebuilt when its target or inputs change. A served destination takes effect on its route without a restart (the port stays in the restart-only `server:` block).
- The planner reads only `plan: true` calendars. Its output is the built-in `focacal` calendar.
- Stored events are keyed by calendar id. Today's key is `{source_id}:{calendar_id}:{uid}` with separate columns; the migration decides how rows carry over (section 9).
- Stored OAuth tokens are keyed by the old source id; the account keeps that id so they survive.

## 9. Migration

Runs once on load of a `calendars.yaml` that still has the legacy `sources` key and no `accounts` or `calendars`. The pre-migration file is kept as a `.bak`.

- Each source becomes an account with `id` equal to the old source id and `name` from `label`, plus its calendars:
  - One calendar per entry in `calendar_ids`. A source with zero or one calendar gives a calendar whose id is the old source id. A source with several gives `<source>-<slug(remote id)>`.
  - Empty `calendar_ids` keeps today's defaults (`primary` for google and outlook). **[call]** The builder confirms what the caldav connector does with an empty list and preserves it.
  - `conflicts` moves to the calendar. `plan` is `true`.
- An `ics` source becomes an account-less calendar with `url` or `url_env`.
- Legacy destinations are not consumed by the runtime today. They are carried over best effort, building accounts and calendars for the `google`, `outlook` and `caldav` types and mapping `sources` to `inputs`. Anything that cannot be mapped (for example an `ics` destination, which cannot be written) is dropped with a startup warning that names the destination id. **[call]**
- Stored events and sync state are caches that can be rebuilt. The migration drops them and lets the next sync repopulate; blocks are kept. **[call]**
- The seeded default destination `focacal` is added if no destination serves that id.

## 10. API contract

All changes go through the existing merge-patch config API for the `calendars` domain.

- Domain keys become `accounts`, `calendars`, `destinations`; `sources` is removed. Collection items are keyed by `id`. The destination key `id|calendar_id` goes away.
- Create sends `name` and the type-specific fields; the response contains the generated `id`.
- Delete is a `null` entry and cascades as in section 6; the response lists what was pruned.
- The view still omits secrets and reports `has_secret`. OpenAPI and `docs/config-domains.md` are updated with the change.

## 11. Left to the builder

- Exact code layout, type names and the store migration steps.
- How the caldav connector maps `remote_id` and what an empty list means.
- Whether the cycle check reports one cycle or all.
- Ledger table design and retention.
- Validation message wording, as long as ids appear and secret values never do.
