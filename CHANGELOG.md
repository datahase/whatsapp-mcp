# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
before 1.0, minor versions may contain breaking changes.

## [0.6.2] - 2026-10-29

### Fixed
- Treat all history messages and queries as explicit text, including media URLs.
  Prevent automatic media decoding/downloads from blocking local indexing.
- Resume failed indexing in the same authorized scope without losing completed
  progress, and show the current indexing attempt rather than stale failures.

### Added
- Search the history chat picker by name or number, with named chats first and
  selection counts that remain stable while filtering.
- Synthetic URL-input regression coverage for document, search and question
  embedding paths.

## [0.6.1] - 2026-10-29

### Fixed
- Retry interrupted FFmpeg source downloads with an alternate official hostname,
  keeping the pinned source checksum verification.
- Show current synced contact names in chat lists instead of stale numeric
  identifiers, including contacts stored under linked IDs or mapped phone IDs.
- Preserve group names and chat selection identifiers; retain identifier
  fallbacks when a contact name is unavailable.

## [0.6.0] - 2026-10-29

### Added
- Optional local WhatsApp-history search with pinned EmbeddingGemma 2, scoped
  incremental SQLite vector/keyword indexes, resumable jobs and source context.
- Scoped conversational sessions, cited provider answers with separate external
  consent, and exhaustive chronological action-review workflows.
- Post-link monitoring prompt and history selection/progress/source-inspection
  UI. Monitoring selection leaves existing WhatsApp tool permissions unchanged.
- Optional CPU runtime image and reproducible synthetic verification.

### Fixed
- Preserve reply references and deletion tombstones during cache ingestion.
- Prevent a cancelled old index from pausing a newly selected scope, clear queued
  authorization on scope/session deletion, and enforce one-time login-code claims.
- Invalidate history derivatives transactionally after source changes, and avoid
  redispatching possibly billed history generation after a restart.

## [0.5.0] - 2026-10-29

### Added
- Vercel AI Gateway provider (`vercel`) with `AI_GATEWAY_API_KEY` and `_FILE`
  support, independently selectable for transcription or all AI features.
- Native Gateway recorded-audio transcription using Gemini, with original
  Ogg/Opus input and the versioned REST protocol. Translation and images use
  Gateway's OpenAI-compatible endpoint.
- Configuration, authentication, audio serialization and provider-error tests,
  plus Gateway setup and quota documentation.

## [0.4.1] - 2026-10-28

### Fixed
- Advertise `WhatsApp MCP` in companion pairing metadata instead of the library
  default device name. Existing sessions are preserved; an older phone entry
  may keep its previous label until relinked.

## [0.4.0] - 2026-10-28

### Added
- One-command Docker deployment with `whatsapp-mcp setup` and installer `--setup`.
- Interactive or unattended local, private Tailscale, public Funnel and existing
  HTTPS proxy setup, plus advanced environment-file import.
- Existing-deployment discovery, protected credential generation, configuration
  checks, health-gated startup and previous-image recovery after startup failure.
- Native container CI checks for fresh setup, reruns and failed-update recovery.

### Fixed
- Admin sign-in codes show the configured HTTPS origin for remote deployments.
- Serve embedded brand images without requiring an admin session.

### Changed
- Use the supplied MCP logo in the login page, navigation, browser icons and README.
- Copy approved brand masters during the frontend build, preserving transparency.

## [0.3.0] - 2026-10-28

### Added
- Add an installable host management command: `whatsapp-mcp login`, status, logs,
  start, stop and restart work from any folder without container names.
- Support standalone and curl-piped CLI installation from GitHub, with HTTPS
  download, optional ref selection and validation before replacing an install.

- MCP OAuth authorization-code flow with S256 PKCE, discovery, dynamic public
  client registration, rotating refresh tokens and owner-managed revocation.
- Browser client approval and OAuth-first connection instructions when a
  public HTTPS origin is configured; optional OAuth-only API mode.
- Next.js and shadcn/ui admin interface, exported at build time and embedded
  in the Go binary; system light/dark theme, no Node.js runtime.
- Add accounts through the admin UI, persisted in the private data volume.
  Fresh installations start with no preset accounts.
- Single-use admin sign-in codes via `whatsapp-mcp login-code` (10 minutes).
- Authenticated client connection screen with endpoint/token copy controls.

### Changed
- Fresh installations add accounts in the browser instead of preset profiles.
- Simplified installation and client guides, including Tailscale Serve/Funnel.

### Fixed
- Keep the WhatsApp socket alive after pairing; separate QR deadlines from the
  account connection lifetime and reject stale pairing attempts.
- Preserve the saved link when an offline logout cannot be confirmed, and show
  connection errors outside the closed confirmation dialog.
- Redact protocol-library phone/JID logs during pairing and sync.

## [0.2.0] - 2026-10-27

### Added
- AI providers via their native APIs: Gemini (default), OpenAI, Groq,
  OpenRouter, Anthropic, any OpenAI-compatible API, and dedicated
  speech-to-text services Deepgram, ElevenLabs Scribe and AssemblyAI.
- Separate transcription provider (`AI_TRANSCRIBE_PROVIDER`), with its own key.
- Model IDs configurable through `AI_TRANSCRIBE_MODEL`, `AI_MODEL`,
  `AI_QUALITY_MODEL`; `:free` model IDs refused unless explicitly allowed.
- Optional WAV conversion for providers that do not accept Ogg/Opus.

### Changed
- Configuration and secrets come from environment variables (`.env` with
  Compose). `config.yaml` is optional; `*_FILE` variables still work.
- `scripts/init-env.sh` replaces `scripts/init-secrets.sh`.
- Config section `gemini:` renamed `ai:` (the old name is still accepted).
- `gemini-smoke` renamed `ai-smoke` (old name still works).

## [0.1.0] - 2026-10-27

### Changed
- License: GPL-3.0-or-later (the binary links GPL-3.0 libsignal); upstream MIT
  notice retained in LICENSE-UPSTREAM-MIT.

### Added
- Multi-account core: isolated per-account stores, locks, clients, limits and
  lifecycle; mandatory `account_id` on every account-specific tool.
- 16-tool MCP allowlist for reading, idempotent sending, media and analysis.
- Durable send operations with idempotency keys, pre-dispatch message IDs,
  receipt tracking and restart recovery.
- Authenticated streaming media upload/download with account-bound media IDs.
- Direct Gemini integration: transcription, translation, image analysis,
  per-account result cache, usage allowances, provider-file cleanup.
- Admin page with login sessions, CSRF protection and per-account pairing.
- Multi-platform container (amd64/arm64) with a minimal FFmpeg build,
  hardened Compose file, CI and a prepared release workflow.

### Changed (from upstream Sealjay/mcp-whatsapp 078ac8c)
- Voice-note conversion writes inside the account directory (fixes the
  converted-file rejection).
- Ordinary audio and voice notes are separate send modes; media is classified
  by content.
- The custom Ogg parser was replaced by deadline-bound FFprobe validation.
- Media transfers stream through files instead of loading whole files.

### Removed
- Upstream group management, polls/contact cards, reactions, replies,
  edit/delete, typing/presence, read marking, blocklist, privacy tools,
  MCP media resources and the rate-limit override header.
