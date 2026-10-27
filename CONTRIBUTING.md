# Contributing

Thanks for helping. Keep changes small and focused, and keep the service
lightweight: new runtime dependencies, services or tools need a clear reason.

## Setup

- Go (version from `go.mod`; the `toolchain` line pins the patch release)
- FFmpeg/FFprobe on `PATH` with libopus, libmp3lame and libx264 (tests
  generate synthetic fixtures; media tests skip without them)
- Docker with Buildx for image work
- Node.js 22 and pnpm 10.32.1 for the build-time admin frontend (not required to run the image)

```sh
make test-race   # all unit/integration tests
make e2e         # real binary over HTTP
make lint vuln   # gofmt, vet, staticcheck, govulncheck
make smoke       # container acceptance suite for the local architecture
```

## Rules

- **No personal data in the repository**: no phone numbers, chats, media,
  session databases, keys or logs from real accounts. Tests use synthetic
  fixtures only. Do not attach such data to issues.
- Behaviour changes need tests that exercise the behaviour: boundaries,
  failure paths and side effects, not just happy paths.
- Every account-specific tool must require `account_id`; never add a default
  account or cross-account search.
- Do not add tools that send without an explicit client call, auto-reply, or
  run bulk actions.
- After changing dependencies, run `scripts/third-party-licenses.sh` and review
  new licenses in `THIRD_PARTY_NOTICES.md`.
- Bump whatsmeow deliberately (roughly monthly), run the parity script and the
  full test suite, and note it in `CHANGELOG.md`.

## Commit and pull requests

Describe what changed and why, and how you tested it. CI runs formatting,
vet, staticcheck, race tests, end-to-end tests, vulnerability scanning and
native amd64/arm64 container checks without any credentials.
