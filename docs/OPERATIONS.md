# Operating whatsapp-mcp

Commands assume the repository directory with `compose.yaml`, and Compose's
default project name `whatsapp-mcp` (volume `whatsapp-mcp_whatsapp-mcp-data`).

## Start, stop, logs, status

```sh
docker compose up -d
docker compose stop            # graceful: up to 20 s for in-flight work
docker compose logs -f --tail 100
curl -s -H "Authorization: Bearer $(cat secrets/mcp_token)" http://127.0.0.1:8765/api/status
```

Logs redact phone numbers and message bodies by default. Never set
`WHATSAPP_MCP_DEBUG=1` on a production instance.

## Accounts

- **Add:** add an entry to `accounts` in `config.yaml`, then
  `docker compose up -d` (configuration changes take effect on restart). Pair
  it from `/admin`.
- **Ids are permanent.** The `id` names the data directory. Renaming it later
  creates a new, empty slot.
- **Log out one account:** `/admin` → account → *Log out this linked device*.
  Cached messages stay readable; other accounts are unaffected.
- **Pair a different number in a slot:** log out, then *Archive and reset*.
  Data moves to `/data/archive/<id>-<timestamp>/`; delete it when you no longer
  need it. A slot refuses to pair a different number without a reset, so old
  history is never shown under the wrong number.
- If a phone removes the linked device, the account becomes `logged_out` and
  waits; nothing re-pairs automatically.

## Disk use and quotas

```sh
docker compose exec whatsapp-mcp sh -c 'du -sh /data /data/accounts/* /data/service'
```

- Downloaded media: shared 512 MiB cache, least-recently-used eviction
  (`limits.media_cache_bytes`). Evicted media can be fetched again only while
  WhatsApp still has it.
- Staged uploads expire after 24 hours unless a send is using them.
- Message databases: when all accounts together exceed
  `limits.message_cache_bytes`, new messages stop being stored and
  `get_account_status` shows `message_cache_full_since`. History is never
  deleted automatically. Raise the limit or archive accounts, then restart.
- Session databases (`whatsapp.db`) are never evicted.

## Backup

The volume contains linked-device credentials and decrypted messages. Treat
backups as secrets and store them encrypted.

```sh
docker compose stop
docker run --rm -v whatsapp-mcp_whatsapp-mcp-data:/data:ro -v "$PWD":/backup debian:trixie-slim \
  tar -C /data -czf /backup/whatsapp-mcp-$(date +%Y%m%d).tgz .
docker compose start
```

Also keep a copy of `config.yaml` and `secrets/` (separately protected), and
note the image version you were running.

## Restore or move to another host

1. Stop the old instance, and keep it stopped. Two running copies of one
   session will fight and can get the device logged out.
2. On the new host, create the volume and unpack the archive into it:

```sh
docker volume create whatsapp-mcp_whatsapp-mcp-data
docker run --rm -v whatsapp-mcp_whatsapp-mcp-data:/data -v "$PWD":/backup debian:trixie-slim \
  sh -c 'tar -C /data -xzf /backup/whatsapp-mcp-YYYYMMDD.tgz && chown -R 10001:10001 /data'
```

3. Restore `config.yaml` and `secrets/`, then run `docker compose up -d`.

To test a restore first, unpack into a differently named volume and run it
with networking disabled (`docker run --network none …`). Never connect a
restored copy while the original is still running.

## Upgrade and rollback

1. Back up as above, recording the current image tag.
2. Pull or build the new image, set `WHATSAPP_MCP_IMAGE` in `.env`, then run
   `docker compose up -d`.
3. To roll back, stop the container, restore the **pre-upgrade backup** into
   the volume, and start the old image. Do not run an older binary on data a
   newer version has already migrated.

Without a registry, transfer an image with:

```sh
docker save whatsapp-mcp:<tag> | gzip | ssh <host> 'docker load'
```

## Credential rotation

- **MCP token / admin password:** replace the file in `secrets/` (keep
  ownership and mode; on Linux, UID 10001 with mode 0440), then
  `docker compose up -d --force-recreate`. Update MCP clients. Admin sessions
  end on restart.
- **Gemini key:** replace `secrets/gemini_api_key_paid`, then recreate the
  container.
- **A WhatsApp session you suspect was copied:** remove the linked device on
  the phone (Settings → Linked devices), then reset and pair the slot again.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `host not allowed` (421) | Add the address the client uses to `allowed_hosts` |
| Container restarts at start | `docker compose logs`: config errors are listed together; secret files must be at least 16 characters and readable by UID 10001 |
| Account `error: … in use by another process` | Another container or process uses the same volume |
| Account `error: another client took over this session` | A second copy of the same data is running somewhere |
| Sends `rate limited … retry after` | App policy; wait, or change `limits` deliberately |
| Send `unknown` | WhatsApp did not confirm. Check the chat before sending again with a **new** key |
| Analysis `quota` / `credits depleted` | Provider billing or quota; fix it in your Gemini project. The service does not switch tiers |
| Analysis `daily … allowance reached` | Local limit in `gemini.*`; resets at 00:00 UTC |
