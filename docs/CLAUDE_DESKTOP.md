# OAuth sign-in for Claude Desktop

Use this guide for regular Claude Desktop. Choose the connection path that
matches your network. Claude Code has [separate instructions](CLAUDE_CODE.md).

## Before you start

Link a phone and enable [HTTPS and OAuth](OAUTH.md#server-setup) on WhatsApp MCP.
Copy your endpoint from **Connect client**. Replace every
`https://whatsapp.example.com/mcp` below with that endpoint.

The owner admin login and the client's OAuth approval are separate. Generate
an owner sign-in code with `whatsapp-mcp login` on the Docker host when needed.
Approval grants the client access to all current and future linked accounts.

## Option A: Public remote connector

Use this path with a public HTTPS endpoint, such as [Tailscale Funnel](../README.md#remote-access-with-tailscale).
Claude contacts remote connectors from Anthropic's cloud, including when you
use Claude Desktop. A private Serve address cannot be reached through this path.
See [Anthropic's network requirements](https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp).

1. Open Claude's **Customize > Connectors** and add a custom connector.
2. Name it **WhatsApp MCP** and enter your endpoint, including `/mcp`.
3. Choose OAuth/sign-in authentication and **Register automatically** for the
   OAuth client. This server supports DCR, not Claude's published-identity option.
4. If asked for scopes, enter `whatsapp`. Leave client ID/secret unset for
   automatic registration.
5. Connect, sign in to the WhatsApp MCP admin page in the browser, and approve.
6. Enable the connector for the conversation and run the read-only check below.

Your workspace may require an owner to add connectors. UI wording and available
controls depend on the account and app version. The
[official connector guide](https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp)
covers account-specific steps.

## Option B: Local bridge for private Tailscale access

This path runs a small bridge on your computer. It reaches the server through
your local network, so private Tailscale Serve is sufficient. Install Tailscale
on both devices and connect them to your tailnet. Node.js and `npx` are needed
on the **Claude Desktop computer** for this bridge; the Docker server needs neither.

Open regular Claude Desktop's developer configuration. Merge this entry into
its `mcpServers` object, preserving any existing servers and other settings:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote@0.15.2",
        "https://whatsapp.example.com/mcp",
        "--transport",
        "http-only",
        "--resource",
        "https://whatsapp.example.com/mcp"
      ]
    }
  }
}
```

On macOS, the file is normally
`~/Library/Application Support/Claude/claude_desktop_config.json`. On Windows,
it is normally `%APPDATA%\Claude\claude_desktop_config.json`. This project's
Windows bridge setup has not been tested.

Replace the URL in **both** places. Fully quit and reopen regular Claude
Desktop. The bridge opens browser sign-in when authorization is needed. Approve
access and return to the app. If `npx` is not found, use its absolute executable
path and ensure Node is available to the desktop process.

The bridge uses [`mcp-remote`](https://github.com/punkpeye/mcp-remote) and stores
its OAuth credentials locally. Keep its credential directory private. This
configuration belongs to regular Claude Desktop; Claude Work/Cowork and remote
connectors use their own configuration paths.

## Verify the connection

Start a conversation with the WhatsApp tools enabled and ask:

> Use the WhatsApp MCP list_accounts tool to list my connected accounts. Do not send anything.

Confirm the account is returned before testing sends or media. Browser approval
alone is insufficient.

The local bridge was verified with OAuth and a real `list_accounts` call.
Regular Claude Desktop also loaded it and completed tool discovery. The public
remote-connector path still needs its own completed OAuth/tool-call acceptance.

## Revoke or troubleshoot

Open **Connect client > Revoke** on the WhatsApp MCP admin page to stop a
connection immediately. To remove a local bridge, remove only its `whatsapp`
configuration entry and restart Claude Desktop. Uninstalling alone does not
revoke credentials or disconnect the WhatsApp phone.

| Symptom | Check |
| --- | --- |
| Remote discovery fails | Use Funnel/public HTTPS; check public DNS from outside the tailnet. |
| Published identity is rejected | Choose automatic registration/DCR. |
| Local bridge fails to start | Check Node/npx, the absolute command path, and valid JSON. |
| Private endpoint is unreachable | Connect the Claude computer to Tailscale and check tailnet permissions. |
| Callback fails | Authorize in a browser on the computer running the local bridge. |
| Login succeeds but tools are absent | Enable the connector or restart the local app, then verify a real account-list call. |

[Back to README](../README.md) · [Shared OAuth settings](OAUTH.md)
