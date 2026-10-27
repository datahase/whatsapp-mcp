#!/usr/bin/env sh
# Creates .env from .env.example with a random MCP token and admin password.
# An existing .env is never overwritten, and no secret values are printed.
set -eu
cd "$(dirname "$0")/.."
if [ -e .env ]; then
  echo ".env already exists; leaving it unchanged"
  exit 0
fi
umask 077
gen() { od -An -tx1 -N32 /dev/urandom | tr -d ' \n'; }
token=$(gen)
admin=$(gen)
sed -e "s/^WHATSAPP_MCP_TOKEN=.*/WHATSAPP_MCP_TOKEN=$token/" \
    -e "s/^WHATSAPP_MCP_ADMIN_PASSWORD=.*/WHATSAPP_MCP_ADMIN_PASSWORD=$admin/" \
    .env.example > .env
chmod 600 .env
echo "created .env (mode 600) with a random token and admin password"
echo "show the MCP token with: grep ^WHATSAPP_MCP_TOKEN= .env"
