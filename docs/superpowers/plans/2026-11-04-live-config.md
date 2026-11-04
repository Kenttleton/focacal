# Live Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Config edits, from the UI or by hand, take effect in the running process without a restart, except changes to the `server:` block.

**Architecture:** Drop viper: each domain file is decoded strictly with `yaml.v3` over a struct of defaults, environment overrides are one explicit function, and the writer marshals the struct (no comment preservation in the live directory). A `config.Live` holds the current config behind an atomic pointer, reloads on demand, and notifies subscribers on real change. A directory watcher (fsnotify, plus a polling fallback) calls `Live.Reload`. Components read `Live.Current()` at the point of use instead of capturing config at startup.

**Tech Stack:** Go (module `github.com/Kenttleton/focacal`, go 1.26.0), chi, `go.yaml.in/yaml/v3` (fallback: keep `gopkg.in/yaml.v3` if the module cannot be fetched), `github.com/fsnotify/fsnotify`, SQLite store (unchanged).

**Spec:** `docs/superpowers/specs/2026-10-30-calendar-foundation-and-planner-design.md` (sections 4.4, 5.1, 9.5), as extended by `docs/superpowers/plans/2026-11-03-config-foundation.md`. This plan reverses that plan's "changes apply on restart" rule. The decisions below were made with the owner on 2026-11-04.

## Decisions (with the owner, 2026-11-04)

- **Hot reload.** A UI save or a manual edit applies without a restart.
- **`server:` is the only restart-required block.** `pending_restart` means "the `server` block on disk differs from the one the process started with". Nothing else sets it.
- **Domain moves.** `agent` moves from `config.yaml` to `planner.yaml`. `oauth` moves from `config.yaml` to `calendars.yaml`. `config.yaml` keeps `server` and `auth`.
- **No viper.** One YAML package for reading and writing.
- **No comments in the live `config/`.** `config.example/` stays commented for humans. A UI save rewrites the file without comments or unknown keys; document it.
- **Bad manual edits never take the app down.** The last good config keeps running, the error is exposed, and a fixed file recovers.
- **Auth and secrets rules from the earlier plan still hold** (no secrets through the API, `credentials.yaml` rules, endpoints behind auth).

## Global Constraints

- `Config.Server` is the only restart-required block; do not add per-key restart logic.
- Environment overrides still win in the running config: `FOCACAL_<KEY>` with dots as underscores (e.g. `FOCACAL_AGENT_BASE_URL`), and `OAUTH_REDIRECT_BASE_URL`. They are never applied to the file view.
- `*_env` names must match `^FOCACAL_[A-Z0-9_]+$` and file paths must be relative with no `..` on API patches (ruling R12); unchanged.
- `MigrateLegacy` (single-file upgrade) keeps working and may keep using `yaml.Node`; it is not rewritten here.
- Every task ends with `go vet ./...` clean and `go test -race -count=1 ./...` green.

## Review Focus

- A manual edit caught mid-write (empty or truncated file): the last good config keeps running, no panic, and the next complete write is picked up.
- A bad manual edit (YAML syntax error, unknown key, failed validation): the last good config stays, `load_error` is exposed on `GET /config/{domain}`, and fixing the file clears it.
- An editor that replaces the file by rename (new inode): the reload still fires, which is why the directory is watched rather than the file.
- A UI `PATCH` and a hand edit in quick succession: neither change is lost, because every patch reads the file from disk under the writer lock.
- A source removed or changed while a sync is in flight: other sources are unaffected and nothing panics or leaks a connector.
- Deleting `credentials.yaml` reopens `/auth/setup` without a restart, and `auth.enabled` flips live.

---

### Task 1: Move `agent` to the Planner domain and `oauth` to the Calendars domain

**Files:**
- Modify: `internal/config/domain.go`, `internal/config/validate.go`, `internal/config/migrate.go`
- Modify: `config.example/config.yaml`, `config.example/planner.yaml`, `config.example/calendars.yaml`
- Test: `internal/config/domain_test.go`, `internal/config/migrate_test.go`, `internal/config/service_test.go`

**Interfaces:**
- Consumes: `domainKeys`, `Domain.validate`, `ValidateApp/Planner/Calendars`.
- Produces: `DomainForKey("agent") == DomainPlanner`, `DomainForKey("oauth") == DomainCalendars`; `config.yaml` owns only `server` and `auth`.

