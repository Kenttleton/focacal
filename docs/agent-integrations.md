# Agent Integrations — OpenAPI

Focacal exposes an OpenAPI 3.1 spec at `GET /openapi.json` (REST server, port 5233). Any agent platform that reads OpenAPI can use it to call focacal's REST API as a set of tools.

## ChatGPT Custom GPT (Actions)

1. Go to [https://chatgpt.com/gpts/editor](https://chatgpt.com/gpts/editor) and create a new GPT.
2. Under **Configure → Actions**, click **Create new action**.
3. In the schema field, paste the URL:

   ```text
   http://your-host:5233/openapi.json
   ```

   or click **Import from URL** and enter it directly.
4. ChatGPT will parse the spec and list the available operations. Enable the ones you want.
5. Set **Authentication** to **None** (or add an API key header if you've put focacal behind a reverse proxy with auth).

**Suggested system prompt addition:**

```text
You have access to focacal, an AI calendar scheduling tool. Use it to check the user's schedule, manage focus blocks, and trigger replanning when the user's calendar changes.
```

## GitHub Copilot (agent mode)

Copilot agent mode can call external APIs described by OpenAPI. In your workspace `.github/copilot-instructions.md`:

```markdown
## Focacal
Use the focacal API at http://localhost:5233 to manage focus scheduling.
Spec: http://localhost:5233/openapi.json
```

## LangChain / LlamaIndex

Both frameworks can auto-generate tools from an OpenAPI spec:

```python
# LangChain
from langchain_community.agent_toolkits.openapi import create_openapi_agent
from langchain_community.utilities import TextRequestsWrapper
import requests, yaml

spec = requests.get("http://localhost:5233/openapi.json").json()
agent = create_openapi_agent(spec, TextRequestsWrapper(), llm)
agent.run("What does my afternoon look like today?")
```

```python
# LlamaIndex
from llama_index.tools.openapi import OpenAPIToolSpec
from llama_index.agent import ReActAgent

spec = OpenAPIToolSpec(url="http://localhost:5233/openapi.json")
agent = ReActAgent.from_tools(spec.to_tool_list(), llm=llm)
agent.chat("Block 2 hours of focus time tomorrow morning")
```

## Semantic Kernel (Microsoft)

```csharp
var plugin = await kernel.ImportPluginFromOpenApiAsync(
    "focacal",
    new Uri("http://localhost:5233/openapi.json")
);
```

## Available operations

| Operation | Method | Path | Description |
|-----------|--------|------|-------------|
| `getToday` | GET | `/today` | Today's full agenda |
| `getNow` | GET | `/now` | Current or next event |
| `listEvents` | GET | `/events` | Real calendar events for a range |
| `listBlocks` | GET | `/blocks` | AI-planned blocks for a range |
| `createBlock` | POST | `/blocks` | Create a locked focus block |
| `updateBlock` | PATCH | `/blocks/{id}` | Extend, rename, or lock a block |
| `deleteBlock` | DELETE | `/blocks/{id}` | Remove a block |
| `triggerReplan` | POST | `/replan` | Ask the AI to regenerate the schedule |
| `getConfig` | GET | `/config` | Read scheduling preferences |
| `updateConfig` | PATCH | `/config` | Update working hours / focus style |
| `getHealth` | GET | `/health` | Service status and sync state |
| `getCalendarFeed` | GET | `/calendar.ics` | Subscribe via webcal |

## Remote access and security

The REST server has no built-in authentication. For any agent platform calling focacal over the internet:

- Run focacal behind a reverse proxy (nginx, Caddy) with HTTPS and basic auth or a bearer token header.
- Update the `servers[0].url` in the OpenAPI spec to match your public URL, or provide the agent with the correct base URL directly.
- Restrict the `/openapi.json` and API endpoints to known IPs if your threat model requires it.
