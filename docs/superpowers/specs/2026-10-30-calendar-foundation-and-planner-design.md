# Focacal: calendar foundation and planner — decision record

Date: 2026-10-30
Status: decision record from a brainstorming session. It has NOT yet had a builder check or final approval as a full spec. Items marked **[guess]** were proposed by the assistant and not explicitly confirmed by the user. Everything else was stated or confirmed by the user.

## 1. Intent and why

Focacal is an AI scheduling assistant in the spirit of Reclaim: a small AI agent that plans a person's week in advance and adjusts the schedule when changes come up, the way a human assistant would. It aggregates many calendars, plans lunch, focus time and transitions around meetings, and surfaces conflicts.

It will be open sourced and shared with coworkers. The owner's own setup is only an example configuration, not the design target.

Why now: the owner is losing Reclaim access on a new work email. The company was acquired and is on Microsoft 365, while the client they work for uses Google. They need both calendars aggregated, with lunch and short breaks planned as the day shifts.

## 2. Goals, non-goals, anti-goals

Goals
- Handle N calendars per person: several work, several personal, several under the same email address, and shared calendars (for example with a spouse).
- Keep a local merged calendar that links the same meeting across sources (dedupe and cross-calendar tracking).
- Raise conflicts between real meetings for manual resolution.
- Plan Routines, Focus time and Transitions around meetings, a week ahead, and re-plan when meetings change.
- Everything is configuration, not hardcoded to one person's setup.

Non-goals (explicitly out)
- External scheduling links.
- A task / to-do list that feeds the scheduler.
- Write access to external calendars in the first release (deferred, see section 3).

Anti-goals
- Copying Reclaim's exact names or exact feature set (see section 7).
- Silently auto-resolving conflicts between real meetings.

## 3. Decomposition and order

Three pieces, designed and built in this order:

1. **Calendar foundation** (read-only): sources and calendars, Google OAuth and ICS inputs, merged local model with link layer, conflict flags, per-calendar settings. Largely built already, needs finishing (section 4.5).
2. **Planner**: Routine / Focus / Transition rules, failure policy, ranking, warnings, agent placement (section 5).
3. **Outputs**: writing to external calendars with dedupe and obscure rules. Deferred because of Microsoft 365 auth concerns.

## 4. Calendar foundation decisions

### 4.1 Sources and calendars
- A **source** is one way of connecting (a Google login, an M365 login, an ICS URL). A source exposes one or more **calendars**. [guess: this exact terminology]
- Google works today through OAuth. ICS links are wanted as a read-only fallback if OAuth is ever unavailable.
- M365 work account is read-only for now. Risk: a company tenant may require admin consent or block third-party apps (affects `Calendars.Read` less than `Calendars.ReadWrite`). Fallback: the work calendar's published ICS feed, which may itself be disabled by tenant policy. Test the consent flow early.
- The client's Google Workspace may also restrict third-party OAuth apps. ICS is the fallback there too.

