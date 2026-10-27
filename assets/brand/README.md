# WhatsApp MCP logo

- `whatsapp-mcp-logo-transparent.png`: transparent master used in the admin UI.
- `whatsapp-mcp-icon.png`: background-backed icon used for browser icons and the
  repository README.

The frontend build copies these masters into its generated public assets.
Update the masters here; do not edit the generated copies in `web/public/brand`
or `internal/httpapi/static/brand`. The build preserves the original pixels and
alpha channel. Both light and dark UI themes use a light icon backing so the
green mark stays visible.
