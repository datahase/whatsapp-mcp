#!/bin/sh
set -eu

prefix=${1:-"$HOME/.local"}
case "$prefix" in
    -h|--help) printf '%s\n' 'Usage: sh scripts/install-cli.sh [PREFIX]' 'Also supports: curl -fsSL INSTALLER_URL | sh -s -- [PREFIX]' 'Default: $HOME/.local. Use /usr/local for a system installation.' 'WHATSAPP_MCP_REF selects the GitHub branch, tag or commit (default: main).'; exit 0 ;;
    /*) ;;
    *) printf '%s\n' 'Installation prefix must be an absolute path.' >&2; exit 1 ;;
esac
[ "$#" -le 1 ] || { printf '%s\n' 'Expected at most one installation prefix.' >&2; exit 1; }
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
