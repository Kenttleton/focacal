# Accounts, Calendars and Destinations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `sources` and `destinations` in `calendars.yaml` with `accounts`, `calendars` and `destinations` (inputs to one target, or a served feed), with server-generated slug ids, cascade deletes, a cycle check and a write ledger.

**Architecture:** The file model changes; the runtime barely does. `Config.Sources` stays but becomes a derived, non-serialized list with one resolved `SourceConfig` per calendar (id = calendar id, account connection fields merged in). Connectors, conflict detection, link ordering, the UI service and `reconcileConnectors` keep consuming it, so an account change rebuilds all its calendars for free and sync state, events and purge stay keyed by calendar id. Destinations are new runtime: a projection (visibility, renames, kinds) feeding a served CalDAV route or a push driver, with a ledger that stops focacal reading back its own writes.

**Tech Stack:** Go, `go.yaml.in/yaml/v3` (strict decode), SQLite store, chi REST, React + Mantine frontend.

**Spec:** `docs/superpowers/specs/2026-11-04-calendar-model-design.md` (section numbers below refer to it). The auth spec comes after this plan and owns feed authentication.

## Global Constraints

- One YAML package: `go.yaml.in/yaml/v3`, strict decode with `KnownFields(true)`. No new config libraries.
- New ids match `^[a-z0-9][a-z0-9-]{0,62}$`, unique case-insensitively per list. Accounts, calendars and destinations are separate namespaces. `focacal` is reserved among calendar ids.
- Ids are immutable. `name` replaces `label`; the loader accepts `label` as an input alias and saves write `name` only.
- Errors and logs never contain secret values and logs carry ids, not error text. Errors from the API keep the existing kinds: `ErrBadRequest` (HTTP 400) and `ErrValidation` (422).
- Hand-edited files are never rewritten by focacal. A dangling reference found on load is a warning and is skipped at runtime, not a load error.
- `auth: public` on a served destination is rejected by validation until the auth spec ships.
- `plan` defaults to true. A destination has exactly one target.
- Config subscribers never call `Live.Reload`. Sync and purge stay serialized per id.
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- `go vet ./...` and `go test -race -count=1 ./...` pass at the end of every task.

## Review Focus

Behaviors the spec implies that no task's headline tests cover most directly; each is pinned by a named test in the owning task.

1. A `calendars.yaml` that still has a top-level `sources:` key reaches the runtime (hand-pasted after startup): clear load error, never a silently empty config. (Task 2)
2. A calendar is deleted and re-added while its sync is in flight: the purge is skipped and the destination cascade does not prune the new entry. (Task 3, Task 5)
3. `inputs: all` on a destination whose target is a calendar must exclude that target instead of failing the cycle check. (Task 1, Task 2)
4. A migrated multi-calendar Google account keeps its OAuth token and sync state; two remote ids that slug to the same string get distinct calendar ids. (Task 4, Task 5)
5. A name that slugs to nothing (`日本語`, `!!!`) or to a reserved or taken id still creates an entry with a valid id. (Task 1)

---

### Task 1: Types, slug ids and the resolver

**Files:**
- Modify: `internal/config/schema.go` (new types; `UserConfig` fields; `SourceConfig` additions)
- Create: `internal/config/ids.go`, `internal/config/resolve.go`
- Test: `internal/config/ids_test.go`, `internal/config/resolve_test.go`

