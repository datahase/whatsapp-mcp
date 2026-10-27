# Admin interface

The admin UI uses Next.js, React, Tailwind CSS and shadcn/ui. It is exported
at image build time, then embedded in the Go binary. The running container
has no Node.js runtime or frontend server. The theme follows the browser's
system light/dark preference.

Sign in at `/admin` using a one-time code from:

```sh
whatsapp-mcp login
```

Install the [host management command](OPERATIONS.md#management-from-any-folder)
once, then run it from any folder on the server.

A code expires in 10 minutes and can be used once. Opening its link fills the
form without consuming it. The existing admin password remains a fallback.
Only a hash is stored on the server. Sessions are HttpOnly, SameSite Strict,
last 12 hours, and expire when the service restarts. Sensitive responses are
not cached. The static export contains no account data or credentials.

New installations start empty. Choose **Add account**, then **Show QR code**.
Link the phone through WhatsApp's Linked devices screen. A phone-number
pairing option is also available. The UI polls for QR arrival and connection
changes, including the case where a QR arrives without a state change.
A pairing attempt expires after three minutes and can be retried.

Owner-created definitions are saved atomically in `/data/service/accounts.json`
with mode 0600. Each account has its own opaque ID and data directory. Back
up the entire data volume. Existing explicitly configured accounts remain
supported and are merged with saved accounts. Duplicate IDs fail startup.
The UI caps additions at 32 accounts as a resource safeguard; this is not a
claim that 32 simultaneous accounts have been tested.

When OAuth is configured, **Connect client** provides browser-login instructions
and revocable connections (see [OAuth](OAUTH.md)). In static-token mode it
provides the endpoint and masked bearer token,
with copy controls and configuration examples. Copying supports private HTTP
addresses, where browsers do not expose the modern clipboard API. The token
permits reading and sending from all accounts and must stay private.

Connection interruption appears in the UI and clears after polling recovers.
A session that expires returns the owner to sign-in. Disconnect/reset actions
use explicit dialogs and require the session's CSRF token. A reset archives
existing account data before a different phone can be linked.

## Build and verification

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
pnpm --dir web typecheck
go test -race -count=1 ./...
go vet ./...
go test -tags=e2e ./e2e
```

`make build`, `make test`, `make test-race` and `make e2e` build the frontend
first. Generated exports and installed packages are excluded from Git. The
export script hashes Next.js bootstrap scripts for CSP, without allowing
arbitrary inline script or eval. Dependency license texts are included in the
image under `/usr/share/licenses/whatsapp-mcp/FRONTEND_LICENSES.txt`.

Verified on 27 October 2026:

- Production frontend build and TypeScript check pass; production dependency
  audit reports no known vulnerabilities.
- Go unit/integration tests under the race detector, vet and tagged binary
  e2e checks pass. New cases cover concurrent account additions, restart
  restoration, registry write failure/corruption, CSRF boundaries and
  single-use/expired/concurrently consumed login codes.
- Native ARM64 container smoke suite passes, including authenticated media
  transfers, isolation, conversion and restart persistence.
- Browser checks cover one-time-code and password sign-in, account addition,
  account persistence after restart, QR arrival, client configuration,
  token masking, copy actions, sign-out, network interruption and recovery.
- Login, account list, pairing and client screens were checked at desktop and
  mobile widths and in light/dark themes. The WCAG A/AA automated audits of
  those screens report zero violations. Actual phone scanning and screen
  reader/device testing are still pending.
- The private ARM64 target is healthy with no preset accounts. One-time-code
  sign-in and clipboard fallback were verified over its actual private HTTP
  address. No WhatsApp message was sent and no account was paired in these
  checks.

The exported UI is about 1.1 MB. The ARM64 image is about 132 MB unpacked.
The deployed target used 14.3 MiB at idle with zero accounts; container smoke
runs with two unpaired accounts used about 20 MiB. These are not measurements
of paired accounts, initial history sync or real messaging workloads.

## Pairing lifecycle correction (27 October 2026)

QR collection has a three-minute deadline. The WhatsApp socket uses the
account lifetime instead. Finishing QR collection must not cancel a newly
paired socket or its automatic reconnect loop. Pairing watchers are tracked
through shutdown, and an old canceled attempt cannot overwrite a new attempt.
A Connected event that arrives before QR success remains Connected.

After a phone is linked, Connecting/Reconnecting screens show connection
progress instead of QR-scanning instructions. Normal connection progress is
not an error. Disconnect is unavailable while the connection is offline;
removing the device directly from WhatsApp's Linked devices screen is an
alternative. A failed logout keeps the local link unless WhatsApp confirms
removal. Logout errors close the dialog so the error remains visible.

Regression tests cover success/context lifetime, event ordering, stale
attempts, timeout/closed QR channels, initial-sync disconnects, and preserved
sessions after offline or failed logout. The race suite, vet, tagged e2e and
ARM64 container smoke suite pass. Synthetic browser checks cover linked-but-
connecting desktop/mobile screens, a connection loss while the logout dialog
is open, and visible errors after a failed removal request.

The previously interrupted real session returned a WhatsApp 401/logged_out
response on reconnect. Its cache and a stopped-volume backup were retained;
the server does not reuse a session WhatsApp has rejected. A fresh phone scan
is required to complete live acceptance of this correction.

Fresh-pair live acceptance subsequently passed on 27 October 2026. The owner
scanned the new QR on the existing account. The server reached Connected for
the same account ID and phone, received a history-sync event, and remained
connected on subsequent checks. A read-only MCP `list_chats` call returned
three cached chats through the authenticated endpoint. No messages were sent
and no provider analysis was requested. This verifies one real account;
two-account acceptance and intended-client compatibility remain separate checks.
