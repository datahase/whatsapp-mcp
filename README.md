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

### 1. Install the CLI

Run from any folder:

```sh
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/main/scripts/install-cli.sh | sudo sh -s -- /usr/local
```

This installs `whatsapp-mcp`, the command for managing your Docker container.
It downloads the CLI without requiring a project checkout. Docker and the
server are set up in the next step.

Already have a running server? Skip to [Sign in](#3-sign-in).
For installation without `sudo`, see the [CLI installation options](docs/OPERATIONS.md#management-from-any-folder).

### 2. Start the server

Download the deployment files and start the published image:

```sh
mkdir -p whatsapp-mcp/scripts
cd whatsapp-mcp
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/v0.3.0/compose.yaml -o compose.yaml
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/v0.3.0/.env.example -o .env.example
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/v0.3.0/scripts/init-env.sh -o scripts/init-env.sh
sh scripts/init-env.sh
docker compose pull
docker compose up -d --no-build
```

These are configuration files and a setup helper; no source checkout or local
image build is required. The setup script creates a private `.env` file with
random credentials. Keep that file and the persistent Docker data volume.
See [Docker Hub images](#docker-hub-images) for registry and version choices.

Check startup:

```sh
whatsapp-mcp status
```

Wait for `healthy`. The container runs in the background and restarts with
Docker. You can close the terminal. Everyday CLI commands work from any folder
on this Docker host; initial setup and configuration changes use the deployment
folder containing `compose.yaml`.

### 3. Sign in

Generate a one-time code on the server:

```sh
whatsapp-mcp login
```

Open **[http://127.0.0.1:8765/admin](http://127.0.0.1:8765/admin)**, enter the
code, and click **Sign in**. The code works once and expires after 10 minutes.
Run the command again for a new code. The admin password in `.env` also works.

If Docker runs on another computer, use that server's address in your browser.
For Tailscale, follow [remote access below](#remote-access-with-tailscale).
Other network setups are covered in the [remote access guide](docs/REMOTE_ACCESS.md);
`127.0.0.1` refers to the computer where the browser is running.

### 4. Link your WhatsApp account

1. Click **Add account**, then **Show QR code**.
2. On your phone, open WhatsApp and go to **Settings → Linked devices**.
   On Android, use the three-dot menu to find **Linked devices**.
3. Tap **Link a device** and scan the QR code.
4. Wait for the account to show **Connected**.

You can also choose **Link with phone number instead**. To add another
account, repeat these steps with the other phone. Linked accounts stay saved
across container restarts.

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
docker pull datahase/whatsapp-mcp:0.3.0
docker pull ghcr.io/datahase/whatsapp-mcp:0.3.0
```

Both registries publish `0.3.0`, `0.3`, and `latest`, with `linux/amd64` and
`linux/arm64` variants. Docker selects the variant for your host. To pin the
image or use GHCR, set one of these in `.env`:

```sh
WHATSAPP_MCP_IMAGE=datahase/whatsapp-mcp:0.3.0
# Or: WHATSAPP_MCP_IMAGE=ghcr.io/datahase/whatsapp-mcp:0.3.0
```

Pulling an image downloads it. The [startup steps above](#2-start-the-server)
provide configuration and persistent storage. After changing the image setting,
apply it from the deployment folder:

```sh
docker compose pull
docker compose up -d --no-build
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

On a Linux Docker host, start private HTTPS access:

```sh
sudo tailscale serve --bg --https=443 http://127.0.0.1:8765
```

Copy the `https://…ts.net` URL printed by Tailscale. In the deployment folder,
edit `.env`, replacing the example below with that exact URL:

```sh
WHATSAPP_MCP_PUBLIC_URL=https://your-server.your-tailnet.ts.net
WHATSAPP_MCP_OAUTH_ONLY=true
```

Apply the settings from that folder:

```sh
docker compose up -d
```

Keep the default `BIND_IP=127.0.0.1`: Tailscale forwards to Docker locally.
If you changed the bind address or host port, use that address and port in
the Serve/Funnel target instead.
Open `/admin` on your HTTPS URL and use the same URL with `/mcp` in your client.
Run `whatsapp-mcp login` on the server to generate the admin sign-in code.

For **ChatGPT or Claude remote connectors**, enable public Funnel access after
configuring OAuth:

```sh
sudo tailscale funnel --bg --https=443 http://127.0.0.1:8765
```

Complete any Tailscale HTTPS/policy approval prompts. Funnel uses the same
`ts.net` hostname, so a custom domain is unnecessary. It makes the forwarded
service public, including the admin sign-in page. Admin login and OAuth remain
required. The cloud provider does not need to join your tailnet.

Check access with `tailscale serve status` or `tailscale funnel status`. Public
DNS can take time to appear; verify the endpoint from outside your tailnet if
OAuth discovery fails. See the [full remote guide](docs/REMOTE_ACCESS.md),
[Tailscale Serve documentation](https://tailscale.com/docs/features/tailscale-serve),
and [Funnel documentation](https://tailscale.com/docs/features/tailscale-funnel).

## Everyday commands

Run these on the Docker host from any folder:

| Command | What it does |
| --- | --- |
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

## Optional AI features

Messaging works without an AI key. To enable voice-note transcription,
translation, or image analysis, follow the [AI setup guide](docs/AI.md).
Gemini is the default provider; other built-in providers are listed there.
Use paid provider access for personal media. Analysis runs only when requested.

## Updates and backups

Follow the [backup and update guide](docs/OPERATIONS.md) before upgrading.
From your deployment folder, pull and start the configured image:

```sh
docker compose pull
docker compose up -d --no-build
```

If you pinned `WHATSAPP_MCP_IMAGE` to a version, change it to the new release
first. Source-based installations use `git pull --ff-only` and
`docker compose up -d --build` instead.

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
