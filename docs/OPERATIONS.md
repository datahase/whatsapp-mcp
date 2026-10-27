# Operating whatsapp-mcp

Run `whatsapp-mcp setup` from any folder to deploy the published Docker image.
Normal management commands also work from any folder. Manual Compose commands
below use the configuration directory printed by setup. The default project
name is `whatsapp-mcp` (volume `whatsapp-mcp_whatsapp-mcp-data`).

## One-command setup

Install the CLI and start the setup wizard:

```sh
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/main/scripts/install-cli.sh | sudo sh -s -- /usr/local --setup
```

Docker with Compose v2 must already be installed and accessible to the invoking
user. A system-wide installation uses sudo only to install the executable;
setup runs as the invoking user. Tailscale modes also require Tailscale installed
and signed in on the host, with sudo available for network configuration.

| Option | Purpose |
| --- | --- |
| `--mode local` | Bind locally; static-token MCP access |
| `--mode tailscale` | Private Tailscale Serve HTTPS and OAuth |
| `--mode funnel` | Public Tailscale Funnel HTTPS and OAuth |
| `--mode https --url https://HOST` | Configure OAuth for an existing HTTPS proxy; does not create the proxy |
| `--dir PATH` | Settings directory; default `$XDG_DATA_HOME/whatsapp-mcp` or `$HOME/.local/share/whatsapp-mcp` |
| `--port NUMBER` | Docker host port; default 8765 |
| `--https-port NUMBER` | Tailscale port: 443 (default), 8443 or 10000 |
| `--env-file PATH` | Import advanced environment settings on first setup |
| `--image REFERENCE` | Image tag or digest; defaults to this CLI release on a fresh install |
| `--project NAME` | Separate Compose project; also choose a different directory and port |
| `--yes` | Skip interactive prompts; fresh default is local access |
| `--no-pull` | Use an image already present locally |

Keep the imported `.env` private. See [`.env.example`](../.env.example) for AI
provider keys, quotas and other supported settings. Setup never executes the
file as shell code. Credentials are generated when absent; settings files are
written with private permissions. Do not put secrets in command arguments.

Setup downloads a versioned Compose file, pulls the image, validates the app
configuration, waits for health, and prints an admin sign-in code. It refuses
to replace a Tailscale route already using the selected HTTPS port. Funnel is
enabled only after the OAuth-enabled application is healthy.

Rerun `whatsapp-mcp setup --yes` to reapply settings. Existing Compose deployments
are found by project labels, including stopped containers. Their original
configuration directory, `.env`, network settings and data volume are reused.
Without a container, pass `--dir` to reuse a nondefault settings directory.
Existing `.env` files are backed up under `backups/setup-TIMESTAMP-PID/`.
To change advanced settings, edit that deployment's `.env` and rerun setup.
`--env-file` imports are only allowed for fresh installations.

Changing an existing access mode or proxy port requires an explicit `.env` and
proxy change; setup does not rewrite existing routes. On failed configuration
validation, the running container is left alone and its previous `.env` is
restored. On failed startup, setup attempts to restore the previous image and
settings. Data is retained, but database migrations are not undone. Always
make a data backup before a version upgrade. If interrupted, inspect the
configuration directory's `.setup-lock` before removing that stale lock and
retrying. Network-policy or Tailscale approval failures require correction on
the host before retrying.

## Start, stop, logs, status

```sh
whatsapp-mcp start
whatsapp-mcp stop              # retain all account data
whatsapp-mcp logs --follow
whatsapp-mcp status
```

Logs redact phone numbers and message bodies by default. Never set
`WHATSAPP_MCP_DEBUG=1` on a production instance.

## Accounts

- **Add:** choose **Add account** in `/admin`, then **Show QR code**. Account
  definitions and stores persist in the data volume; no restart is needed.
  `WHATSAPP_MCP_ACCOUNTS` remains available for advanced preconfiguration.
- **Ids are permanent.** The `id` names the data directory. Renaming it later
  creates a new, empty slot.
- **Log out one account:** `/admin` → account → *Disconnect account*.
  Cached messages stay readable; other accounts are unaffected.
