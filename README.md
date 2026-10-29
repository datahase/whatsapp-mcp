<p align="center">
  <img src="assets/brand/whatsapp-mcp-icon.png" alt="WhatsApp MCP logo" width="128" height="128">
</p>

# WhatsApp MCP

Connect your personal WhatsApp accounts to an AI assistant. Read and search
chats, send messages and files, and optionally transcribe voice notes, translate
text, or analyze images.

The server runs in one Docker container. Manage accounts in the browser and
use WhatsApp tools through your MCP client. Each account has its own saved
session and data. AI features are optional and off by default.

> Unofficial software, with no Meta affiliation. WhatsApp may change its
> protocol or restrict accounts using unofficial clients. Use your own accounts.

## Quick start

You need **Docker with Compose**, **curl**, and WhatsApp on your phone.
Run the commands below on the computer that will host the Docker container.
Linux and macOS are supported; Windows requires WSL and Docker Desktop and has
not been tested. No Go or Node.js installation is needed for the server.

### 1. Install and start

Run from any folder on the Docker host:

```sh
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/main/scripts/install-cli.sh | sudo sh -s -- /usr/local --setup
```

The installer adds the CLI, then opens a setup wizard as your user. Choose how
you want to connect:

| Choice | Access | Suitable clients |
| --- | --- | --- |
| Local | This computer only | Local MCP clients |
| Private Tailscale | HTTPS within your tailnet | Codex CLI/Desktop, Claude Code, local bridges |
| Public Funnel | Public HTTPS with OAuth | ChatGPT and Claude remote connectors, plus local clients |
| Existing HTTPS proxy | Your own HTTPS origin, with OAuth | Clients that can reach that origin |

For Tailscale modes, install Tailscale on the Docker host and sign in first.
Private access also requires Tailscale on your client device. Funnel requires
permission in your tailnet policy; complete any Tailscale approval prompts.
Docker and Compose must already be installed, running, and accessible to your
user (`docker ps` must work). The installer does not install Docker.

Setup downloads the published image and Compose configuration, generates
credentials, waits for a healthy container, and prints an admin URL and a
one-time sign-in code. No source checkout or local image build is required.
The container runs in the background and restarts with Docker.

### 2. Choose settings, if needed

The wizard works with the piped install command. For unattended installation,
append setup options, for example:

```sh
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/main/scripts/install-cli.sh | sudo sh -s -- /usr/local --setup --mode funnel --yes
```

If the CLI is already installed:

```sh
whatsapp-mcp setup --mode local --yes
# Or choose private HTTPS:
whatsapp-mcp setup --mode tailscale --yes
# Or use a proxy you already operate:
whatsapp-mcp setup --mode https --url https://whatsapp.example.com --yes
```

