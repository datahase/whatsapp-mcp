# whatsapp-mcp

A small self-hosted [MCP](https://modelcontextprotocol.io) server for **several
personal WhatsApp accounts** in one container. An MCP client (Claude Code,
Codex and others) can read and search chats, send text, images, video,
ordinary audio and playable voice notes, fetch received media to a remote
laptop, and optionally ask Gemini to transcribe or translate voice notes and
describe or read text in images.

> **Unofficial software.** It uses the reverse-engineered WhatsApp
> linked-device protocol through [whatsmeow](https://github.com/tulir/whatsmeow).
> It is not affiliated with or endorsed by Meta. WhatsApp can change the
> protocol or restrict accounts that use unofficial clients; no setting here
> guarantees account safety. Use it only with your own accounts and for
> messages you would send yourself.

## What it does

- **One endpoint, several accounts.** Each account is linked independently and
  has its own session, message cache, media, limits and state. Every
  account-specific tool requires `account_id`; there is no default account.
- **Reading:** contacts, chats, paginated messages with keyword/date/sender
  filters, message context, and a bounded request for older history.
- **Sending on explicit request.** The tool executes immediately (no second
  confirmation page). Every send takes an `idempotency_key`; retrying with the
  same key never sends twice, even after a timeout or restart. Results are
  honest: `accepted` (WhatsApp's server took it), `delivered`, `read`,
  `failed`, or `unknown` (check before sending again). Names are never guessed:
  an ambiguous recipient returns candidates.
- **Remote media.** Upload a file from your laptop over authenticated HTTP,
  get an account-bound `media_id`, then send it. Received media is downloaded
  only when asked and comes back as a download URL plus small inline previews.
- **Optional Gemini analysis** (direct HTTPS calls, no extra service):
  verbatim transcription in the original language and script, a separate
  translation, translation of text, image description/OCR/questions. Results
  are cached per account and bounded by daily allowances. Personal media
  always uses your **paid** key; there is no silent fallback to a free tier
  and no silent model upgrade. Basic WhatsApp features work without Gemini.
- **Small footprint.** One Go binary plus a minimal FFmpeg/FFprobe build in a
  Debian slim image (about 114 MB amd64 / 135 MB arm64 uncompressed). No
  browser, Node, Python, local models or GPU.

Not included: video understanding, calls, auto-replies, scheduled or bulk
messaging, group administration, status posts, a chat UI.

## Platforms

One image tag (`datahase/whatsapp-mcp`, not yet published) covers `linux/amd64` and `linux/arm64`; Docker picks the right
variant. On macOS and Windows it runs in Docker Desktop's Linux VM (Windows
uses WSL 2), whose own memory overhead is separate from the figures below.

| Host | Image variant | Status |
| --- | --- | --- |
| Linux ARM64 (NVIDIA Jetson Orin Nano, Ubuntu 22.04, Docker 29) | linux/arm64 | Runtime-tested natively (container acceptance suite) |
| macOS Apple Silicon, Docker Desktop | linux/arm64 | Runtime-tested (container acceptance suite) |
| Linux x86-64 | linux/amd64 | Built and acceptance-tested under emulation; native run in CI's amd64 job |
| macOS Intel, Windows x86-64 (Docker Desktop/WSL 2) | linux/amd64 | Expected to work; not yet tested |
| Windows ARM64, other ARM64 boards/NAS | linux/arm64 | Provisional; not tested |
| 32-bit ARM/x86, native Windows containers | — | Not supported |

## Quick start

Requirements: Docker Engine or Docker Desktop with Compose v2, persistent
storage, and outbound internet access.

```sh
git clone https://github.com/datahase/whatsapp-mcp && cd whatsapp-mcp
./scripts/init-secrets.sh            # random MCP token + admin password in ./secrets
cp config.example.yaml config.yaml   # edit account ids/labels
docker compose up -d --build
```

The port is published on **127.0.0.1:8765** only. Open
<http://127.0.0.1:8765/admin>, sign in with the contents of
`secrets/admin_password`, choose an account and start pairing. On the phone, go to
WhatsApp → Settings → Linked devices → Link a device, and scan the QR code
(or request a pairing code). Pair each account the same way. A pairing attempt
times out after three minutes; nothing re-pairs automatically.

### Access from another device

To use the server from a laptop on a private network (LAN, VPN or
Tailscale), publish it on that interface and allow that address:

```sh
echo "BIND_IP=192.168.1.20" > .env        # the server's address on that network
```

```yaml
# config.yaml
allowed_hosts:
  - 192.168.1.20
```

Then run `docker compose up -d`. Requests whose `Host` or `Origin` header is
not in `allowed_hosts` (plus localhost) are rejected. Traffic is plain HTTP:
use it on a network that already encrypts device-to-device traffic (such as
Tailscale/WireGuard), or put a TLS reverse proxy in front. Do not publish it to
the internet without TLS and an access-control layer.

## Connect an MCP client

The endpoint is `http://<host>:8765/mcp` (Streamable HTTP, stateless). Every
request needs `Authorization: Bearer <contents of secrets/mcp_token>`.
Examples are in [docs/CLIENTS.md](docs/CLIENTS.md).

### Sending a local file

MCP clients should not paste large files into the conversation. Upload first,
then pass the returned `media_id`:

```sh
curl -H "Authorization: Bearer $WHATSAPP_MCP_TOKEN" \
     -F file=@photo.jpg http://127.0.0.1:8765/api/accounts/personal/media
```

`get_media` returns a download URL for received attachments; fetch it with
the same header.

## Tools

| Tool | Purpose |
| --- | --- |
| `list_accounts` | Configured accounts, paired number and state |
| `get_account_status` | State, pairing need, last event/history sync, analysis usage |
| `search_contacts`, `list_chats` | Find contacts and chats in one account |
| `list_messages`, `get_message_context` | Filtered messages (25 per page, max 100) and context |
| `request_history_sync` | Ask the phone for up to 50 older messages in a chat |
| `send_message` | Send text (idempotent) |
| `send_media` | Send an image, H.264/AAC MP4 video, ordinary audio or document by `media_id` |
| `send_voice_note` | Send audio as a playable voice note (converted to Ogg/Opus if needed) |
| `get_send_status` | Look up a send after a timeout or restart |
| `get_media` | Fetch one attachment: metadata, download URL, small inline preview |
| `transcribe_audio` | Verbatim transcript, plus an optional separate translation |
| `translate_text` | Translate given text or a cached message |
| `analyze_image` | Describe, OCR, or answer a question about an image |
| `get_analysis_job` | Result or status of an analysis job |

Reading never marks messages as read, sends presence, or replies. Message
content and model output are treated as untrusted and never authorize a send.

## Gemini (optional)

1. Put your **paid** Gemini API key in `secrets/gemini_api_key_paid`.
2. In `config.yaml`, set `gemini.enabled: true`.
3. Restart: `docker compose up -d`.

Defaults: `gemini-3.5-transcribe` for transcripts, `gemini-3.5-flash-lite` for
translation and images, and `gemini-3.8-flash` only when a call sets
`high_quality: true`. App allowances: 30 audio minutes and 50 requests per UTC
day, 10 minutes per note. These are local limits, not a provider billing cap.
Uploaded audio is deleted from the provider after each job (deletion does not
guarantee zero provider-side retention). Calls may wait up to 45 seconds; longer
jobs return a `job_id` for `get_analysis_job`.

To check provider access without any WhatsApp data, run the fixture command
with a non-sensitive file:

```sh
docker compose run --rm -v "$PWD/sample.ogg:/fixtures/sample.ogg:ro" whatsapp-mcp \
  gemini-smoke -tier paid -audio /fixtures/sample.ogg
```

## Resource use (measured)

| Measurement | Value |
| --- | --- |
| Image size, uncompressed | amd64 114 MB, arm64 135 MB (Debian base ~80–100 MB of that) |
| Image transfer, `docker save \| gzip` | 46 MB (arm64) |
| Idle memory, two **unpaired** accounts | 13–19 MiB (macOS Docker Desktop and Jetson) |
| Startup to healthy | 1–3 s |

Paired-account memory, history-sync peaks and transfer peaks have not been
measured yet. Compose sets 512 MiB / 1 CPU limits, log rotation (3 × 5 MiB), a
PID limit, a read-only root filesystem, dropped capabilities and
`no-new-privileges`.

## Operation

Backups, upgrades, rollback, logout/reset, credential rotation and
troubleshooting: [docs/OPERATIONS.md](docs/OPERATIONS.md). Briefly:

- All state is in the `whatsapp-mcp-data` volume (`/data`). Stop the
  container before copying it. Never run two copies of the same data.
- `GET /api/status` (bearer) shows readiness. `/healthz` is liveness only, so
  unpaired or disconnected accounts do not cause restart loops.
- Log out or reset one account from the admin page; others keep working.

## Limits to know

- History, old media availability and edits/revokes depend on what WhatsApp
  delivers to a linked device; this is not a phone backup. Media can expire
  on WhatsApp's servers.
- View-once media is not retained. Edits and revokes are applied only when they
  come from the original sender, so a group admin's "delete for everyone" of
  someone else's message is not reflected in the cache.
- Send rate limits (45 s interval, 30/hour; stricter for new contacts) are
  this app's policy, not official WhatsApp limits.
- SQLite data is not encrypted at rest; use disk encryption on the host.
- Video files are transported only. Unsupported codecs fail clearly or can be
  sent as documents.

## Development

```sh
make test-race   # unit/integration tests (FFmpeg on PATH enables media tests)
make e2e         # real binary over HTTP, no account needed
make lint vuln   # gofmt, vet, staticcheck, govulncheck
make smoke       # build the image and run the container acceptance suite
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

GPL-3.0-or-later (see [LICENSE](LICENSE) and [NOTICE](NOTICE)). Parts are derived from
[Sealjay/mcp-whatsapp](https://github.com/Sealjay/mcp-whatsapp) under the MIT
license, whose notice is kept in [LICENSE-UPSTREAM-MIT](LICENSE-UPSTREAM-MIT);
see [UPSTREAM.md](UPSTREAM.md). Images also contain MPL-2.0 (whatsmeow) and
LGPL (FFmpeg) components; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