**Interfaces:**
- Produces (all in package `config`):
  - `AccountConfig{ID, Name, Type, CredentialsFile, TokenFile, CalDAVURL, Username, Password, AuthType string}`: yaml and json tags are the same snake_case names `SourceConfig` uses today, with `name` in place of `label`; `type` is `google | outlook | caldav`.
  - `CalendarConfig{ID, Name, Account, RemoteID, URL, URLEnv string; Plan *bool; Conflicts ConflictConfig}` with yaml tags `id,name,account,remote_id,url,url_env,plan,conflicts`; `func (c CalendarConfig) Plans() bool` (nil means true).
  - `DestinationConfig{ID, Name string; Inputs InputList; Target DestinationTarget}`; `InputList{All bool; Items []DestinationInput}` marshals as the scalar `all` or a list; `DestinationInput{Calendar string; Event EventVisibility; Kinds []string}`; `DestinationTarget{Calendar string; Served bool; Auth string}` (`Auth` is `private | public`, empty means private).
  - `UserConfig` gains `Accounts []AccountConfig`, `Calendars []CalendarConfig` (yaml `accounts`, `calendars`); `Sources []SourceConfig` becomes `yaml:"-"` (derived); `Destinations` keeps its key with the new type.
  - `SourceConfig` gains `AccountID string` (`yaml:"-" json:"-"`) and `Plan bool` (`yaml:"-" json:"plan"`).
  - `func Slug(name string) string`; `func UniqueID(name string, taken func(string) bool) string`; `func ValidID(id string) bool`.
  - `func (c *Config) Resolve()` fills `c.Sources`; `func (s SourceConfig) TokenKey() string` returns `AccountID` when set, else `ID`; `func (c *Config) PlanSourceIDs() map[string]bool`; `func (c *Config) ExpandInputs(d DestinationConfig) []DestinationInput`.

- [ ] **Step 1: Write failing tests.**
  - `TestSlug`: `"Work Calendar"` gives `work-calendar`; runs of non-alphanumerics collapse to one `-`; leading and trailing `-` are trimmed; the result is cut at 63 characters; names with no ASCII alphanumerics (`日本語`, `!!!`, empty) give `calendar`.
  - `TestUniqueID`: collisions append `-2`, `-3`; the comparison is case-insensitive; the result is never `focacal` for the calendars namespace (pass a `taken` that reports it).
  - `TestValidID`: UUIDs and `work-google` are valid; `Work`, `a:b`, `a|b`, `-x`, 64 characters are not.
  - `TestResolveAccountCalendars`: one account plus two calendars gives two `SourceConfig`s whose `ID` is the calendar id, `Label` the calendar name, `Type` the account type, connection fields from the account, `CalendarIDs == []string{remote_id}`, `AccountID` the account id, `Plan` from `Plans()`, `Conflicts` from the calendar.
  - `TestResolveURLCalendar`: a calendar with `url`/`url_env` and no account gives `Type == "ics"`, `ICSURL`/`ICSURLEnv` set, `AccountID == ""`.
  - `TestResolveDanglingAccountSkipped`: a calendar naming a missing account is omitted from `Sources` (validation reports it separately in Task 2).
  - `TestExpandInputsAllExcludesTarget`: `inputs: all` with target calendar `work` expands to every calendar except `work` plus `focacal`, each with zero-value (full) visibility.
