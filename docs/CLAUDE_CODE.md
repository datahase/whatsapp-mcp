# OAuth sign-in for Claude Code

Use this guide for Claude Code in your terminal. For the regular desktop app,
see [Claude Desktop](CLAUDE_DESKTOP.md).

## Before you start

Start WhatsApp MCP, link a phone, and enable [HTTPS and OAuth](OAUTH.md#server-setup).
Copy the endpoint from **Connect client** in the admin page. Examples below use
`https://whatsapp.example.com/mcp`; replace it with your endpoint.

Run client commands on the computer where you use Claude Code. Run
`whatsapp-mcp login` on the Docker host when you need an admin sign-in code.
These are separate logins.

A local Claude Code process can reach private Tailscale Serve when its computer
is in your tailnet. A cloud-hosted Claude session has different network access.
See [Tailscale setup](../README.md#remote-access-with-tailscale).

## Add the server and sign in

```sh
claude mcp add --scope user --transport http whatsapp https://whatsapp.example.com/mcp
claude mcp login whatsapp
```

The `user` scope makes the entry available across your local projects. Follow
the browser to your WhatsApp MCP admin page, sign in, review the permissions,
and choose **Approve access**. Return to Claude Code when the callback finishes.
No static bearer token or manually registered client secret is needed.

Alternatively, start `claude`, enter `/mcp`, select `whatsapp`, and use its
sign-in action. These commands and the interactive sign-in are documented in
the [official Claude Code MCP guide](https://code.claude.com/docs/en/mcp#authenticate-with-remote-mcp-servers).

## Verify the connection

In Claude Code, use `/mcp` to check the server and ask:

> Use the WhatsApp MCP list_accounts tool to list my connected accounts. Do not send anything.

A configured entry or completed browser callback does not prove tool access.
Confirm that the tool returns your account. Sending, media, and analysis require
separate checks with a recipient/file you choose.

## Reconnect or remove access

For a fresh sign-in:

```sh
claude mcp logout whatsapp
claude mcp login whatsapp
```

To remove the configuration:

```sh
claude mcp remove --scope user whatsapp
```

For immediate server-side revocation, open **Connect client** in WhatsApp MCP
and choose **Revoke** beside that connection. Removing a client configuration
alone does not revoke its issued credentials. The phone remains linked.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Server name already exists | Inspect `claude mcp get whatsapp`. If replacing an old entry, remove it from its original scope before adding this one. |
| Old token prevents OAuth | Remove the old Authorization header from the entry, then sign in again. |
| Endpoint cannot be reached | Check HTTPS and access from the Claude Code computer. Private Serve requires Tailscale on that computer. |
| Browser callback fails | Run the login on the same computer as your browser. For remote/headless use, consult the official guide's callback instructions. |
| Approval expired or access revoked | Start `claude mcp login whatsapp` again. |
| `mcp login` is unavailable | Use `/mcp` in an interactive session, or update Claude Code. |

Command syntax was checked against the installed Claude Code CLI. A complete
Claude Code OAuth login and tool call has not yet been verified for this project.

[Back to README](../README.md) · [Shared OAuth settings](OAUTH.md)