- **Pair a different number in a slot:** disconnect, then *Reset for another phone*.
  Data moves to `/data/archive/<id>-<timestamp>/`; delete it when you no longer
  need it. A slot refuses to pair a different number without a reset, so old
  history is never shown under the wrong number.
- If a phone removes the linked device, the account becomes `logged_out` and
  waits; nothing re-pairs automatically.

## Disk use and quotas

```sh
docker compose exec whatsapp-mcp sh -c 'du -sh /data /data/accounts/* /data/service'
```

- Downloaded media: shared 512 MiB cache, least-recently-used eviction
  (`limits.media_cache_bytes`). Evicted media can be fetched again only while
  WhatsApp still has it.
- Staged uploads expire after 24 hours unless a send is using them.
- Message databases: when all accounts together exceed
  `limits.message_cache_bytes`, new messages stop being stored and
  `get_account_status` shows `message_cache_full_since`. History is never
  deleted automatically. Raise the limit or archive accounts, then restart.
- Session databases (`whatsapp.db`) are never evicted.

## Backup

The volume contains linked-device credentials and decrypted messages. Treat
backups as secrets and store them encrypted.

```sh
docker compose stop
docker run --rm -v whatsapp-mcp_whatsapp-mcp-data:/data:ro -v "$PWD":/backup debian:trixie-slim \
  tar -C /data -czf /backup/whatsapp-mcp-$(date +%Y%m%d).tgz .
docker compose start
```

Also keep a copy of `.env` (separately protected; it holds credentials), and
note the image version you were running.

## Restore or move to another host

1. Stop the old instance, and keep it stopped. Two running copies of one
   session will fight and can get the device logged out.
2. On the new host, create the volume and unpack the archive into it:

```sh
docker volume create whatsapp-mcp_whatsapp-mcp-data
docker run --rm -v whatsapp-mcp_whatsapp-mcp-data:/data -v "$PWD":/backup debian:trixie-slim \
  sh -c 'tar -C /data -xzf /backup/whatsapp-mcp-YYYYMMDD.tgz && chown -R 10001:10001 /data'
```

3. Restore `.env`, then run `docker compose up -d`.

To test a restore first, unpack into a differently named volume and run it
with networking disabled (`docker run --network none …`). Never connect a
restored copy while the original is still running.

## Upgrade and rollback

1. Back up as above, recording the current image tag.
2. Pull or build the new image, set `WHATSAPP_MCP_IMAGE` in `.env`, then run
   `docker compose up -d`.
3. To roll back, stop the container, restore the **pre-upgrade backup** into
   the volume, and start the old image. Do not run an older binary on data a
   newer version has already migrated.

Without a registry, transfer an image with:

```sh
docker save whatsapp-mcp:<tag> | gzip | ssh <host> 'docker load'
```

## Credential rotation

- **MCP token / admin password:** change the value in `.env`, then
  `docker compose up -d` (recreates the container). Update MCP clients. Admin
  sessions end on restart.
