# Upstream provenance

This project started from [Sealjay/mcp-whatsapp](https://github.com/Sealjay/mcp-whatsapp)
at commit `078ac8c5e3f8a6f32c6a5902e4859f86c853330f` (16 October 2026), MIT
licensed. Its copyright and permission notice is retained in
`LICENSE-UPSTREAM-MIT`. This project as a whole is GPL-3.0-or-later, because
the distributed program links GPL-3.0 libsignal.

That code in turn credits Luke Harries' original WhatsApp MCP. It wraps
[whatsmeow](https://github.com/tulir/whatsmeow) (MPL-2.0).

## Baseline at import

Before changes, on macOS arm64 with Go 1.26.0: `go build ./...`, `go vet ./...`,
`go test -race ./...` (8 packages) and the tagged e2e suite all passed.

## What was kept

- whatsmeow adapter: message/history ingestion, LID normalisation, chat naming,
  media metadata extraction, history-sync anchoring, pairing-code formatting.
- SQLite message cache schema and query helpers (`internal/store`), file lock,
  permission helpers, and their tests and fixtures.
- Log redactor, filename sanitiser and path confinement (`internal/security`).
- In-memory send rate limiter (`internal/ratelimit`), minus the header bypass.
- FFmpeg invocation hardening (argument sanitising, minimal child environment).
- Tests for all of the above.

## What changed

| Area | Upstream | Here |
| --- | --- | --- |
| Accounts | One store, client and pairing state per process | `internal/accounts`: N isolated accounts, each with its own directory, lock, databases, client, limiter and lifecycle; mandatory `account_id`, no default |
| Lifecycle | Repeated `Connect` added logout handlers; SQL container not closed; connect errors dropped | One event handler per client, removed on close; container owned and closed; per-account states and bounded initial-connect retry; no automatic re-pairing |
| Tools | 42 tools | 16-tool allowlist (see README) |
| Sending | Fire-and-forget; no idempotency; receipts only logged | Durable operations keyed by idempotency key and fingerprint; message ID recorded before dispatch; accepted/delivered/read/failed/unknown; restart recovery; no blind resend |
| Recipients | Phone or JID | Exact identifiers only; names return candidates |
| Audio | `.ogg` always sent as push-to-talk; MP3/M4A as documents | Explicit modes: ordinary audio vs voice note; content verified with FFprobe |
| Voice-note conversion | Output in system temp dir, then rejected by the send path | Output in the account's work directory; regression test through the send adapter |
| Ogg parsing | Custom parser with unchecked slice bounds | Removed; FFprobe in a child process with a deadline |
| Remote media | Server-local `media_path` arguments; unbounded base64 resource | Streaming HTTP upload/download with opaque account-bound media IDs; bounded inline content; MCP resources removed |
| Media I/O | Whole files in memory | `UploadReader` / `DownloadToFile` through files; size checked before transfer |
| Auth | Optional bearer token; loopback unauthenticated | Bearer token always required for MCP/media; separate admin password with browser sessions, CSRF and Host/Origin checks |
| Rate-limit bypass | `X-Rate-Limit-Override` header | Removed |
| Gemini | None | `internal/gemini`: direct REST calls, jobs, cache, usage ledger, cleanup |
| Edits/revokes/view-once | Not applied | Applied to cached content; derived results invalidated; view-once media not retained |
| Packaging | Single-arch Dockerfile, no Compose | Multi-platform image, hardened Compose, CI and release workflows |
| Module path | `github.com/sealjay/mcp-whatsapp` | `github.com/datahase/whatsapp-mcp` |

Removed upstream features: groups management, polls/contact cards sending,
reactions, replies, edits/deletes sent by this service, typing/presence, read
marking, blocklist, privacy settings, terminal `login`, the `/pair` page and
`smoke` subcommand.