### 4.2 Per-calendar settings (configurable, per calendar)
- Whether the meeting **title** and/or **body** are shown, set per destination entry (section 4.4). Title and body each support: pass through, rename (for example title shown as "busy"), or leave empty. Default is obscure both.
- Whether Focacal-generated events are added to a calendar is NOT a flag. It is expressed by listing `focacal` as a source under that calendar's destination entry (section 4.4). Absent means no generated events there.
- Colors are NOT per source calendar. Colors follow the **event type** (focus green, lunch reddish, meetings blue as seen in Reclaim). A default palette of 3 to 5 colors, with custom hex or RGB values allowed. [guess: applied in Focacal's own views and feed, configurable]

### 4.3 Local model: link layer and conflicts
- Keep every per-source event row. Do not delete duplicates (today `DeduplicateByUID` deletes rows and cannot match Google to Outlook because the IDs are provider-specific).
- Add a **link layer** that groups rows describing the same meeting. [guess: match on iCalUID first (Google `iCalUID`, Graph `iCalUId`), then a time-and-title heuristic as fallback]
- **Conflicts** are raised for manual resolution. Conflicts are between real meetings, for example two accepted meetings at the same time across calendars. Generated events yield to meetings and are re-planned; that is not a conflict. [guess: the exact conflict taxonomy]

### 4.4 The loop (inputs and outputs)
- All external calendars are **inputs**. Focacal's own calendar is the default **output**.
- Focacal holds two collections: the **merged calendar** (all linked inputs plus generated events) and the **generated events alone**.
- An external calendar can also be an **output**, with:
  - **dedupe**: do not write back events that originated in that calendar;
  - **obscure rules** keyed by source calendar: title, body, or both obscured, default **both**;
  - generated events follow the same rules with "Focacal generated" acting as a source calendar.
- Example from the owner: the work and client calendars sync out as meetings-only (obscured). Generated events stay in Focacal only, since only the owner cares about them.
- **Config shape** (owner, decided 2026-10-30; edited from both `config.yaml` and the web UI, with `config.yaml` as the persisted form):
  - Top-level `sources:` are **accounts** (a Google login, an M365 login, an ICS URL), not individual calendars. Each has a free-text `label`, a `type`, credentials, and the calendars it exposes. The `id` can either be generated by the UI or hand-written in `config.yaml`; either way Focacal validates that ids are unique (owner).
  - `destinations:` is a new top-level list, and each entry points at an individual **calendar** within an account, reusing the input naming: `id` is the account id and `calendar_id` is the calendar. Validation: `calendar_id` must be in that account's `calendar_ids` list. When the account is declared as an input, the type is inferred from it and not repeated. An **export-only** destination (owner, decided 2026-10-30) has no matching input account, so its declaration carries its own connection information (type, credentials, calendar), the same fields an input account would have. Entries in a destination's `sources:` list use the same `id` + `calendar_id` pair.
  - Each destination has a `sources:` list. Each entry refers to an input calendar (or the built-in `focacal` source for generated events) plus that pair's visibility settings. The same input calendar can therefore be shown differently in different destinations.
  - Destination entries have no label of their own: the destination calendar is already named where it is declared as an input.
  - Focacal generates around all inputs, so `focacal` is one source entry. It is listed under a destination only if that calendar should receive generated events. The earlier `generated: true` flag was dropped.
  - Focacal's own calendar is the implicit default destination.
  - Example:

    ```yaml
    destinations:
      - id: "work-google"
        calendar_id: "primary"
        sources:
          - id: "work-outlook"
            calendar_id: "calendar"
            event:
              title:
                visibility: true
                rename: "busy"
              body:
                visibility: false
          - id: "focacal"
            event:
              title:
                visibility: true
              body:
                visibility: false
    ```

  - Title and body settings sit under an `event:` key on each destination source entry (owner, decided). Each of `title` and `body` has a `visibility` bool and an optional `rename` string. The group exists so code can fetch the title/body pair in one read (`*.event`), since operations almost always need both.
  - Semantics (owner, confirmed): `visibility: false` leaves the field empty; `visibility: true` with no `rename` passes the text through; `visibility: true` with `rename` shows the rename text instead. Omitting `event`, `title` or `body` is the same as `visibility: false`, so the default is obscure both.
- Spouse view: busy-only information so the spouse knows when the owner is in meetings. Owner (decided): this is just a destination with a busy-only `event` setup, using the export-only form of destination above. It is not a separate feed type.
- Writing outputs is piece 3 and deferred.

### 4.5 Current state and gaps (from code audit, 2026-10-30; nothing was built or run)
Built: Google connector (OAuth, incremental sync), generic CalDAV connector, Outlook connector (auth, delta sync, write, webhooks), SQLite store, event bus, CalDAV / REST / MCP servers, web UI (`rest/ui.html`), Raycast extension, `mac-menubar/`, `windows-tray/`, retry/back-off, WAL checkpoint, locked blocks, pomodoro/fixed gap logic.

Still needed for the foundation:
- Link layer replacing delete-based dedup.
- Conflict detection and surfacing.
- ICS source type (CalDAV connector is not an ICS poller).
- Per-calendar settings (label, title/body visibility, generated-events switch) and the event-type color palette.
- Outlook reads only the default calendar; `calendar_ids` is ignored on read.
- Outlook delta deletions probably broken: `outlookEvent.AdditionalData` is tagged `additionalData`, but Graph sends `@removed` (`internal/connector/outlook/sync.go:401-410`).
- Disable write-back while read-only (sample `config.yaml` has `writeback: true` on the Google source).

Known write-path bugs (matter when write-back returns, not for read-only):
- Google `WriteEvent` formats times with `fmt.Sprintf("%v", time.Time)`, not RFC3339 (`internal/connector/google/webhook.go:107-108`).
- Cross-busy writes are untracked (no stored remote ID, no managed tag), so every replan creates duplicates (`internal/emitter/writeback/writeback.go:133-141`).
- Neither connector filters its own `X-FOCACAL-MANAGED` events on sync, so written blocks come back as meetings.
- Webhooks cannot work as configured (`redirect_base_url` is `localhost:8080`, routes are on the REST port, Graph needs HTTPS); Google `RegisterWebhook` opens a new channel on every call. Polling every 5 minutes covers it; recommend disabling webhooks for now.

Other:
- `config.yaml` has an `ollama:` block but the schema reads `agent:`, so those values are ignored. It is unverified where `OLLAMA_BASE_URL` is bound.
- No tests anywhere. Project is not in git. Go module path is `github.com/you/focacal`.
- `focacal-roadmap.md` is out of date: Phases 3, 4 and parts of 2, 5, 6, 7 are already implemented.

## 5. Planner decisions

### 5.1 Rule kinds (names chosen by the owner)
- **Routine**: something done regularly within the schedule (lunch, weekly notes/time tracking). Tied to days and a time window, with a duration range and an ideal time.
- **Focus**: the smallest feature set around a single goal: target an amount of focus time per week, prefer large gaps, leave the 30 to 60 minute gaps between meetings alone. Differs in approach from Reclaim. Note: "Focus" is also Reclaim's exact term (flagged to the owner; owner's call).
- **Transition**: what happens between events, configurable how, when and if (for example 5 minutes to decompress after a meeting, skipped silently when meetings are back to back).

