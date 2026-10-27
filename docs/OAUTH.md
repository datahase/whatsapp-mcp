# Connect with OAuth

OAuth lets a client sign in through your browser. You approve the connection
once; the client obtains and refreshes its own credentials. There is no service
access token to copy into the client.

The owner still signs in to the admin page with a one-time code or admin
password. WhatsApp phones remain linked through QR/phone pairing.

## Server setup

Use a private HTTPS address reachable from your client, through a TLS reverse
proxy or Tailscale Serve. Set these in the server's `.env`:

```sh
WHATSAPP_MCP_PUBLIC_URL=https://whatsapp.example.com
WHATSAPP_MCP_OAUTH_ONLY=true
```

Replace the example with your own HTTPS origin, without `/mcp` or another path.
Restart with `docker compose up -d`. The public host is automatically allowed.
Your proxy must preserve the Host and set `X-Forwarded-Proto: https`. Restrict
access to the backend listener to your private network/proxy.

`OAUTH_ONLY=true` disables the static token for MCP, media uploads/downloads
and the status API. Health checks and browser admin sessions continue to work.
If `PUBLIC_URL` is empty, the existing static-token mode remains available.
For local development only, an HTTP loopback origin is accepted.

Tailscale Serve can provide HTTPS within your tailnet without exposing the
service publicly. Both the browser and Codex computer must be able to reach
that private address. Other deployments can use their own TLS proxy; Tailscale
is optional.

## Connect Codex

1. In the admin page, choose **Connect client** and copy the endpoint.
2. Run these commands on the computer where Codex runs:

   ```sh
   codex mcp add whatsapp --url https://whatsapp.example.com/mcp
   codex mcp login whatsapp
   ```

3. Follow the browser sign-in and click **Approve access**. Check that you
   started this login and that the callback belongs to your client.
4. Ask Codex to list your WhatsApp accounts.

Use your server's address in the command. Do not set a bearer-token environment
variable for this connection. If you already have a static-token entry named
`whatsapp`, replace its configuration with the OAuth endpoint before signing in.

The approval covers reading, sending and requested analysis for all current
and future linked accounts. Sending still occurs only through explicit client
tool calls. Approval does not send a message or trigger media analysis itself.

## ChatGPT and Claude remote connectors

These clients connect from their providers' cloud servers. A private Tailscale
Serve address works for a local Codex client but is not reachable through their
**Server URL** or remote connector settings. Use a publicly reachable HTTPS
endpoint protected by OAuth, or a supported private connector route.

For a public endpoint:

1. Add `https://your-host/mcp` in the client's custom MCP settings.
2. Choose **OAuth** and **Register automatically** (dynamic client registration,
   DCR). This server does not support the published-identity/CIMD option.
3. Use `whatsapp` as the scope if the client asks. Leave client secret blank.
4. Sign in to the owner admin page and approve the connection.
5. Verify a read-only account listing before sending a message.

A **Could not discover OAuth settings** error can mean the provider cannot
reach the endpoint. Check public DNS, TLS, the MCP 401 challenge and both
well-known metadata routes from outside your private network. A browser on
your own Tailscale-connected laptop does not verify cloud reachability.

Tailscale Funnel can make an existing Serve endpoint publicly reachable while
retaining its hostname and TLS certificate. Enable it only when the owner has
chosen public access. OAuth remains required for MCP and data APIs; the admin
sign-in page is also reachable and still requires its separate owner login.
To return an endpoint to private-only access, disable Funnel and restore Serve.

### Claude Desktop over a private network

Regular Claude Desktop can instead launch an OAuth-capable local stdio bridge,
which reaches the HTTP server through your laptop's network. For example,
[`mcp-remote`](https://github.com/punkpeye/mcp-remote) supports this connection:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "npx",
      "args": ["-y", "mcp-remote@0.15.2", "https://your-host/mcp",
               "--transport", "http-only", "--resource", "https://your-host/mcp"]
    }
  }
}
```

Use your real endpoint in both places and an absolute executable path if the
app cannot find Node/npx. Merge this entry with existing desktop configuration,
then fully restart regular Claude Desktop. This is separate from Claude Work,
Cowork and Claude's cloud-backed remote connectors.

## Remove a client's access

Open **Connect client** and click **Revoke** beside the connection. Existing
credentials stop working immediately; the client must complete a new login.
This does not disconnect a WhatsApp phone or delete chats.

OAuth approval lasts at most 30 days. Access tokens last 15 minutes; refresh
tokens rotate when used. Reusing an old refresh token revokes that token family.
Server-side credentials are stored as hashes in `/data/service/oauth.db`.
Approved connections survive a service restart; pending browser approvals do
not. Include the complete data volume in private backups.

## Compatibility and implementation

- Authorization code with S256 PKCE; no implicit/password/client-credentials grants.
- MCP protected-resource and authorization-server discovery metadata.
- Dynamic registration of public clients with exact redirect URI matching.
- HTTPS callbacks or HTTP loopback callbacks; no wildcard redirects or fragments.
- Resource/audience, client, scope and expiration validation.
- Single-use consent requests and authorization codes; session/CSRF protected approval.
- Revocation, rotating refresh tokens and replay detection.
- Client ID Metadata Documents (CIMD), external identity providers and per-account
  authorization scopes are not implemented.

Codex was verified with the installed CLI/app-server: browser approval with an
automatic local callback, followed by a real read-only `list_accounts` call over
private HTTPS. A synthetic local check also forced access expiry and verified
Codex refresh. A local `mcp-remote` OAuth bridge was also verified with account listing, and
regular Claude Desktop loaded the bridge and requested its tools. ChatGPT and
Claude cloud-backed remote connectors still need their own tool-call acceptance
checks. OAuth alone does not make a private server reachable by cloud clients.

[Back to setup](../README.md)
