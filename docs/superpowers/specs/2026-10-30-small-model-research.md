# Focacal: small model research for the planner agent

Date: 2026-10-30
Status: desk research only. Nothing here has been run against Focacal's task. Scores are mostly vendor-reported, come from mixed benchmark versions, and are NOT comparable across rows. Treat every entry as a lead to test, not a recommendation.

Companion to `2026-10-30-calendar-foundation-and-planner-design.md` (section 5.3).

## 1. Question

Is there an open model of roughly 2B to 6B parameters, ideally trained for date or schedule planning, that can run on a laptop via Ollama / ollama.cpp and act as Focacal's planning agent (multi-step tool calls against MCP tools such as `focacal_get_free_gaps`, plus a validator)?

## 2. Headline finding

No publicly released model trained specifically for calendar scheduling or planning at 4B to 6B was found. This is a negative result from limited searching, not proof none exists. The nearest leads are a fine-tuning recipe (PRIMETIME) and a temporal-reasoning model (Time-R1, 3B) that targets temporal prediction rather than calendar scheduling.

The realistic options are:
1. A general small model with good tool calling (shortlist below), benchmarked on Focacal's own task.
2. Later: fine-tune a small base model using the validator as a data generator (a separate project, not in scope now).

## 3. Shortlist

| Model | Size | License | Ollama | Reported tool-use signal | Notes |
|---|---|---|---|---|---|
| Qwen3.5-9B | 9B | Apache-2.0 | `qwen3.5` | BFCL-V4 66.1, TAU2 79.1 (vendor-reported) | Strongest numbers, but above the 6B hope. |
| Qwen3 4B / 8B / 14B | 4B/8B/14B | Apache-2.0 | `qwen3` (about 2.5 / 5.2 / 9.3 GB) | 4B-Instruct-2507 card: BFCL-v3 61.9, TAU2-Retail 40.4, TAU2-Airline 24.0 | Best fit for the "4B" hope; 8B and 14B are fallbacks. Current default in `defaults.go` is `qwen2.5:14b`. |
| Gemma 4 E4B / E2B | about 4B / 2B effective | Apache-2.0 | `gemma4` | Native function calling; Tau2 42.2% (E4B) | No BFCL score found. |
| Granite 4.0 Micro / H-Tiny | 3B / 7B | Apache-2.0 | `granite4` | BFCL v3 about 59.98 (Micro), 57.65 (H-Tiny); H-Small 32B 64.69 | Small and tool-oriented. |
| Phi-4-mini-instruct | 3.8B | MIT | unchecked | none found | Ollama availability not verified. |
| xLAM-2-8b-fc-r | 8B | CC-BY-NC | GGUF only | BFCL v3 72.83% (search snippet, unverified) | Non-commercial license conflicts with open sourcing. |
| Hammer2.1 | 0.5B to 7B | CC-BY-NC | unchecked | no numbers | Non-commercial license. |

## 4. Closest to "trained for date planning"

- **PRIMETIME recipe** (arXiv 2504.16155): Qwen2.5 plus LoRA, reported about 99.3% on an event-planning task and perfect date parsing. This is a recipe, not a ready-made model. It is the most relevant evidence that a small tuned model can do this class of task.
- **Time-R1 (3B)**: targets temporal prediction and reasoning, not calendar scheduling. Possibly useful as a base, unproven for our task.

## 5. Caveats

- Public benchmarks (BFCL, tau-bench) measure tool-call correctness and dialogue policy. They do not test Focacal's core constraints: free-gap fitting, time windows, duration ranges, rank-based contention.
- No small-model results were found for planning benchmarks (NATURAL PLAN, TCP, TravelPlanner).
- Date and time arithmetic is the typical weak spot for small models. Mitigation: move arithmetic into tools (gaps, durations, window checks) so the model chooses among precomputed options, or fine-tune.
- The existing roadmap notes the current agent sometimes places oversized blocks, so even a 14B model has shown this failure.
- Several non-commercial licenses (xLAM, Hammer) rule those out for an open source release shared with coworkers.
- One page the research agent visited contained claims that could not be verified (for example model names that did not check out). Those were not relied on.
- Assistant's caution (unchanged from the decision record): 2B is at the low end for reliable multi-step tool use; 7B to 14B is more realistic. Untested.

## 6. Proposed way to decide (at planner time)

1. Build a benchmark: sample days and weeks as data (meetings, working hours, Routine / Focus / Transition rules), with the validator as scorer (valid plan, oversized blocks, window violations, warnings emitted correctly, retries needed).
2. Run candidates through it via an OpenAI-compatible endpoint: Qwen3 4B, Qwen3 8B, Gemma 4 E4B, Granite 4.0 Micro, Phi-4-mini, with Qwen3 14B (or the current `qwen2.5:14b`) as the reference.
3. Record pass rate, retries, latency and memory on the owner's laptop. Model choice stays a config value (`agent.model`).
4. If no small model clears the bar, consider generating training data with the validator and LoRA-tuning a small base (PRIMETIME-style). Separate project.

## 7. Open items

- Verify Ollama availability and tool-calling support for Phi-4-mini and Granite 4.0 tags.
- Verify the xLAM and Hammer scores and licenses if they are ever reconsidered (likely moot because of the license).
- Search more for scheduling-specific tuned models; the negative result came from limited searching.