- [ ] **Step 1: Write the failing tests**
  - `TestDomainForKey_AgentAndOAuth`: `agent` → planner, `oauth` → calendars, `server` and `auth` → app.
  - `TestValidate_AgentLivesInPlanner`: an invalid `agent.timeout_secs` fails `ValidatePlanner` and no longer fails `ValidateApp`; an invalid `oauth.redirect_base_url` fails `ValidateCalendars`. Move the existing agent and oauth cases out of the app validator tests.
  - `TestMigrate_NewLayoutConfigWithAgentKey`: a directory whose `config.yaml` still holds `agent:` and `oauth:` blocks (written by the previous version) and whose `planner.yaml` and `calendars.yaml` are the shipped samples migrates those blocks into `planner.yaml` and `calendars.yaml`, and `config.yaml` ends with only `server` and `auth`. The samples must not make the migration refuse; if the current collision rule would refuse it, narrow the rule to keys actually present in both files with different values.
- [ ] **Step 2: Run them and see them fail** — `go test ./internal/config/ -run 'AgentAndOAuth|AgentLivesInPlanner|NewLayoutConfigWithAgentKey'`.
- [ ] **Step 3: Implement.** Move the `agent` entry (`appAgent` projection, no `api_key`) and the `oauth` entry between `domainKeys` lists; move the validation code between `ValidateApp/Planner/Calendars`; update `legacyDomainKeys`/`LegacyKeys` so `agent` and `oauth` in `config.yaml` are treated as moved keys. Update the three sample files and their header comments (move the commented `agent:` block into `planner.yaml`, `oauth:` into `calendars.yaml`).
- [ ] **Step 4: Run the full suite** — `go test -race -count=1 ./...` passes.
- [ ] **Step 5: Commit** — `git commit -m "refactor(config): agent belongs to planner, oauth to calendars"`.

---

### Task 2: Replace viper with strict yaml.v3 decoding

**Files:**
- Create: `internal/config/load.go`, `internal/config/env.go`
- Modify: `internal/config/defaults.go`, `internal/config/service.go` (`loadFilesWith`, `lowerMapKeys`), `internal/config/startup.go` (`LoadStart`, `bindEnv`), `internal/config/schema.go`, `cmd/root.go`, `go.mod`, `go.sum`
- Test: `internal/config/load_test.go`, `internal/config/env_test.go`; update `roundtrip_test.go`, `service_test.go`

**Interfaces:**
- Produces:
  - `func Defaults() *Config` (the values `applyDefaults` registers today)
  - `func decodeDomain(d Domain, b []byte, into *Config) error` — strict (`KnownFields(true)`), decodes only `d`'s keys; empty input is a no-op; errors never echo values (reuse `redactDecodeError`'s guarantee)
  - `func ApplyEnv(c *Config, getenv func(string) string)` — explicit overrides, one line per supported variable
  - `LoadStart(dir) (started, running *Config, err error)` and `LoadDir(dir) (*Config, error)` keep their signatures and meanings.
- Consumes: `Domains`, `domainKeys`, `Validate()`.

- [ ] **Step 1: Write the failing tests**
  - `TestDecodeDomain_StrictRejectsUnknownKey`: `working_hours: {strt: "09:00"}` returns an error naming the key but not any value.
  - `TestDecodeDomain_OnlyOwnKeys`: a `sources:` block inside `config.yaml` is ignored (as today), while a typo inside an owned key is an error.
  - `TestDefaultsApplyWhenKeyAbsent`: an empty directory loads to exactly `Defaults()`.
  - `TestApplyEnv`: `FOCACAL_AGENT_BASE_URL`, `FOCACAL_AGENT_MODEL`, `FOCACAL_SERVER_REST_PORT` (int parse) and `OAUTH_REDIRECT_BASE_URL` override; an unparseable int is ignored with the file value kept; `started` never carries them.
  - `TestColorsKeysKeepCase`: `colors: {Meeting: "#fff"}` loads as `Meeting` (viper lowercased it). Delete the lowercase and dot-key rejection tests that no longer apply.
  - Every existing roundtrip/service test still passes with viper removed.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** Decode each domain file onto `Defaults()` in `Domains` order. Remove the viper import everywhere, including the `--debug` flag binding in `cmd/root.go` (read the cobra flag value directly), drop all `mapstructure` tags and the `,squash` embeds' tags (use plain embedding with `yaml:",inline"`; `UserConfig.Auth` and `GlobalConfig.Auth` both exist today and an inline decode rejects the duplicate `auth` key, so keep only `GlobalConfig.Auth` and fix its callers), delete `applyDefaults`, `bindEnv`, `lowerMapKeys`. Swap the import to `go.yaml.in/yaml/v3` across the module (`go get go.yaml.in/yaml/v3`, `go mod tidy`); if the fetch fails, keep `gopkg.in/yaml.v3` and say so in the commit message.
