# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
before 1.0, minor versions may contain breaking changes.

## [Unreleased]

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
