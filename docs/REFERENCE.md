# Technical reference

Start with the [setup steps](../README.md) to link a phone and connect a client.
This page covers the tool contract, supported platforms and operating limits.

## Capabilities

- **One endpoint, several accounts.** Each account is linked independently and
  has its own session, message cache, media, limits and state. Every
  account-specific tool requires `account_id`; there is no default account.
- **Reading:** contacts, chats, paginated messages with keyword/date/sender
  filters, message context, and a bounded request for older history.
- **Sending on explicit request.** The tool executes immediately (no second
  confirmation page). Every send takes an `idempotency_key`; retrying with the
  same key reuses the recorded operation instead of dispatching a fresh send.
  Exactly-once delivery cannot be guaranteed across network failures. Results are
  honest: `accepted` (WhatsApp's server took it), `delivered`, `read`,
  `failed`, or `unknown` (check before sending again). Names are never guessed:
  an ambiguous recipient returns candidates.
- **Remote media.** Upload a file from your laptop over authenticated HTTP,
  get an account-bound `media_id`, then send it. Received media is downloaded
  only when asked and comes back as a download URL plus small inline previews.
- **Optional AI analysis** (direct calls to each provider's native API, no
  SDKs or extra services): verbatim transcription in the original language
  and script, a separate translation, translation of text, image
  description/OCR/questions. Gemini is the default; OpenAI, Groq, OpenRouter,
  Anthropic and dedicated speech-to-text services (Deepgram, ElevenLabs,
  AssemblyAI) are built in. Results are cached per account and bounded by
  daily allowances. Use paid keys for personal media: there is no silent
  fallback to a free tier and no silent model upgrade. Basic WhatsApp
  features work with AI disabled.
- **Small footprint.** One Go binary plus a minimal FFmpeg/FFprobe build in a
  Debian slim image. The admin frontend is compiled during the build; the
  running image has no browser, Node.js, Python, local models or GPU dependency.

Not included: video understanding, calls, auto-replies, scheduled or bulk
messaging, group administration, status posts, a chat UI.

## Platform status

The Docker build targets `linux/amd64` and `linux/arm64`. A multi-platform
image lets Docker select the matching variant. On macOS and Windows it runs in Docker Desktop's Linux VM (Windows
uses WSL 2), whose own memory overhead is separate from the figures below.

| Host | Image variant | Status |
| --- | --- | --- |
| Linux ARM64 (NVIDIA Jetson Orin Nano, Ubuntu 22.04, Docker 29) | linux/arm64 | Runtime-tested natively (container acceptance suite) |
| macOS Apple Silicon, Docker Desktop | linux/arm64 | Runtime-tested (container acceptance suite) |
| Linux x86-64 | linux/amd64 | Earlier baseline tested under emulation; native AMD64 not verified in this local run |
| macOS Intel, Windows x86-64 (Docker Desktop/WSL 2) | linux/amd64 | Expected to work; not yet tested |
| Windows ARM64, other ARM64 boards/NAS | linux/arm64 | Provisional; not tested |
| 32-bit ARM/x86, native Windows containers | — | Not supported |

## MCP tools

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

## Resource use

| Measurement | Recorded value |
| --- | --- |
| Current admin UI ARM64 image, unpacked | About 132 MB |
| Exported admin frontend | About 1.1 MB, embedded in the Go binary |
| Idle with no accounts, ARM64 target | 14.3 MiB |
| Container smoke with two unpaired accounts | About 20 MiB |
| Earlier baseline AMD64 image, unpacked | About 114 MB; new UI image not remeasured on AMD64 |

These measurements cover unpaired accounts. Paired accounts, history sync
and real messaging workload peaks remain unverified. Docker Desktop's VM
memory is additional. Compose defaults to 512 MiB and one CPU, with a
read-only root filesystem, dropped capabilities and bounded logs.

## Limits

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

[Back to setup](../README.md)