- [ ] **Step 4: Verify** — `go mod tidy && go vet ./... && go test -race -count=1 ./...`; `grep -rn viper go.mod go.sum cmd internal` returns nothing.
- [ ] **Step 5: Commit** — `git commit -m "refactor(config): decode with strict yaml, drop viper"`.

---

### Task 3: Map-based merge patch and a plain marshalling writer

**Files:**
- Modify: `internal/config/patch.go`, `internal/config/writer.go`, `internal/config/service.go` (`Patch`, `view`), `internal/config/migrate.go` (keep `findKey`, `removePair` here; they are `yaml.Node` helpers `MigrateLegacy` uses)
- Test: `internal/config/patch_test.go`, `internal/config/writer_test.go`, `internal/config/service_test.go`

**Interfaces:**
- Produces:
  - `func ApplyMergePatch(doc map[string]any, patch map[string]any, c map[string]CollectionSpec) error` — RFC 7396 on plain maps; collections are lists of maps in `doc` and id-keyed objects in `patch` (same `CollectionSpec{Key, Identity}` semantics, `null` deletes, new items append, existing items keep their position)
  - `func (w *Writer) Update(fn func(cur []byte) (next []byte, err error)) error` — lock, read current bytes (missing file = empty), call `fn`, write atomically with the existing rename-then-in-place fallback and `.bak` behavior
  - `func marshalDomain(d Domain, c *Config) ([]byte, error)` — deterministic YAML of exactly `d`'s keys in `domainKeys` order, collections as lists in their existing order
- Consumes: `decodeDomain`, `Defaults`, `Domain.validate`, `ErrValidation`.

- [ ] **Step 1: Write the failing tests.** Port every case in the current `patch_test.go` to maps with the same expectations (scalar replace, nested merge, `null` delete, collection add/update/delete, order kept, destination key `id|calendar_id`). Add: `TestPatchOrderStable` (patching one source leaves list order unchanged), `TestWriterUpdateSeesDiskState` (an external write between two `Update` calls is read by the second), `TestPatchDropsComments` (a commented file loses its comments, keeps values), `TestPatchFailureLeavesFileByteIdentical`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** `Service.Patch`: inside `Writer.Update`, decode the current bytes into `map[string]any` (empty → empty map), `ApplyMergePatch`, marshal back, `decodeDomain` onto `Defaults()` plus the other domains' current files, validate with `d.validate`, then `marshalDomain` for the bytes written. Secret rejection, R12 limits, size limit and error kinds are unchanged. Delete the `yaml.Node` code paths from `patch.go` and `writer.go`.
- [ ] **Step 4: Run the full suite.**
- [ ] **Step 5: Commit** — `git commit -m "refactor(config): merge patch on maps, writer marshals structs"`.

---

### Task 4: `config.Live`: current snapshot, reload, subscribers

**Files:**
- Create: `internal/config/live.go`
- Modify: `internal/config/service.go` (`NewService` takes `*Live`; `View` gains `LoadError string`; `PendingRestart` derives from `Live`), `cmd/root.go`, `cmd/start.go`
- Test: `internal/config/live_test.go`

