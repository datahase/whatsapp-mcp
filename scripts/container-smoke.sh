#!/usr/bin/env bash
# Container acceptance check against a built image, using the real
# compose.yaml with a throwaway project directory. No WhatsApp account is
# paired and no message is sent. Needs docker (with compose), curl, python3
# and a host ffmpeg with lavfi/libx264/libmp3lame for fixtures, or
# FIXTURES=<dir> pointing at fixtures generated elsewhere.
#
#   scripts/container-smoke.sh whatsapp-mcp:local [linux/arm64]
set -euo pipefail
IMAGE=${1:?image}
PLATFORM=${2:-}
REPO=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PROJECT=wamcp-smoke-$$
PORT=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
export WHATSAPP_MCP_IMAGE=$IMAGE HOST_PORT=$PORT BIND_IP=127.0.0.1
[ -n "$PLATFORM" ] && export DOCKER_DEFAULT_PLATFORM=$PLATFORM
dc() { docker compose -p "$PROJECT" --project-directory "$WORK" -f "$REPO/compose.yaml" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT
pass() { echo "PASS $*"; }
fail() { echo "FAIL $*"; dc logs --no-color | tail -40; exit 1; }

# Settings as a user would create them: only a .env file, no config file.
cp "$REPO/.env.example" "$WORK/.env.example"
(cd "$WORK" && mkdir -p scripts && cp "$REPO/scripts/init-env.sh" scripts/ && sh scripts/init-env.sh >/dev/null)
printf 'WHATSAPP_MCP_ACCOUNTS=personal:Personal,second:Second\n' >> "$WORK/.env"
TOKEN=$(sed -n "s/^WHATSAPP_MCP_TOKEN=//p" "$WORK/.env")
AUTH="Authorization: Bearer $TOKEN"
BASE="http://127.0.0.1:$PORT"

# Synthetic fixtures (no personal data).
F=$WORK/fixtures; mkdir -p "$F"
if [ -n "${FIXTURES:-}" ]; then
  cp "$FIXTURES"/* "$F/"   # pre-generated (hosts without ffmpeg)
else
  ff() { ffmpeg -hide_banner -loglevel error -y "$@"; }
  ff -f lavfi -i sine=frequency=440:duration=3 -c:a libopus "$F/voice.ogg"
  ff -f lavfi -i sine=frequency=440:duration=3 -c:a libmp3lame "$F/song.mp3"
  ff -f lavfi -i sine=frequency=440:duration=3 "$F/clip.wav"
  ff -f lavfi -i sine=frequency=440:duration=3 -c:a aac "$F/memo.m4a"
  ff -f lavfi -i testsrc=size=160x120:rate=10:duration=2 -f lavfi -i sine=duration=2 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$F/video.mp4"
  ff -f lavfi -i testsrc=size=64x64:duration=1 -frames:v 1 "$F/image.png"
  ff -f lavfi -i testsrc=size=64x64:duration=1 -frames:v 1 "$F/photo.jpg"
  printf 'OggS\0\2garbage-not-a-real-stream' > "$F/broken.ogg"
  head -c 40 "$F/voice.ogg" > "$F/truncated.ogg"
  printf '%%PDF-1.4\n%%synthetic\n' > "$F/doc.pdf"
fi

start=$(date +%s)
dc up -d --no-build >/dev/null || fail "compose up"
for i in $(seq 1 60); do curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS "$BASE/healthz" >/dev/null || fail "healthz"
pass "started and healthy in $(( $(date +%s) - start ))s"

CID=$(dc ps -q whatsapp-mcp)
[ "$(docker inspect -f '{{.Config.User}}' "$CID")" = "10001:10001" ] && pass "runs as UID 10001" || fail "user"
[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$CID")" = "true" ] && pass "read-only root filesystem" || fail "rootfs"
docker exec "$CID" sh -c 'touch /usr/x' 2>/dev/null && fail "root fs writable" || pass "root fs write rejected"
caps=$(docker inspect -f '{{.HostConfig.CapDrop}}' "$CID"); [[ "$caps" == *ALL* ]] && pass "capabilities dropped" || fail "caps"
binding=$(docker port "$CID" 8765/tcp | head -1); [[ "$binding" == 127.0.0.1:* ]] && pass "published on localhost only ($binding)" || fail "binding $binding"
arch=$(docker exec "$CID" uname -m); pass "container arch $arch"

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/mcp"); [ "$code" = 401 ] && pass "MCP without token -> 401" || fail "unauth $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" -H 'Host: attacker.example' "$BASE/api/status"); [ "$code" = 421 ] && pass "foreign Host -> 421" || fail "host $code"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/admin"); [ "$code" = 303 ] && pass "admin without session -> redirect" || fail "admin $code"

rpc() { curl -fsS -H "$AUTH" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$1" "$BASE/mcp" | sed -n 's/^data: //p;/^{/p' | head -1; }
n=$(rpc '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["result"]["tools"]))')
[ "$n" = 16 ] && pass "tools/list returns 16 tools" || fail "tools $n"
accts=$(rpc '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_accounts","arguments":{}}}')
echo "$accts" | grep -q unpaired && pass "accounts report unpaired (no pairing needed to run)" || fail "accounts $accts"

upload() { curl -s -H "$AUTH" -F "file=@$1" -w '\n%{http_code}' "$BASE/api/accounts/personal/media"; }
check() { # file expected_type expected_mime
  out=$(upload "$F/$1"); code=$(echo "$out" | tail -1); body=$(echo "$out" | sed '$d')
  [ "$code" = 201 ] || fail "upload $1 -> $code $body"
  got=$(echo "$body" | python3 -c 'import json,sys;m=json.load(sys.stdin)["media"];print(m["media_type"],m["mime_type"],sep="|")')
  [ "$got" = "$2|$3" ] && pass "upload $1 detected as $got" || fail "upload $1 got $got want $2|$3"
}
check voice.ogg audio "audio/ogg; codecs=opus"
check song.mp3 audio audio/mpeg
check clip.wav audio audio/wav
check memo.m4a audio audio/mp4
check video.mp4 video video/mp4
check image.png image image/png
check photo.jpg image image/jpeg
check doc.pdf document application/pdf
for bad in broken.ogg truncated.ogg; do
  out=$(upload "$F/$bad"); code=$(echo "$out" | tail -1)
  body=$(echo "$out" | sed '$d')
  if [ "$code" = 400 ]; then pass "malformed $bad rejected (400)"
  elif echo "$body" | grep -q '"duration_ms"'; then fail "malformed $bad accepted with a duration: $body"
  else pass "malformed $bad not treated as playable audio ($code)"; fi
done
head -c $((33*1024*1024)) /dev/zero > "$F/big.bin"
code=$(upload "$F/big.bin" | tail -1); [ "$code" = 413 ] && pass "oversize upload -> 413" || fail "oversize $code"

# Download round trip with hash comparison.
id=$(upload "$F/photo.jpg" | sed '$d' | python3 -c 'import json,sys;print(json.load(sys.stdin)["media"]["media_id"])')
curl -fsS -H "$AUTH" "$BASE/api/accounts/personal/media/$id" -o "$WORK/dl.jpg"
[ "$(shasum -a 256 < "$F/photo.jpg")" = "$(shasum -a 256 < "$WORK/dl.jpg")" ] && pass "download hash matches upload" || fail "hash"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$BASE/api/accounts/second/media/$id"); [ "$code" = 404 ] && pass "other account cannot read media -> 404" || fail "cross $code"

# Voice-note conversion with the image's own FFmpeg, in the account work dir.
docker cp "$F/song.mp3" "$CID:/data/accounts/personal/media/work/in.mp3" >/dev/null 2>&1 || docker exec -i "$CID" sh -c 'cat > /data/accounts/personal/media/work/in.mp3' < "$F/song.mp3"
docker exec "$CID" ffmpeg -nostdin -hide_banner -loglevel error -i /data/accounts/personal/media/work/in.mp3 -vn -map_metadata -1 -ac 1 -ar 48000 -c:a libopus -b:a 32k -application voip -f ogg -y /data/accounts/personal/media/work/out.ogg || fail "convert"
codec=$(docker exec "$CID" ffprobe -v error -of csv=p=0 -show_entries stream=codec_name /data/accounts/personal/media/work/out.ogg)
[ "$codec" = opus ] && pass "MP3 -> Ogg/Opus conversion inside image" || fail "codec $codec"
docker exec "$CID" ffmpeg -nostdin -hide_banner -loglevel error -i /data/accounts/personal/media/work/out.ogg -ac 1 -ar 16000 -c:a pcm_s16le -f wav -y /data/accounts/personal/media/work/out.wav || fail "wav"
[ "$(docker exec "$CID" ffprobe -v error -of csv=p=0 -show_entries stream=codec_name /data/accounts/personal/media/work/out.wav)" = pcm_s16le ] && pass "Opus -> WAV conversion inside image (for chat-audio models)" || fail "wav codec"

idle=$(docker stats --no-stream --format '{{.MemUsage}} cpu={{.CPUPerc}}' "$CID"); pass "resource snapshot: $idle"

# Graceful stop, persistence and restart.
t0=$(date +%s); dc stop -t 20 >/dev/null; t1=$(date +%s)
ec=$(docker inspect -f '{{.State.ExitCode}}' "$CID"); [ "$ec" = 0 ] && pass "graceful stop in $((t1-t0))s, exit 0" || fail "exit $ec"
dc start >/dev/null
for i in $(seq 1 60); do curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS -H "$AUTH" "$BASE/api/accounts/personal/media/$id" -o /dev/null && pass "media persisted across restart" || fail "persist"
rpc '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_accounts","arguments":{}}}' | grep -q '"error"' && fail "account error after restart" || pass "no account errors after restart"
echo "ALL CHECKS PASSED for $IMAGE ${PLATFORM:-native}"
