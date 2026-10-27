# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report it privately through GitHub: open the repository's **Security** tab
and choose **Report a vulnerability**
(<https://github.com/datahase/whatsapp-mcp/security/advisories/new>).

Include the version or commit, your configuration (with secrets removed), and
steps to reproduce. Never include tokens, API keys, `whatsapp.db` or message
data.

## Scope and design

- The server is for one owner. The MCP bearer token grants full read/send
  access to every configured account; treat it like a password.
- The admin page (pairing, logout, reset) uses a separate password, server-side
  sessions with HttpOnly SameSite=Strict cookies, CSRF tokens, login rate
  limiting and strict security headers.
- All routes except `/healthz` check `Host` and `Origin` against
  `allowed_hosts` (DNS-rebinding and cross-site protection).
- Default publishing is localhost only. Plain HTTP is intended for loopback or
  an already-encrypted private network. Internet exposure needs TLS and access
  control in front, and is not a supported default.
- Received messages, media and model output are untrusted. They cannot trigger
  sends; tools only act on explicit client calls.
- Media handles are opaque and bound to one account; files are confined to the
  account directory, and CDN URLs and media keys never appear in tool output.
- Data at rest (SQLite, media) is not encrypted by the application.

## Supported versions

Only the latest release receives fixes until a support policy is published.