**Interfaces:**
- Produces:
  - `func NewLive(dir string, log *slog.Logger) (*Live, error)` — first load must succeed (validated), else the error is returned (startup fails fast on a bad config; only reloads are lenient)
  - `func (l *Live) Current() *Config` — running config (files + env, paths resolved under `dir`); never nil; the returned value is immutable by convention
  - `func (l *Live) Reload() (changed bool, err error)` — re-reads all domain files; on error keeps the previous config and records it; on success clears the error; `changed` is true only when the new running config differs (`reflect.DeepEqual`) from the previous
  - `func (l *Live) OnChange(fn func(old, new *Config))` — called after the swap, in registration order, on the goroutine that called `Reload`
  - `func (l *Live) LoadError() error`
  - `func (l *Live) RestartRequired() bool` — the file-level `server` block differs from the one captured at `NewLive`
- Consumes: `LoadDir`-equivalent file loading, `ApplyEnv`, `Config.ResolveFilePaths`, `Validate`.

- [ ] **Step 1: Write the failing tests.** `TestLiveReloadSwapsOnChange`, `TestLiveReloadKeepsLastGoodOnBadYAML` (error recorded, `Current()` unchanged, then a fixed file clears the error and swaps), `TestLiveReloadKeepsLastGoodOnValidationFailure`, `TestLiveOnChangeOnlyWhenDifferent` (rewriting identical bytes does not notify), `TestLiveRestartRequiredOnlyForServer` (changing `agent.model` does not set it, changing `server.rest_port` does, reverting clears it), `TestLiveEnvOverrideSurvivesReload`, `TestServiceGetShowsLoadError`. Run these under `-race` with concurrent `Current()` readers during `Reload`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** Snapshot `{file, running *Config}` behind `atomic.Pointer`; a mutex serializes `Reload`. `Service.Patch` calls `Live.Reload()` synchronously after a successful write, so a later watcher event finds nothing changed and stays silent. `LoadStart` is replaced by `NewLive`; keep `LoadStart` only if `cmd/auth.go` still needs a one-shot load.
- [ ] **Step 4: Run the full suite.**
- [ ] **Step 5: Commit** — `git commit -m "feat(config): Live config with reload, subscribers and restart-required"`.

---

### Task 5: Directory watcher

**Files:**
- Create: `internal/config/watch.go`
- Modify: `cmd/start.go`, `go.mod`
- Test: `internal/config/watch_test.go`

**Interfaces:**
- Produces: `func (l *Live) Watch(ctx context.Context) error` — blocks until `ctx` ends; watches the directory with fsnotify, debounces events 200ms (field `debounce time.Duration`, settable in tests), reacts only to the four known file names (domain files and `credentials.yaml`) and their rename/remove/create events, and runs a poll every 5s (field `pollEvery`) that stats the same files and reloads when mtime, size or existence changed, so bind mounts that drop events still converge. A watcher setup failure falls back to polling alone and logs a warning.
- Consumes: `Live.Reload`.

- [ ] **Step 1: Write the failing tests** (short debounce and poll values): `TestWatchReloadsOnWrite`, `TestWatchReloadsOnRenameReplace` (write temp file, `os.Rename` over the domain file), `TestWatchDebouncesBurst` (ten rapid writes cause one reload), `TestWatchIgnoresUnrelatedFiles` (`*.bak`, `.swp`, other names), `TestWatchSurvivesTruncatedWrite` (empty file then complete file: last good kept, then updated), `TestWatchPollFallback` (watcher creation forced to fail), `TestWatchStopsOnContextCancel` (no goroutine left, checked with a done channel).
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement** the watcher; start it from `cmd/start.go` with the signal context.
- [ ] **Step 4: Run with `-race -count=20` for the watcher tests, then the full suite.**
- [ ] **Step 5: Commit** — `git commit -m "feat(config): watch the config directory and reload on change"`.

---

### Task 6: Consumers read live config

**Files:**
- Create: `internal/connector/registry.go`
- Modify: `cmd/start.go`, `internal/emitter/rest/server.go` and tests, `internal/mcp/server.go` (`NewAgent`), `internal/ui/service.go`, `internal/auth/simple/middleware.go` and tests, `cmd/auth.go`
- Test: `internal/connector/registry_test.go`, `cmd/start_test.go` (reconcile function), plus updated rest and auth tests

