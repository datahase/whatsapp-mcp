# whatsapp-mcp

Connect your WhatsApp accounts to an AI assistant such as Claude Code or Codex.
Read and search chats, send messages and files, and optionally transcribe voice
notes, translate text or analyze images.

Runs in one Docker container. Add accounts in the browser; each account keeps
its own data. AI features are optional and off by default.

> Unofficial WhatsApp software, with no Meta affiliation. WhatsApp may change
> its protocol or restrict accounts using unofficial clients. Use your own
> accounts and send only messages you intend to send.

## Set up

You need Docker with the `docker compose` command, Git, and WhatsApp on your
phone. Use Terminal on macOS/Linux, or a WSL terminal connected to Docker
Desktop on Windows. Windows setup has not yet been tested.

Already running the server? Skip to [Sign in](#3-sign-in).
Running it on another computer? Follow [remote access](docs/REMOTE_ACCESS.md).

### 1. Download the project

```sh
git clone https://github.com/datahase/whatsapp-mcp.git
cd whatsapp-mcp
```

Run the initial setup commands from this folder. After setup, the management
command works from any folder on the Docker host.

### 2. Start the server

```sh
sh scripts/init-env.sh
docker compose up -d --build
sudo sh scripts/install-cli.sh /usr/local
```

The first command creates a private `.env` file with random credentials.
The second builds this checkout, including the admin UI, and starts it.
The last installs the small host management command.
The first build can take several minutes. No Node.js or Go installation is
needed on your computer.

Check that it started:

```sh
whatsapp-mcp status
```

Wait for `healthy`. Keep `.env` private; it contains your access credentials.

### 3. Sign in

Generate a one-time code:

```sh
whatsapp-mcp login
```

Open **[http://127.0.0.1:8765/admin](http://127.0.0.1:8765/admin)** in your
browser, enter the code and click **Sign in**.

Run this on the computer hosting Docker, from any folder. It finds the
container automatically; no project path or container name is needed.

The code works once and expires after 10 minutes. Generate another if needed.
The admin password from `.env` is also available as a fallback.

### 4. Link WhatsApp

1. In the browser, click **Add account**, then **Show QR code**.
2. On your phone, open WhatsApp and go to **Settings**, then **Linked devices**
   (on Android, open the three-dot menu, then **Linked devices**).
3. Tap **Link a device** and scan the QR code on your computer.
4. Wait for **Connected** in the browser.

Use **Link with phone number instead** if you cannot scan the QR code.
To link another WhatsApp account, repeat these steps with the other phone.
Accounts stay saved across restarts. There are no preset Personal or Secondary
profiles to configure.

### 5. Connect your AI assistant

Open **Connect client** in the admin sidebar. With OAuth enabled, add the
endpoint in your client, choose sign-in and approve access in the browser.
No access token needs to be copied. Follow the [OAuth setup guide](docs/OAUTH.md)
for HTTPS configuration and the exact Codex commands.

If your server uses static-token mode, follow the
[client setup guide](docs/CLIENTS.md) instead. The admin page manages connections;
read and send messages through your connected AI assistant.

Try a read-only request first:

> List my WhatsApp accounts, then show the recent chats for the account I choose.

Sending happens when your client calls a send tool. There is no extra
confirmation page in this app.

## Optional: transcription and image analysis

WhatsApp works without an AI provider key. To add transcription, translation
or image analysis, follow the [AI setup guide](docs/AI.md). It covers Gemini,
OpenAI and the other built-in providers, keys and usage limits.

## If something goes wrong

| Problem | What to do |
| --- | --- |
| Sign-in code expired or already used | Run `whatsapp-mcp login` again. |
| QR code expired | Start another attempt with **Show QR code**. |
| Admin page will not open | Run `whatsapp-mcp status`. Check logs with `whatsapp-mcp logs`. |
| Server is on another computer | Use its private address, not `127.0.0.1`. See [remote access](docs/REMOTE_ACCESS.md). |
| Client cannot connect | Check the endpoint, token and network access in [client setup](docs/CLIENTS.md). |
| Transcription is unavailable | Messaging still works. Check your provider key and quota using [AI setup](docs/AI.md). |

To stop the server without deleting accounts:

```sh
whatsapp-mcp stop
```

Start it again with `whatsapp-mcp start`. Do not use `docker compose down -v`
unless you intend to delete the saved accounts and messages.

For all everyday commands, run `whatsapp-mcp help`. See
[management commands](docs/OPERATIONS.md#management-from-any-folder) for setup
without administrator access or selection of multiple instances.

## More help

- [OAuth sign-in for Codex](docs/OAUTH.md)
- [Client configuration and file transfers](docs/CLIENTS.md)
- [Backups, updates and account recovery](docs/OPERATIONS.md)
- [AI providers and model settings](docs/AI.md)
- [Platforms, MCP tools, measurements and limitations](docs/REFERENCE.md)
- [Admin UI and verification details](docs/ADMIN_UI.md)
- [Development](CONTRIBUTING.md) · [Security](SECURITY.md)

## License

[GPL-3.0-or-later](LICENSE). Upstream MIT notices are retained in
[LICENSE-UPSTREAM-MIT](LICENSE-UPSTREAM-MIT) and [UPSTREAM.md](UPSTREAM.md).
Bundled dependencies have additional notices in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
