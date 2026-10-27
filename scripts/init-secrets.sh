#!/usr/bin/env sh
# Creates ./secrets with a random MCP bearer token and admin password, and an
# empty Gemini key file (fill it in only if you enable Gemini). Existing files
# are never overwritten. Prints no secret values.
set -eu
cd "$(dirname "$0")/.."
umask 077
mkdir -p secrets
gen() { od -An -tx1 -N32 /dev/urandom | tr -d ' \n'; }
for name in mcp_token admin_password; do
  if [ ! -s "secrets/$name" ]; then gen > "secrets/$name"; echo "created secrets/$name"; fi
done
[ -e secrets/gemini_api_key_paid ] || : > secrets/gemini_api_key_paid
# The container runs as UID 10001. Docker Desktop (macOS/Windows) maps file
# access for you; on Linux the files must be readable by that UID.
if [ "$(uname -s)" = "Linux" ] && [ "$(id -u)" != "10001" ]; then
  if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo -n chown 10001:"$(id -g)" secrets/* && sudo -n chmod 0440 secrets/* && echo "secrets owned by UID 10001, readable by your group (mode 0440)"
  else
    echo "as root: chown 10001:$(id -g) secrets/* && chmod 0440 secrets/*" >&2
  fi
fi
echo "read the token for your MCP client with: cat secrets/mcp_token"