### 5.2 Per-rule settings
- **If it can't be placed**: keep it and warn, or drop it. Examples: lunch = keep and warn (health). Weekly notes = drop (do not break working hours). Transition = drop, no warning.
- **Priority** (called "rank" in earlier drafts; renamed by the owner): contested slots between generated blocks are won by priority. Priority applies only to generated blocks; meetings from calendars always win, and Focacal warns when a generated event is impossible to schedule (owner, decided). Scheme (owner, decided 2026-10-30): three MoSCoW-style bands, **Must**, **Should**, **Could**, with a numeric sub-order inside each band borrowed from ABCDE, in descending importance: M1, M2, M3, S1, S2, C1, and so on. "Won't" is dropped because a rule that should not apply simply does not exist. Equal priority is valid: the agent weighs equal-priority rules equally, and if one must be picked it is left to the agent. A user who needs a definite winner should give the rules different priorities. Data model (owner, decided; verbose on purpose, for portability): `priority: {band: "must", order: 1}`. The short form (M1, S2, C1) is not stored; it is generated on the fly where it is shown, and code matches on the long form. Failure policy stays a separate per-rule setting; a Must rule is not forced to keep-and-warn. Other methods the owner raised: the batching technique and Eat That Frog. [My read: these are placement strategies (group similar blocks, put the hardest thing early), so they belong in a rule's placement preference and in agent guidance, not in rank.]
- A rule is one structured definition read by both the UI form and the planning agent. The example rules (work hours 9 to 5 Eastern, 5 minute decompress after meetings, lunch 30 to 60 minutes inside 11 to 2, 30 minutes of notes at end of week) should be expressible in this format.
- Meetings always win. Meetings inside a lunch window are fine because lunch is relative. Moving a task earlier when the conflict is caught early is the correct fix.

### 5.3 The agent is the planner
- Intent (owner, firm): a small AI agent acts as the scheduling assistant. It plans a week ahead and adjusts as meetings change, like a human would. It is not a rules engine replacing the agent.
- Tools support the agent (existing MCP tools such as `focacal_get_free_gaps`), plus a **validator** that rejects invalid plans so the agent can retry. [guess: validator design]
- The model sits behind an OpenAI-compatible endpoint (Ollama / ollama.cpp). Model size is a configuration setting.
- Owner's hope: a small laptop-runnable model (2B, 4B or 6B, possibly one trained for date planning) is enough. Assistant's caution: 2B is at the low end for reliable multi-step tool use; 7B to 14B is more realistic, and the roadmap already noted the current agent sometimes places oversized blocks. This is untested.
- Plan for deciding: at planner time, build a benchmark (sample days as data plus the validator as scorer) and run candidate models through it. The same validator could later generate training data for a task-specific small model (a later project, not in scope now).
- Model research (shortlist) is in `2026-10-30-small-model-research.md` in this folder.

## 6. Out of scope for the first release
- Any write to external calendars (outputs piece).
- Obsidian plugin and other roadmap phase 7 items not yet built.

## 7. Legal / differentiation constraint (the owner's view, not legal advice)
Do not reuse Reclaim's exact names (Habits, Buffers, Focus, its four priority tiers) or its exact feature set. The owner's analogy: Reels and Shorts copied the idea of TikTok's short-form video but differed enough in name and features to be their own products. The same underlying functionality is fine; terminology, structure and settings should be Focacal's own. Apply to rule types, priorities, UI copy and docs.

## 8. Open items
- Which piece to start using first ("smallest version you'd start using on Monday") was asked twice and not answered; resolved by the agreed order: foundation, then planner.
- The exact Focus differences from Reclaim are undecided.
- Per-calendar configuration lives in both `config.yaml` and the UI (decided; see section 4.4). Rule configuration also lives in `config.yaml` (and the UI). `config.yaml` already has a first attempt at working hours, lunch and focus. Owner (decided): Routine and Focus already exist there but need a refactor, and Transition needs adding. Rules follow the same id semantics as sources and destinations: the id can be generated by the UI or hand-written, and is validated unique.
- Where the free-text `label` lives (on the source, or per destination entry) is undecided.
- Conflicts are surfaced in the UI (owner, decided).
- Git init and module path rename are recommended housekeeping, not yet decided.
