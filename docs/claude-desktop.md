# Claude Desktop — MCP Integration

Focacal's MCP server exposes all seven planning tools directly to Claude. Once configured, you can ask Claude things like:

- "What's my schedule today?"
- "Move my afternoon focus block 30 minutes earlier"
- "Add a 90-minute deep work block tomorrow at 9 AM"
- "Trigger a replan — I just accepted two new meetings"

## Setup

1. Make sure focacal is running (`focacal start` or via Docker). The MCP server listens on port **5234** by default.

2. Open Claude Desktop and go to **Settings → Developer → Edit Config**.

3. Add focacal to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "focacal": {
      "url": "http://localhost:5234/mcp"
    }
  }
}
```

4. Restart Claude Desktop. You should see the focacal tools available in the tools panel.

## Available tools

| Tool | What it does |
| ------ | ------------- |
| `focacal_get_today` | Returns today's full agenda (events + blocks) |
| `focacal_get_free_gaps` | Returns unscheduled gaps in the next N days |
| `focacal_write_block` | Creates or updates a focus/lunch/break block |
| `focacal_delete_block` | Removes a block (locked blocks are refused) |
| `focacal_list_events` | Lists real calendar events for a date range |
| `focacal_get_config` | Returns current scheduling preferences |
| `focacal_update_config` | Updates working hours, lunch window, or focus style |

## Remote access

If Claude Desktop is not on the same machine as focacal, replace `localhost` with the server's IP or hostname and ensure port 5234 is reachable. The MCP server has no built-in authentication — put it behind a firewall or VPN if exposed beyond localhost.