- **AI provider keys:** change `GEMINI_API_KEY` (or the provider's key) in
  `.env`, then `docker compose up -d`.
- Variables are visible to anyone who can run `docker inspect` on the host.
  If that matters, use `*_FILE` variables (for example
  `WHATSAPP_MCP_TOKEN_FILE=/run/secrets/token`) with Docker secrets instead.
- **A WhatsApp session you suspect was copied:** remove the linked device on
  the phone (Settings → Linked devices), then reset and pair the slot again.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `host not allowed` (421) | Add the address the client uses to `allowed_hosts` |
| Account `error: … in use by another process` | Another container or process uses the same volume |
| Account `error: another client took over this session` | A second copy of the same data is running somewhere |
| Sends `rate limited … retry after` | App policy; wait, or change `limits` deliberately |
| Send `unknown` | WhatsApp did not confirm. Check the chat before sending again with a **new** key |
| Analysis `quota` / `credits depleted` | Provider billing or quota; fix it with the provider. The service does not switch tiers or providers |
| Analysis `daily … allowance reached` | Local limit (`AI_DAILY_REQUESTS`, `AI_DAILY_AUDIO_MINUTES`); resets at 00:00 UTC |
| Container restarts at start | `docker compose logs`: config errors are listed together, e.g. a missing provider key or model |

## Admin sign-in and accounts

Run `whatsapp-mcp login` from any folder on the Docker host. Enter the short code on `/admin/login`. Codes work once,
expire after 10 minutes, and only a hash is stored. Opening a code link does
not consume it. The admin password remains available as a fallback. Sessions
expire after 12 hours or whenever the service restarts.

Use **Add account** in the admin UI, then **Show QR code** to link a phone.
Accounts created in the UI are saved in `/data/service/accounts.json`.
Back up the entire data volume, including that registry. Advanced accounts
specified in `.env`/YAML continue to work; their IDs must not be renamed after
linking. Leave `WHATSAPP_MCP_ACCOUNTS` empty on new installations.

## Building the admin frontend locally

The image build installs frontend tooling in a separate stage and copies
only static assets into the Go build. Node.js is absent from the runtime.
For local Go development and tests, first run:

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go test -race ./...
go build ./cmd/whatsapp-mcp
```

The static export is generated under `internal/httpapi/static/` and excluded
from Git. Rebuild it after changing frontend source. Next.js bootstrap scripts
have build-generated CSP hashes; inline scripts are not broadly allowed.

## OAuth client access

For browser-approved Codex connections, configure a private HTTPS origin and
follow [OAuth setup](OAUTH.md). Use **Connect client > Revoke** to remove a
client's access immediately. This keeps the WhatsApp phone linked and saved
messages available. Disconnecting a phone is a separate account action.

Back up `/data/service/oauth.db` with the rest of the data volume. Changing
`WHATSAPP_MCP_PUBLIC_URL` changes the resource audience; clients must sign in
again at the new endpoint. Pending consent requests expire or disappear on
restart; approved connections persist.

## Management from any folder

Install the host command once from the downloaded project:

```sh
sudo sh scripts/install-cli.sh /usr/local
```

For installation without a checkout:

```sh
curl -fsSL https://raw.githubusercontent.com/datahase/whatsapp-mcp/main/scripts/install-cli.sh | sudo sh -s -- /usr/local
```

The piped installer downloads the host CLI over HTTPS. It checks the script
marker and shell syntax before replacing the executable. Download or validation
failure leaves an existing installation unchanged. For a pinned installation, use the chosen commit in
the installer URL and pass `WHATSAPP_MCP_REF` with that same commit to `sh`.
Omitting `--setup` installs only the CLI. Add `--setup` to deploy the server
using the [setup wizard](#one-command-setup). Docker must already be installed.

It copies a standalone script to `/usr/local/bin/whatsapp-mcp`. It uses Docker
service labels to find the existing container, including stopped containers.
It does not depend on the checkout, Compose file, shell aliases or saved secrets.
Docker must be running and accessible to your user. Run it on the server, not
on a client laptop that has no access to that Docker daemon.

```sh
whatsapp-mcp login            # one-time admin code
whatsapp-mcp status           # state and health
whatsapp-mcp logs             # last 50 log lines
whatsapp-mcp logs --follow    # stream logs
whatsapp-mcp stop             # retain all data
whatsapp-mcp start
whatsapp-mcp restart
```

For a user-only installation, run `sh scripts/install-cli.sh` and add
`$HOME/.local/bin` to your PATH. The installer refuses to overwrite an unrelated
executable. Re-run the installer after updating the script. The command and its
Docker dependency run on the host; the app inside the container is unchanged.

If multiple instances exist, the command asks you to choose one. Use
`whatsapp-mcp --container NAME login` or set `WHATSAPP_MCP_CONTAINER`. Non-Compose
containers also work with an explicit name. It never chooses the first match.

`whatsapp-mcp setup` provisions and reapplies published-image deployments.
Source builds and the manual backup examples use Compose from the configuration
directory. The normal management commands above need no folder change. An uninstall removes only the installed script; it does not stop the
container or remove data.