Choose one mode on first setup. Use `--port 8766` for another host port,
`--https-port 8443` if Tailscale already uses port 443, or `--dir /path/to/settings`
for a different configuration directory. Import AI and other advanced settings
with `--env-file /path/to/private.env`. Keep secrets in that file, not command
arguments. See [all setup options](docs/OPERATIONS.md#one-command-setup).

Settings default to `$HOME/.local/share/whatsapp-mcp` (or
`$XDG_DATA_HOME/whatsapp-mcp`). Keep its private `.env` file and the persistent
Docker data volume. Existing deployments are discovered and reused. Rerunning
`whatsapp-mcp setup --yes` keeps their credentials and network configuration.
The CLI reports the configuration directory it uses.

To install only the CLI, omit `--setup` from the curl command. For installation
without `sudo`, see [CLI installation options](docs/OPERATIONS.md#management-from-any-folder).

### 3. Sign in

Open the admin URL printed by setup and enter its one-time code. It works once
and expires after 10 minutes. Generate another code at any time:

```sh
whatsapp-mcp login
```

For local mode, the admin URL is
**[http://127.0.0.1:8765/admin](http://127.0.0.1:8765/admin)**.
Tailscale and HTTPS modes print your configured HTTPS address. Run the CLI on
the Docker host; open the URL in the browser on the device that has access.
The admin password in `.env` also works.

### 4. Link your WhatsApp account

1. Click **Add account**, then **Show QR code**.
2. On your phone, open WhatsApp and go to **Settings → Linked devices**.
   On Android, use the three-dot menu to find **Linked devices**.
3. Tap **Link a device** and scan the QR code.
4. Wait for the account to show **Connected**.

You can also choose **Link with phone number instead**. To add another
account, repeat these steps with the other phone. Linked accounts stay saved
across container restarts. New links advertise the device name **WhatsApp MCP**.
An older linked-device entry may retain its previous name until you relink it.

### 5. Connect your AI assistant

Open **Connect client** in the admin sidebar. It shows your MCP endpoint and
the setup instructions for your server's authentication mode.

| Client | Setup guide |
| --- | --- |
| Codex CLI | [OAuth sign-in](docs/OAUTH.md#connect-codex), or use the static-token configuration shown in **Connect client** |
| Codex Desktop | [Desktop MCP setup and OAuth](docs/CODEX_DESKTOP.md) |
| Claude Code | [HTTP MCP setup and OAuth](docs/CLAUDE_CODE.md) |
| Claude Desktop | [Public connector or private local bridge](docs/CLAUDE_DESKTOP.md) |
| ChatGPT custom MCP plugin | [Public HTTPS and OAuth registration](docs/OAUTH.md#chatgpt-and-claude-remote-connectors) |

A private Tailscale Serve address is reachable by devices in your tailnet.
Cloud-backed remote connectors need a public route, such as Tailscale Funnel,
or a provider-supported private tunnel. OAuth authorizes access; it does not
create that network route. See [OAuth setup](docs/OAUTH.md) before exposing
an endpoint publicly.

Try a read-only request first:

> List my WhatsApp accounts, then show recent chats for the account I choose.

The admin page manages accounts and client access. Read and send messages
through your AI assistant. A send tool sends immediately, so specify the
account, recipient, and message you intend to send.

## Docker Hub images

Pull the published multi-platform image:

```sh
docker pull datahase/whatsapp-mcp:latest
```

For a fixed release, or to pull from GHCR instead:

```sh
docker pull datahase/whatsapp-mcp:0.6.3
docker pull ghcr.io/datahase/whatsapp-mcp:0.6.3
```

Both registries publish `0.6.3`, `0.6`, and `latest`, with `linux/amd64` and
`linux/arm64` variants. Docker selects the variant for your host. To pin the
image or use GHCR, set one of these in `.env`:

```sh
WHATSAPP_MCP_IMAGE=datahase/whatsapp-mcp:0.6.3
# Or: WHATSAPP_MCP_IMAGE=ghcr.io/datahase/whatsapp-mcp:0.6.3
```

Pulling an image downloads it. `whatsapp-mcp setup` provides configuration and persistent storage.
To deploy a specific image from any folder:

```sh
whatsapp-mcp setup --image datahase/whatsapp-mcp:0.6.3 --yes
```

To build from source instead, clone this repository, run
`sh scripts/init-env.sh`, then `docker compose up -d --build`. Source development
instructions are in [CONTRIBUTING.md](CONTRIBUTING.md).

## Remote access with Tailscale

Tailscale is optional. Install it on the **Docker host**, outside the container.
For private access, also install it on your laptop and connect both devices to
the same tailnet.

| Access | Use it for | Who can reach it? |
| --- | --- | --- |
| Tailscale Serve | Admin page, local Codex/Claude Code, or a Claude Desktop bridge | Permitted devices in your tailnet |
| Tailscale Funnel | ChatGPT and Claude cloud-backed remote connectors | Public internet, with app login and OAuth still required |

The setup wizard configures Tailscale for you on a fresh deployment:

```sh
whatsapp-mcp setup --mode tailscale --yes
# For public remote connectors, choose this instead:
whatsapp-mcp setup --mode funnel --yes
```

Both modes set the HTTPS origin and OAuth-only API access. Setup leaves an
existing Tailscale route alone; use another `--https-port` if necessary. Public
Funnel is enabled only after the application is healthy. Keep the default
`BIND_IP=127.0.0.1`; Tailscale forwards to Docker locally.

Funnel uses your `ts.net` hostname, so a custom domain is unnecessary. It makes
the forwarded service public, including the admin sign-in page. Admin login
and OAuth remain required. The cloud provider does not need to join your tailnet.
For an existing deployment, keep its network settings or follow the
[manual network configuration guide](docs/OAUTH.md#server-setup).

Check access with `tailscale serve status` or `tailscale funnel status`. Public
DNS can take time to appear; verify the endpoint from outside your tailnet if
OAuth discovery fails. See the [full remote guide](docs/REMOTE_ACCESS.md),
[Tailscale Serve documentation](https://tailscale.com/docs/features/tailscale-serve),
and [Funnel documentation](https://tailscale.com/docs/features/tailscale-funnel).

## Everyday commands

Run these on the Docker host from any folder:

| Command | What it does |
| --- | --- |
| `whatsapp-mcp setup` | Deploy or reapply the server configuration |
| `whatsapp-mcp login` | Generate an admin sign-in code |
| `whatsapp-mcp status` | Show container state and health |
| `whatsapp-mcp logs` | Show the last 50 log lines |
| `whatsapp-mcp logs --follow` | Stream logs |
| `whatsapp-mcp restart` | Restart the server |
| `whatsapp-mcp stop` | Stop the server and keep saved data |
| `whatsapp-mcp start` | Start the existing container again |
| `whatsapp-mcp help` | Show available commands |

If multiple instances exist, select one with
`whatsapp-mcp --container NAME login`. The CLI never guesses which one to use.

## Chat with your WhatsApp history

The optional [history feature](docs/HISTORY.md) uses local EmbeddingGemma 2
embeddings and per-account SQLite indexes. After linking an account, choose all
available chats or selected conversations to monitor. These choices control
indexing only; existing WhatsApp MCP tools keep their current access.

Search past discussions, inspect exact message/transcript sources, and use
retrieval-only tools with your MCP client's own model. Server-generated answers
require a separate external-provider opt-in. An all-actions review walks cached
history chronologically rather than treating top search results as complete.
No search automatically transcribes audio or sends messages.

Pull the optional runtime with `docker pull datahase/whatsapp-mcp:0.6.3-history`
(or `ghcr.io/datahase/whatsapp-mcp:0.6.3-history`). It needs substantially more memory
than the default Go container. See [setup, privacy and verification](docs/HISTORY.md).

## Optional AI features

Messaging works without an AI key. To enable voice-note transcription,
translation, or image analysis, follow the [AI setup guide](docs/AI.md).
Gemini is the default provider. Vercel AI Gateway keys are also supported for
Gemini transcription; see [Gateway setup](docs/AI.md#vercel-ai-gateway-with-gemini).
Other built-in providers are listed in the AI guide.
Use paid provider access for personal media. Analysis runs only when requested.

## Updates and backups

Follow the [backup and update guide](docs/OPERATIONS.md) before upgrading.
From any folder, choose the new release image:

```sh
whatsapp-mcp setup --image datahase/whatsapp-mcp:0.6.3 --yes
```

Setup backs up the existing `.env`, validates configuration, and waits for
health. It attempts to restore the previous image and settings if startup
fails. Back up the data volume before upgrades; startup rollback does not
undo database migrations. Source-based installations use `git pull --ff-only`
and `docker compose up -d --build` instead.

Re-run the curl installation command to update the host CLI. Docker stores
account sessions, cached messages, and media in its persistent data volume.
`docker compose down -v` deletes that volume; do not use it to restart or update.

## Troubleshooting

| Problem | Next step |
| --- | --- |
| `whatsapp-mcp: command not found` | Run the CLI installer on the Docker host. The system installation uses `/usr/local/bin`. |
| Cannot access Docker | Start Docker and check that your user can run `docker ps`. |
| No WhatsApp MCP container found | Complete server startup, or select an existing container with `--container NAME`. |
| Sign-in or QR code expired | Generate a new sign-in code or choose **Show QR code** again. |
| Admin page will not open | Check `whatsapp-mcp status` and `whatsapp-mcp logs`. For another computer, check [remote access](docs/REMOTE_ACCESS.md). |
| OAuth discovery fails | Check public DNS, HTTPS, and provider-side reachability. See [OAuth troubleshooting](docs/OAUTH.md#chatgpt-and-claude-remote-connectors). |
| Transcription is unavailable | Check your provider key, model, and quota in [AI setup](docs/AI.md). Messaging remains available. |

## Documentation

- [Shared OAuth setup and Codex CLI sign-in](docs/OAUTH.md)
- [Claude Code OAuth sign-in](docs/CLAUDE_CODE.md)
- [Claude Desktop OAuth sign-in](docs/CLAUDE_DESKTOP.md)
- [Codex Desktop OAuth sign-in](docs/CODEX_DESKTOP.md)
- [Client configuration and media transfers](docs/CLIENTS.md)
- [Remote access](docs/REMOTE_ACCESS.md)
- [Backups, updates, and recovery](docs/OPERATIONS.md)
- [AI providers](docs/AI.md)
- [Supported platforms, MCP tools, and limitations](docs/REFERENCE.md)
- [Admin interface](docs/ADMIN_UI.md)
- [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)

## License

[GPL-3.0-or-later](LICENSE). Upstream MIT notices are retained in
[LICENSE-UPSTREAM-MIT](LICENSE-UPSTREAM-MIT) and [UPSTREAM.md](UPSTREAM.md).
Dependency notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
