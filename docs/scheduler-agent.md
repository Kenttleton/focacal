# Scheduler Agent Configuration

Focacal's scheduling agent uses the **OpenAI chat completions format** (`POST /v1/chat/completions`), which is supported by virtually every major AI provider. Switching providers is a three-line config change — no code changes required.

## How it works

On each replan cycle the agent:

1. Calls `focacal_get_free_gaps` to find available slots across the next 7 days
2. Calls `focacal_write_block` to place focus, lunch, and break blocks
3. Skips any slot covered by a real calendar event or a locked block
4. Calls `focacal_trigger_emitters` when done

All tool calls go to the local MCP server (`:5234`), regardless of which AI provider is running the agent loop.

## Configuration

All settings live under the `agent` key in `config.yaml`:

```yaml
agent:
  base_url: http://localhost:11434/v1   # OpenAI-compatible endpoint
  model: qwen2.5:14b                    # Model name as the provider expects it
  api_key: ""                           # Leave empty for local/unauthenticated endpoints
  timeout_secs: 120
  max_tokens: 4096
```

---

## Provider reference

### Ollama (default — local, free, private)

```yaml
agent:
  base_url: http://localhost:11434/v1
  model: qwen2.5:14b      # or qwen2.5:7b for lower RAM; llama3.1:8b works but is less consistent
  api_key: ""
```

Pull the model first: `ollama pull qwen2.5:14b`

**Recommended models for scheduling:**

| Model | RAM | Tool-use quality |
| ------- | ----- | ----------------- |
| `qwen2.5:14b` | ~10 GB | Best for local |
| `qwen2.5:7b` | ~5 GB | Good, slightly less reliable on multi-step chains |
| `llama3.1:8b` | ~5 GB | Works, occasional missed tool calls |

---

### OpenAI

```yaml
agent:
  base_url: https://api.openai.com/v1
  model: gpt-4o
  api_key: sk-...
```

**Recommended models:**

| Model | Notes |
| ------- | ------- |
| `gpt-4o` | Best tool-use reliability |
| `gpt-4o-mini` | Faster, cheaper, reliable for scheduling |
| `o3-mini` | Strong reasoning, slightly slower |

---

### Anthropic (via OpenAI-compatible endpoint)

Anthropic exposes an OpenAI-compatible API at `https://api.anthropic.com/v1`.

```yaml
agent:
  base_url: https://api.anthropic.com/v1
  model: claude-sonnet-4-6
  api_key: sk-ant-...
```

**Recommended models:**

| Model | Notes |
| ------- | ------- |
| `claude-sonnet-4-6` | Best balance of speed and quality for scheduling |
| `claude-opus-4-6` | Highest quality, slower and more expensive |
| `claude-haiku-4-5` | Fastest, cheapest; reliable for simple schedules |

---

### Azure OpenAI

```yaml
agent:
  base_url: https://YOUR-RESOURCE.openai.azure.com/openai/deployments/YOUR-DEPLOYMENT
  model: gpt-4o          # Must match your deployment's model
  api_key: YOUR-AZURE-KEY
```

For Azure, the `model` field must match the deployment name exactly as configured in Azure AI Studio.

---

### GitHub Models

```yaml
agent:
  base_url: https://models.inference.ai.azure.com
  model: gpt-4o
  api_key: YOUR-GITHUB-TOKEN   # Personal access token with models:read scope
```

GitHub Models gives free access to a curated set of models. Check [github.com/marketplace/models](https://github.com/marketplace/models) for the current list.

---

### Groq (fast inference)

```yaml
agent:
  base_url: https://api.groq.com/openai/v1
  model: llama-3.3-70b-versatile
  api_key: gsk_...
```

Groq is significantly faster than hosted providers for local-model-class models. Good option if you want sub-second replan cycles without running Ollama locally.

---

### LM Studio / Jan / any local OpenAI-compatible server

```yaml
agent:
  base_url: http://localhost:1234/v1   # LM Studio default; adjust port for other tools
  model: your-loaded-model-name
  api_key: ""
```

---

## Choosing a model

The scheduler agent runs a multi-step tool-calling loop. Not all models handle this equally well. Key requirements:

1. **Reliable tool/function calling** — the model must consistently call tools rather than generating text responses
2. **Multi-turn coherence** — the model must track which blocks it has already placed across 10–20 tool calls
3. **Instruction following** — the model must respect constraints like locked blocks, lunch priority, and mode-specific rules (pomodoro, fixed)

Models that work well in practice:

- **`qwen2.5:14b`** (Ollama) — best local option
- **`gpt-4o`** / **`gpt-4o-mini`** (OpenAI) — highly reliable
- **`claude-sonnet-4-6`** (Anthropic) — excellent at multi-step planning
- **`llama-3.3-70b-versatile`** (Groq) — fast with good tool use

Models to avoid:

- Very small models (<7B parameters) — often fail to use tools correctly
- Older models without explicit tool-calling support

## Claude Desktop for interactive tweaks

For one-off changes ("move my 3 PM focus block to 4 PM", "add a 90-minute deep work block tomorrow morning"), use Claude Desktop with focacal's MCP server rather than triggering a full replan. See [claude-desktop.md](claude-desktop.md) for setup. The same MCP tools are available in both paths.

## Environment variable override

`api_key` can be provided via environment variable to avoid storing credentials in `config.yaml`:

```bash
export FOCACAL_AGENT_API_KEY=sk-...
```

Viper maps `FOCACAL_AGENT_API_KEY` → `agent.api_key` automatically when the app starts.
