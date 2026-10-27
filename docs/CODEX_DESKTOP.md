# OAuth sign-in for Codex Desktop

Connect the local Codex desktop client to WhatsApp MCP through Streamable HTTP.
The official OpenAI documentation currently describes this integration under
the ChatGPT desktop app; names and settings placement can vary by app version.

## Before you start

Link a WhatsApp phone and enable [HTTPS and OAuth](OAUTH.md#server-setup).
Copy the endpoint from **Connect client** in the admin page. Replace
`https://whatsapp.example.com/mcp` below with your endpoint.

For local desktop execution, the computer must reach the server. Private
Tailscale Serve works when that computer is in your tailnet. Cloud-backed
plugin connections require public HTTPS, such as
[Tailscale Funnel](../README.md#remote-access-with-tailscale).

## Set up in the desktop app

1. Open **Settings > MCP servers** and add a server named `whatsapp`.
2. Select **Streamable HTTP** and enter the endpoint, including `/mcp`.
3. Save and restart the MCP connection/app as prompted.
4. Choose **Authenticate** for the server. In the browser, sign in to WhatsApp
   MCP, review access, and approve the connection.
5. Return to the app and check the server through `/mcp`.

This local MCP setup shares configuration with the Codex CLI for the same host.
See the [official OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp#connect-codex-to-an-mcp-server).

## Set up through the shared CLI configuration

If Codex CLI is installed on the same computer, you can configure it there:

```sh
codex mcp add whatsapp --url https://whatsapp.example.com/mcp
codex mcp login whatsapp
```

Approve in the browser, then restart/reload the desktop app's MCP connection.
Use the same Codex configuration home for both clients. A custom `CODEX_HOME`
or project-scoped configuration may point to a different server entry.

For manual configuration, merge this table into `~/.codex/config.toml`:

```toml
[mcp_servers.whatsapp]
url = "https://whatsapp.example.com/mcp"
scopes = ["whatsapp"]
```

Preserve other settings and avoid duplicate tables. Remove an old bearer-token
setting for this server before using OAuth. No client secret is required. This
server supports dynamic registration; if automatic registration fails, retry:

```sh
codex mcp login whatsapp --oauth-client-registration dcr
```

## Using the plugin/custom-server screen

If your app presents **Plugins > Add custom MCP server** with a **Server URL**
connection instead, use a public HTTPS endpoint. Choose **OAuth**,
**Register automatically (DCR)** in advanced settings, and scope `whatsapp`.
Follow [remote OAuth setup](OAUTH.md#chatgpt-and-claude-remote-connectors).
That connection does not make a private Serve address reachable by the cloud.

## Verify and revoke

Ask in the desktop app:

> Use the WhatsApp MCP list_accounts tool to list my connected accounts. Do not send anything.

A successful account-list result proves more than a saved configuration or
browser callback. Do not treat CLI success alone as desktop-app acceptance.

For local credential removal, use `codex mcp logout whatsapp`. For immediate
server-side removal, choose **Connect client > Revoke** in WhatsApp MCP.
Neither action unlinks the WhatsApp phone. To remove the shared configuration,
use `codex mcp remove whatsapp`.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| CLI works, desktop has no server | Check the host/configuration home, restart the connection, and inspect the desktop MCP list. |
| Browser callback fails | Run login and the browser on the same computer; keep Codex running until completion. |
| Discovery fails in the plugin screen | Check public HTTPS/DNS and use Funnel instead of private Serve. |
| OAuth is bypassed or rejected | Remove a previous bearer token/header and sign in again. |
| Wrong registration method | Select DCR; this server does not advertise CIMD. |
| Approval expired or access revoked | Start a new authentication request from the client. |

Codex CLI/app-server OAuth, automatic callback, real account listing, refresh,
and revocation were verified. The desktop GUI path still needs a separate
end-to-end account-list check. No desktop configuration is changed by this guide.

[Back to README](../README.md) · [Codex CLI OAuth guide](OAUTH.md#connect-codex)
