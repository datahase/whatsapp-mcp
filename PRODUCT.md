# Product

<!-- impeccable:product-schema 1 -->

## Platform
web

## Stack
Go HTTP and MCP service. Next.js and shadcn/ui admin frontend, statically exported during image build. No Node runtime in the final container.

## Users
One owner managing their own WhatsApp accounts, from desktop or mobile over localhost or a private network.

## Product Purpose
Connect WhatsApp accounts to MCP clients and make messaging and selected media analysis available through explicit requests.

## Operating Context
Docker deployment with private persistent storage. Admin login uses a server-issued one-time code or the admin password. MCP clients can use browser-approved OAuth when an HTTPS origin is configured; Codex is the first verified client. WhatsApp linking requires the owner's phone.

## Capabilities and Constraints
Add accounts through the interface without predefined Personal or Secondary profiles. Persist owner-created accounts across restarts. Preserve existing configured accounts and their data. Expose MCP connection details after admin authentication. No full chat interface, auto-replies, or account data in static assets.

## Product Principles
- Add and link an account without configuration-file editing.
- Keep credentials private and show clear connection and recovery states.
- Keep the running service lightweight.

## Brand Commitments
Plain functional copy. Next.js and shadcn/ui requested by the owner. Theme follows the system setting.