**Interfaces:**
- Produces:
  - `connector.Registry`: `NewRegistry() *Registry`, `Get(id string) (Connector, bool)`, `IDs() []string` (sorted), `Snapshot() map[string]Connector`, `Replace(next map[string]Connector)` — all safe for concurrent use
  - `func reconcileConnectors(reg *connector.Registry, old, new *config.Config, build buildFunc, log *slog.Logger) (added []string)` in `cmd/start.go`: builds connectors for sources that are new or whose `SourceConfig` changed (`reflect.DeepEqual`), drops removed ones, keeps untouched ones, logs and skips a source that fails to build
  - `rest.New(reg *connector.Registry, ...)`; `mcp.NewAgent(live *config.Live, mcpPort int, log)` reads `live.Current()` per `Replan`; `ui.Service` takes `*config.Live`; `simple.Middleware(enabled func() bool, apiKey func() string, credentialsPresent func() bool, log)` evaluates `enabled()` per request
- Consumes: `Live.Current`, `Live.OnChange`.

- [ ] **Step 1: Write the failing tests.** `TestRegistryConcurrentGetReplace` (`-race`), `TestReconcileAddsNewSource`, `TestReconcileRebuildsChangedSource`, `TestReconcileKeepsUntouchedConnector` (same instance pointer), `TestReconcileDropsRemoved`, `TestReconcileSkipsFailingBuild` (others unaffected), `TestAgentUsesCurrentPlannerConfig` (model changes between two `Replan` calls), `TestMiddlewareHonorsEnabledToggle` (off then on without rebuilding the handler), `TestSetupReopensWhenCredentialsDeleted`, `TestUISourcesFollowLiveConfig`.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement.** In `cmd/start.go`: `Live.OnChange` calls `reconcileConnectors`, then, for added sources, triggers one `syncAll` for just those sources, and republishes a replan request on the bus when the sources or planner values changed. The 5-minute sync loop reads `live.Current()` and `reg.Snapshot()` every tick; `registerWebhooks` uses `Current().OAuth.RedirectBaseURL`. `server.*` values (ports, bind address, `webhooks_enabled`) are still read once at startup. `ctx` still carries a `*UserConfig`; replace `config.WithUserConfig(ctx, &cfg.UserConfig)` with a getter on `Live` if any caller reads it after startup (check with `grep UserConfigFromContext`).
- [ ] **Step 4: Run the full suite with `-race`.**
- [ ] **Step 5: Commit** — `git commit -m "feat: sources, planner, agent and auth switch follow live config"`.

---

### Task 7: API surface, docs, samples

**Files:**
- Modify: `internal/emitter/rest/config_handlers.go`, `internal/emitter/rest/openapi.json`, `internal/emitter/rest/ui.html` (only if it shows `pending_restart`), `README.md`, `docs/config-domains.md`, `config.example/*.yaml` headers, `docker-compose.yml` comments
- Test: `internal/emitter/rest/config_handlers_test.go`, `desktop/focacal/frontend/src/Settings.jsx` and `App.jsx` (reload banner text only)

**Interfaces:**
- Consumes: `View.LoadError`, `View.PendingRestart`.
- Produces: `GET /config/{domain}` returns `load_error` (empty string when none) and `pending_restart` (only ever caused by `server`); `PATCH` responses carry the same.

- [ ] **Step 1: Write the failing tests.** A handler test that a `GET` returns `load_error` after a hand-written bad file, and that a `PATCH` to `planner` leaves `pending_restart` false while a `PATCH` to `app` that changes `server.rest_port` sets it.
- [ ] **Step 2: Run them and see them fail.**
- [ ] **Step 3: Implement and document.** Docs say: changes apply within about a second, except `server:`; a UI save drops comments and unknown keys from the live file; a bad hand edit keeps the old config and shows an error; the watcher falls back to polling on bind mounts that drop events. Remove "changes apply on restart" text everywhere (`grep -rn "restart" README.md docs/ docker-compose.yml internal/emitter/rest/openapi.json`). The UI shows `load_error` as a warning and `pending_restart` as "restart to apply server changes".
- [ ] **Step 4: Verify** — full suite, `docker compose config -q`, and a manual run: start the app on a scratch config directory, add a calendar via `PATCH`, confirm it appears and syncs with no restart; break `planner.yaml` by hand, confirm `load_error` and that the app keeps running; fix it, confirm recovery.
- [ ] **Step 5: Commit** — `git commit -m "docs: live config semantics; expose load_error"`.
