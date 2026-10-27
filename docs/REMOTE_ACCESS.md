# Access from another computer

If the service runs on another machine, run setup and sign-in-code commands
on that server. Open the admin page in your laptop's browser using the
server's private address, for example `http://192.168.1.20:8765/admin`.
`127.0.0.1` always means the device where the browser or MCP client is running.

To use the server from a laptop on a private network (LAN, VPN or
Tailscale), publish it on that interface and allow that address in `.env`:

```sh
BIND_IP=192.168.1.20                  # the server's address on that network
WHATSAPP_MCP_ALLOWED_HOSTS=192.168.1.20
```

Then run `docker compose up -d`. Requests whose `Host` or `Origin` header is
not in `allowed_hosts` (plus localhost) are rejected. Traffic is plain HTTP:
use it on a network that already encrypts device-to-device traffic (such as
Tailscale/WireGuard), or put a TLS reverse proxy in front. Do not publish it to
the internet without TLS and an access-control layer.

## Sign in and connect

1. On the server, run from any folder (after installing the
   [management command](OPERATIONS.md#management-from-any-folder)):

   ```sh
   whatsapp-mcp login
   ```

2. Enter that code in the admin page on your laptop.
3. Choose **Add account**, then **Show QR code**, and scan it with WhatsApp.
4. Open **Connect client**. With [OAuth enabled](OAUTH.md), follow the Codex
   commands and approve access in your browser. Static-token mode uses the
   endpoint and access token shown on that page.

The laptop and server must be on a network where they can reach each other.
For Tailscale, both devices must be connected to the same permitted tailnet.
You do not need to install Tailscale inside the application container.

[Back to setup](../README.md)
