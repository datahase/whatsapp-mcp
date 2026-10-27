# Connecting MCP clients

Endpoint: `http://<host>:8765/mcp`, Streamable HTTP in stateless mode. It
accepts both pre-2026 clients (`initialize` first) and the 2026-07-28 revision,
and never issues an `Mcp-Session-Id`. Every request needs:

```
Authorization: Bearer <contents of secrets/mcp_token>
```

Keep the token in an environment variable or your client's secret store, not
in a file you commit. `<host>` is `127.0.0.1` on the same machine, or the
address you set as `BIND_IP` and listed in `allowed_hosts`.

Verification status: the server is tested with raw JSON-RPC over HTTP (unit,
end-to-end and container suites). The client snippets below follow each
client's documented HTTP configuration but have **not** been verified
end to end yet.

## Claude Code

```sh
export WHATSAPP_MCP_TOKEN=$(cat secrets/mcp_token)
claude mcp add --transport http whatsapp http://127.0.0.1:8765/mcp \
  --header "Authorization: Bearer $WHATSAPP_MCP_TOKEN"
```

## Codex CLI (`~/.codex/config.toml`)

```toml
[mcp_servers.whatsapp]
url = "http://127.0.0.1:8765/mcp"
bearer_token_env_var = "WHATSAPP_MCP_TOKEN"
```

## Generic JSON configuration (clients that support HTTP servers)

```json
{
  "mcpServers": {
    "whatsapp": {
      "type": "http",
      "url": "http://127.0.0.1:8765/mcp",
      "headers": { "Authorization": "Bearer ${WHATSAPP_MCP_TOKEN}" }
    }
  }
}
```

Hosted chat products that connect to MCP servers from their own cloud
(for example web connectors) need a publicly reachable HTTPS URL and often
OAuth. This project does not provide that; do not expose it publicly to make
one work.

## Upload and download from the client machine

```sh
# Upload: returns {"media": {"media_id": "med_…", "mime_type": …, "sha256": …}}
curl -H "Authorization: Bearer $WHATSAPP_MCP_TOKEN" \
     -F file=@voice.m4a http://<host>:8765/api/accounts/<account_id>/media

# Download a media_id returned by get_media
curl -H "Authorization: Bearer $WHATSAPP_MCP_TOKEN" -OJ \
     http://<host>:8765/api/accounts/<account_id>/media/<media_id>
```

Uploads up to 32 MiB are streamed to disk and verified by content, not by
extension. Staged uploads expire after 24 hours.

## A typical agent flow

1. `list_accounts`, then pick the `account_id`.
2. `search_contacts` with a name, then confirm the exact `jid` with the user if
   there is more than one candidate.
3. `send_message` with a fresh `idempotency_key` (a UUID). If the call times
   out, call it again with the **same** key, or use `get_send_status`.
