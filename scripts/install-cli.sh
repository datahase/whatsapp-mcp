#!/bin/sh
set -eu

prefix=$HOME/.local
setup_now=false
case "${1:-}" in
    -h|--help)
        printf '%s\n' 'Usage: sh scripts/install-cli.sh [PREFIX] [--setup [SETUP_OPTIONS]]' 'Default prefix: $HOME/.local. Use /usr/local for a system installation.' 'Add --setup to deploy the Docker server after installation.' 'WHATSAPP_MCP_REF selects the GitHub branch, tag or commit (default: main).'
        exit 0 ;;
    /*) prefix=$1; shift ;;
    '') ;;
    --setup) ;;
    *) printf '%s\n' 'Installation prefix must be an absolute path.' >&2; exit 1 ;;
esac
if [ "${1:-}" = --setup ]; then setup_now=true; shift; fi
if [ "$setup_now" = false ] && [ "$#" -ne 0 ]; then printf '%s\n' 'Unexpected installer arguments. Use --setup before deployment options.' >&2; exit 1; fi
target=$prefix/bin/whatsapp-mcp
if [ -e "$target" ] || [ -L "$target" ]; then
    if [ -L "$target" ] || ! grep -q '^# whatsapp-mcp Docker management CLI$' "$target"; then
        printf 'Refusing to replace an existing unrelated executable: %s\n' "$target" >&2
        exit 1
    fi
fi

source_file=
# A checkout can install offline. Piped or standalone installers fetch the CLI.
case "$0" in
    *install-cli.sh)
        if [ -f "$0" ]; then
            source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
            if [ -f "$source_dir/whatsapp-mcp" ]; then
                source_file=$source_dir/whatsapp-mcp
            fi
        fi
        ;;
esac
temp_dir=
trap 'if [ -n "$temp_dir" ]; then rm -rf "$temp_dir"; fi' 0
trap 'exit 1' HUP INT TERM
if [ -z "$source_file" ]; then
    ref=${WHATSAPP_MCP_REF:-main}
    case "$ref" in
        *[!a-zA-Z0-9._/-]*|*..*|-*) printf '%s\n' 'Invalid WHATSAPP_MCP_REF.' >&2; exit 1 ;;
    esac
    command -v curl >/dev/null 2>&1 || { printf '%s\n' 'curl is required to download the CLI.' >&2; exit 1; }
    temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/whatsapp-mcp-install.XXXXXXXX")
    source_file=$temp_dir/whatsapp-mcp
    curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location \
        "https://raw.githubusercontent.com/datahase/whatsapp-mcp/$ref/scripts/whatsapp-mcp" \
        --output "$source_file"
fi
if ! grep -q '^# whatsapp-mcp Docker management CLI$' "$source_file" || ! sh -n "$source_file"; then
    printf '%s\n' 'Invalid CLI download or local source. Nothing was installed.' >&2
    exit 1
fi
mkdir -p "$prefix/bin"
install -m 755 "$source_file" "$target"
printf 'Installed %s\n' "$target"
case ":$PATH:" in
    *":$prefix/bin:"*) printf '%s\n' 'Ready: whatsapp-mcp status' ;;
    *) printf 'Add this directory to your PATH: %s/bin\nThen run: whatsapp-mcp status\n' "$prefix" ;;
esac

if [ "$setup_now" = true ]; then
    if [ -n "$temp_dir" ]; then rm -rf "$temp_dir"; temp_dir=; fi
    # Install system-wide with sudo, then keep deployment files owned by the
    # invoking user. Docker access is checked again for that user by setup.
    if [ "$(id -u)" -eq 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
        exec sudo -H -u "$SUDO_USER" "$target" setup "$@"
    fi
    exec "$target" setup "$@"
fi