- [ ] **Step 2:** Run `go test ./internal/config -run 'TestSlug|TestUniqueID|TestValidID|TestResolve|TestExpandInputs'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement the types and functions above.**
  - `Resolve` runs at the end of `loadFiles`, and again after `ResolveFilePaths` and `ApplyEnv` change account fields (those must now act on `Accounts` and `Calendars` instead of `Sources`; `envref.go` keeps reading the derived `Sources`).
  - Custom `UnmarshalYAML`/`MarshalYAML` and `UnmarshalJSON`/`MarshalJSON` on `InputList`: the scalar string `all`, or a sequence of `DestinationInput`; any other scalar is an error.
- [ ] **Step 4:** Rerun the command; all PASS. Run `go build ./...` (it must compile: nothing else changes yet).
- [ ] **Step 5: Commit** `feat(config): account, calendar and destination types with slug ids and resolver`.

### Task 2: Loading, aliases and validation

**Files:**
- Modify: `internal/config/domain.go` (calendars keys), `internal/config/patch.go`, `internal/config/load.go`, `internal/config/validate.go`, `internal/config/startup.go`, `internal/config/service.go` (only what the compiler forces; the API work is Task 3), `config.example/calendars.yaml`
- Test: `internal/config/validate_test.go`, `load_test.go`, `domain_test.go`, plus every existing test fixture that has `sources:` (move them to the new shape; add a small test helper that builds a calendars YAML)

**Interfaces:**
- Consumes: Task 1 types.
- Produces:
  - `domainKeys[DomainCalendars]` is `accounts`, `calendars`, `destinations` (all id-keyed collections), `oauth`. `sources` is gone.
  - `var IDSpec = CollectionSpec{...}` (key and identity on `id`), used by all three collections. `DestinationsSpec`, `SourcesSpec` and `DestinationKey` are removed.
  - `(*Config).ValidateCalendars() error`.

- [ ] **Step 1: Write failing tests.**
  - `TestDecodeRejectsLegacySourcesKey` (Review Focus 1): a calendars file with `sources:` fails with an error naming the key and saying to restart so focacal can migrate; the message contains no values.
  - `TestDecodeAcceptsLabelAlias`: `label:` on an account, calendar or destination decodes into `Name`; `marshalDomain` writes `name` only.
  - `TestValidateCalendars` table cases, each asserting the error text contains the id and no secret:
    - duplicate id in a list; account `type` not in `google|outlook|caldav`;
    - calendar with both `account` and `url`, or neither; `account` set without `remote_id` is allowed (means the connector default) but a missing account is an error; calendar id `focacal` reserved;
    - destination input naming a missing calendar; input `focacal` valid; `kinds` containing anything but `focus|lunch|break`; `kinds` on a non-`focacal` input is an error;
    - target exactly one of `calendar` or `served`; a url-only target calendar fails (not writable); a `plan: false` target is valid;
    - `auth: public` rejected with a message that it is not available yet; `auth` valid only with `served`;
    - cycle check: a destination whose target is also one of its inputs, and a two-destination chain A→B and B→A, each report the destination ids on the cycle; `inputs: all` with a calendar target does not cycle (Review Focus 3).
  - `TestDanglingReferencesLoadWithWarning` / `TestDanglingReferencesRejectedByPatch`: a hand-edited file with a missing account, input or target loads (warning logged, entry skipped at runtime, file untouched), while the same dangling reference in an API patch fails with `ErrValidation`. Implement as two entry points: `ValidateCalendars` (strict, used by `Service.Patch`) and a loose variant without the reference errors (used on load).
- [ ] **Step 2:** Run the new tests. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - `load.go`: `renameKeys` pre-decode step (`label` to `name` on items of `accounts`, `calendars`, `destinations`) next to `dropObsoleteKeys`; for the calendars domain, a top-level `sources` key is the legacy error above (other domains still ignore stray keys). `obsoleteItemKeys` handling stays for the migration's reader.
  - `startup.go`: `ResolveFilePaths` resolves `Accounts[i].CredentialsFile` and `TokenFile`, then calls `Resolve`.
  - `validate.go`: the new validator as above, with the cycle check as a DFS over edges input calendar to target calendar (served targets have no outgoing edge).
  - Dangling references: `loadFiles` runs the loose variant and logs warnings through the existing logger seam; the skipped entries are filtered inside `Resolve`/`ExpandInputs` and in the runtime in later tasks.
  - `config.example/calendars.yaml`: commented examples of an account, a calendar, a url calendar, a push destination and a served destination.
- [ ] **Step 4:** `go test ./internal/config ./cmd` PASS (existing tests fixed for the new fixtures).
- [ ] **Step 5: Commit** `feat(config): calendars domain keys, validation and cycle check`.

### Task 3: Service and REST API: create, immutable ids, cascade delete

**Files:**
- Modify: `internal/config/service.go` (views, `checkItem`, `Patch`), `internal/emitter/rest/config_handlers.go`, `internal/emitter/rest/server.go` (route), `internal/emitter/rest/openapi.json`
- Create: `internal/config/cascade.go`
- Test: `internal/config/service_test.go`, `internal/config/cascade_test.go`, `internal/emitter/rest/config_handlers_test.go`

**Interfaces:**
- Consumes: `IDSpec`, `UniqueID`, `ValidID`.
- Produces:
  - `type Pruned struct{ Kind, ID string }`; `View` gains `Pruned []Pruned`.
  - `func (s *Service) Create(coll string, body []byte) (id string, v View, err error)` for `accounts`, `calendars`, `destinations` in the calendars domain. The body is the item without `id`; `name` is required.
  - `func (s *Service) Patch(d Domain, body []byte, opts PatchOptions) (View, error)` with `PatchOptions{DryRun bool}`. A dry run runs everything except the write and returns the `Pruned` list.
  - `func pruneCascade(doc map[string]any, deleted map[string][]string) []Pruned`: operates on the merged document map inside `Writer.Update`, so one atomic write covers the delete and its cascade.
  - REST: `POST /config/calendars/{collection}` returns `201 {"id": ..., "view": ...}`; `PATCH /config/calendars?dry_run=true`.

- [ ] **Step 1: Write failing tests.**
  - `TestCreateGeneratesSlugID`: creating a calendar named `Work Calendar` returns `work-calendar`; the second with the same name returns `work-calendar-2`; the name `focacal` (and `日本語`) yields a valid non-reserved id (Review Focus 5); a body containing `id` is rejected (`ErrBadRequest`); a missing `name` is `ErrValidation`.
  - `TestPatchIDImmutable`: a patch whose item `id` differs from its key is rejected; changing `name` is allowed.
  - `TestDeleteCalendarPrunesDestinations`: deleting calendar `work` removes it from every destination's `inputs`, removes destinations whose target is `work`, and leaves every other destination byte-for-byte the same; a destination left with no inputs stays; `View.Pruned` lists the removed input references and destinations.
  - `TestDeleteAccountCascades`: deleting an account deletes its calendars and then prunes as above, in one write (assert a single `Writer.Update` call and one `.bak`).
  - `TestDeleteOddIDNotBlocked`: a hand-written source id `a:b` can be deleted (the null-delete check runs before the slug and `:`/`|` checks); creating one is still rejected.
  - `TestDryRunWritesNothing`: file bytes and mtime are unchanged and `Pruned` is populated.
  - `TestViewsHideSecrets`: account views omit `password` and report `has_secret` using the existing `hasSecret` rules (now over `AccountConfig`, `URL`, `URLEnv`); calendar views redact `url` userinfo; destination views carry `name`, `inputs`, `target`.
  - REST: `POST` returns 201 with the id; `PATCH ?dry_run=true` returns 200 and does not change the file; both stay behind the existing auth middleware.
- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Replace `sourcesView` and `destinationsView` with one generic id-keyed view builder.
  - `checkItem`: move the `e == nil` (delete) branch before the id-shape checks; the id-shape check for creates is `ValidID`.
  - `Create` takes the writer lock via `Update`, derives the id with `UniqueID` against the ids in the document (case-insensitive; reserved `focacal` for calendars), and runs the same validate-then-write path as `Patch`.
  - `pruneCascade` order: accounts, then calendars, then destinations; a destination whose target was deleted is removed, an input of a deleted calendar is dropped, nothing else is touched. Hand-edited dangling references are not cleaned up here.
  - OpenAPI documents `POST`, the `dry_run` parameter, `Pruned`, the `name`/`id` rules and the removal of `sources`.
- [ ] **Step 4:** `go test -race ./internal/config ./internal/emitter/rest` PASS.
- [ ] **Step 5: Commit** `feat(config): create with generated ids, immutable ids, cascade delete and dry run`.

### Task 4: Migration

**Files:**
- Modify: `internal/config/migrate.go`, `cmd/root.go` (call after `MigrateLegacy`), `internal/store/db.go` (schema version)
- Test: `internal/config/migrate_test.go`, `internal/store/db_test.go`

**Interfaces:**
- Produces:
  - `func MigrateCalendarModel(dir string) (migrated bool, err error)`: idempotent; runs on a `calendars.yaml` that has `sources` and neither `accounts` nor `calendars`.
  - Store: `PRAGMA user_version` goes from 0 or 1 to 2. On that step, once, delete all `events` and `sync_state` rows, set `blocks.source_event_id` to NULL and `blocks.written_back` to `'{}'`. `oauth_tokens` and `blocks` are kept.

- [ ] **Step 1: Write failing tests.**
  - `TestMigrateSingleCalendarSource`: a google source `work` with no `calendar_ids` gives account `work` (name from `label`, connection fields) and calendar `work` (`remote_id: primary`, `plan: true`, `conflicts` moved); ids and the `.bak` are as specified.
  - `TestMigrateMultiCalendarSource` (Review Focus 4): `calendar_ids: [primary, team@x.com, team@x.com2]` gives calendars `work`, `work-team-x-com`, `work-team-x-com-2` (distinct ids, slug collisions suffixed), all with `account: work`.
  - `TestMigrateICSSource`: becomes an account-less calendar with `url` or `url_env` (old `ics_url`, `ics_url_env`), id unchanged.
  - `TestMigrateCalDAVEmptyListKeepsDefault`: an empty `calendar_ids` gives one calendar with no `remote_id`.
  - `TestMigrateDestinationsBestEffort`: a `google`/`outlook`/`caldav` destination becomes account plus calendar plus destination (inputs from its `sources`, target the new calendar); an `ics` destination is dropped with a warning naming its id; a destination that matched an existing source reuses that source's calendar.
  - `TestMigrateAddsDefaultDestination`: when no destination serves `focacal`, one is added (`served`, `private`, `inputs: all`).
  - `TestMigrateIdempotentAndAtomic`: a second run returns `false` and writes nothing; a failing write leaves the original file and returns an error naming the file; a read-only directory refuses startup path (error, no panic).
  - `TestStoreVersion2ResetsCaches`: a v1 database with events, sync_state, blocks and an oauth token ends with no events or sync_state, blocks kept with null `source_event_id` and `'{}'` `written_back`, the token kept, and `user_version == 2`; reopening does not repeat the reset.
- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3: Implement** with the same atomic-writer and `.bak` pattern as `MigrateLegacy`. `cmd/root.go` calls `MigrateCalendarModel` after `MigrateLegacy` and before `SeedDefaults`/`NewLive`; an error stops startup with the file named.
- [ ] **Step 4:** `go test -race ./internal/config ./internal/store ./cmd` PASS. Also run the migration over a copy of `config.example` from the pre-change commit (use `git show <commit>:config.example/calendars.yaml`) and confirm it loads.
- [ ] **Step 5: Commit** `feat: migrate calendars.yaml and the store to the calendar model`.

### Task 5: Runtime consumers of the derived sources

**Files:**
- Modify: `internal/connector/google/auth.go`, `internal/connector/google/webhook.go`, `internal/connector/outlook/auth.go`, `internal/connector/caldav/sync.go`, `internal/mcp/server.go`, `internal/ui/service.go`, `cmd/auth.go`, `cmd/start.go` (comments and tests only unless a test shows otherwise)
- Test: `cmd/start_test.go`, `internal/mcp/server_test.go`, `internal/ui/service_test.go`, connector tests

**Interfaces:**
- Consumes: `SourceConfig.TokenKey()`, `Config.PlanSourceIDs()`.

- [ ] **Step 1: Write failing tests.**
  - `TestGoogleOutlookUseTokenKey`: with `AccountID: "work"` and `ID: "work-team"`, `GetOAuthToken`/`SetOAuthToken` use `work`; with no `AccountID` they use the calendar id (existing behavior).
  - `TestCalDAVSyncFiltersByRemoteID`: with `CalendarIDs == ["/cal/a/"]` only that collection is synced; with none, all are (today's behavior). Webhook channel tokens and `sync_state` keys still use the calendar id.
  - `TestReconcileAccountChangeRebuildsItsCalendars`: changing an account's `password` rebuilds every calendar of that account and none of another account's; removing a calendar drops and purges only it; a delete then quick re-add skips the purge (Review Focus 2). These drive `reconcile` with configs produced by `Resolve`.
  - `TestMCPListEventsExcludesNonPlanCalendars`: events of a `plan: false` calendar are absent from `list_events` and from `computeGaps`, present in the REST event listing and the UI.
  - `TestUISourcesCarriesNameAndPlan`: `/ui` sources entries keep `id`, `type`, `calendar_ids`, and `label` is the calendar `name`; add `account` and `plan`.
  - `TestAuthCmdResolvesAccountOrCalendarID`: `focacal auth <id>` accepts an account id or a calendar id of an OAuth account and uses `TokenKey()`.
- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3: Implement** as above. The mcp filter reads `uc.PlanSourceIDs()` from the context `UserConfig` (the planner reads through MCP, so this satisfies "the planner reads only plan calendars"); the REST and UI listings are unfiltered.
- [ ] **Step 4:** `go test -race ./...` PASS.
- [ ] **Step 5: Commit** `feat: account-shared tokens, caldav remote filter and plan filtering`.

### Task 6: Projection and served destinations

**Files:**
- Create: `internal/destination/project.go`
- Modify: `internal/emitter/caldav/server.go`, `internal/emitter/caldav/merge.go`, `cmd/start.go`, `internal/config/seed.go`
- Test: `internal/destination/project_test.go`, `internal/emitter/caldav/server_test.go`, `cmd/start_test.go`

**Interfaces:**
- Produces:
  - `func Project(cfg *config.Config, d config.DestinationConfig, events []*store.Event, blocks []*store.Block) ([]*store.Event, []*store.Block)`: pure, applies `ExpandInputs`, drops events of calendars not in the inputs (and dangling ones), applies per-input `title`/`body` visibility and `rename` (a hidden title becomes the rename, or `Busy` if none), and keeps only blocks whose type is in the `focacal` input's `kinds` (all when empty). Returned values are copies.
  - `caldav.Server` serves each served destination at `/calendar/{id}/` (PROPFIND, REPORT, GET, OPTIONS as today), `/calendar/` lists the served destinations, `/.well-known/caldav` redirects to `/calendar/`, and `/focacal/` and `/focacal/calendar/` alias the destination with id `focacal`. The display name is `Destination.Name` or the id. The server reads destinations from `*config.Live` on each request, so edits apply without a restart.
  - Auth stays today's global middleware (the auth spec changes it). Do not add per-destination checks here.
  - `SeedDefaults` and the example config seed the default `focacal` destination if the migration did not already.

- [ ] **Step 1: Write failing tests.**
  - `TestProjectVisibility`: hiding the title replaces it with `Busy`; `rename` wins; hidden `body` is empty; an input not listed contributes nothing; a missing-calendar input is skipped.
  - `TestProjectKinds`: `kinds: [focus]` keeps focus blocks only; empty keeps all.
  - `TestProjectDoesNotMutateInputs`.
  - `TestServedFeedsAreIndependent`: two destinations, one full and one `title: Busy`, return different bodies at their own paths from the same store; an unknown id is 404; the alias paths return the `focacal` destination's feed; `/calendar/` lists both names.
  - `TestServedDestinationAppliesWithoutRestart`: after a `Live` swap that adds a destination, its path answers 200 on the next request.
- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3: Implement.** `BuildICS` keeps its signature; the server projects first, then builds. Per-feed `getctag` is a hash of the projected output.
- [ ] **Step 4:** `go test -race ./...` PASS.
- [ ] **Step 5: Commit** `feat: served destinations with per-feed projection and visibility`.

### Task 7: Push destinations and the write ledger

**Files:**
- Create: `internal/destination/push.go`
- Modify: `internal/store/db.go` (table and methods), `internal/store/events.go` (ingest suppression), `cmd/start.go`
- Delete: `internal/emitter/writeback/` (dead code; no import remains)
- Test: `internal/destination/push_test.go`, `internal/store/ledger_test.go`

**Interfaces:**
- Produces:
  - Table `write_ledger(calendar_id TEXT, remote_id TEXT, destination_id TEXT, hash TEXT, written_at DATETIME, PRIMARY KEY(calendar_id, remote_id))`. Keyed by the target calendar id because ingest knows the event's calendar id, and a destination has exactly one target. Rebuildable state.
  - `func (db *DB) LedgerGet(ctx, calendarID, remoteID string) (hash string, ok bool, err error)`, `LedgerPut(ctx, calendarID, remoteID, destinationID, hash string) error`, `LedgerDelete(ctx, calendarID, remoteID string) error`.
  - `func FieldHash(title, body string, start, end time.Time) string` in `internal/destination`: trims and whitespace-collapses the title, reduces the body to plain text, converts start and end to UTC, then SHA-256 (spec section 11).
  - `func (db *DB) UpsertEvent` skips (and does not store) an incoming event whose `FieldHash` equals the ledger entry for its calendar id and remote uid; a different hash proceeds normally and the ledger entry is left for the next push to refresh.
  - `type Pusher` with `Notify(ctx) error`, built over `*config.Live`, the registry and the store. For each destination with a calendar target it projects, writes new or changed items through the target connector's `WriteEvent` (using the stored remote id), deletes items no longer produced via `DeleteEvent`, and records each write in the ledger. A target that is not writable, or whose connector failed to build, fails only that destination and logs its id.
  - Runs on `EventBlocksUpdated` and `EventCalendarChanged`.

- [ ] **Step 1: Write failing tests.**
  - `TestFieldHashNormalizes`: whitespace, HTML body and timezone differences hash equal; any real change differs.
  - `TestIngestSkipsOwnWrite`: after `LedgerPut` for a written event, an incoming event with matching fields is not stored; a user edit (different title) is stored.
  - `TestPlanTrueTargetDoesNotReadBackFocusBlocks`: the end-to-end loop case from spec section 7 with a fake connector, a `plan: true` target and a `focacal` input: after a push and a sync that returns the written event, `ListEvents` shows no such event and the next replan input is unchanged.
  - `TestPusherWritesAndUpdates`: second `Notify` with an unchanged projection writes nothing; a changed block updates by stored remote id; a removed block calls `DeleteEvent`.
  - `TestPusherIsolatesFailures`: a failing destination does not stop others; a url-only or missing target logs the id only (no error text).
  - `TestCrossDestinationChainNoLoop`: A→B and B→C (valid, no cycle) with fake connectors settle after one round (ledger suppresses the echo at each hop).
- [ ] **Step 2:** Run them. Expected: FAIL.
- [ ] **Step 3: Implement.** Remove the writeback package and its tests; nothing imports it. Wire the `Pusher` in `cmd/start.go` as another subscriber with the same ordered, never-`Reload` rules as the existing ones.
- [ ] **Step 4:** `go test -race -count=3 ./internal/destination ./internal/store` then `go test -race ./...` PASS.
- [ ] **Step 5: Commit** `feat: push destinations with a write ledger; remove dead writeback emitter`.

### Task 8: Docs, example config and the frontend

**Files:**
- Modify: `docs/config-domains.md`, `README.md`, `config.example/*.yaml` (final pass), `desktop/focacal/frontend/src/Settings.jsx`, `Sidebar.jsx`, `App.jsx`
- Test: frontend build; `internal/config` example-config test

- [ ] **Step 1:** Add a test that `config.example/` seeds, loads and validates with every example uncommented where it must be valid (reuse the existing example-load test).
- [ ] **Step 2: Docs.** `docs/config-domains.md` and the README describe the model, the id and name rules, cascade delete with dry run, served feeds at `/calendar/{id}/` with the alias, the cycle rule and its cost (no two-way mirroring), `plan`, and the migration. Say plainly that `auth: public` is not available until the auth work ships.
- [ ] **Step 3: Frontend, minimal.** The UI consumes the derived `/ui` sources, so the sidebar, event labels and colors keep working (check `label` is now the calendar name). Settings: replace the sources and destinations editors, which patch the removed `sources` key, with read-only lists of accounts, calendars and destinations (name, with the id shown read-only) and a delete action that calls `PATCH ?dry_run=true`, shows the pruned list, and confirms. **Adding and editing accounts, calendars and destinations in the UI is intentionally left to the Settings refactor**; say so in the PR and the Settings page text. Do not add id fields to any create form.
- [ ] **Step 4:** `cd desktop/focacal/frontend && npm ci && npm run build`, then `go test -race ./...` and `go vet ./...` PASS.
- [ ] **Step 5: Commit** `docs,ui: calendar model docs and read-only settings lists`.
