# syntax=docker/dockerfile:1.7
# One multi-platform image (linux/amd64, linux/arm64). The Go stage runs on
# the build machine and cross-compiles with the target's C toolchain, which
# the CGO SQLite driver needs; GOARCH alone is not enough.

ARG GO_IMAGE=golang:1.26.10-trixie@sha256:e18951cff6896c16909458a8e0cbd52364a347826ff1f5b86d095fe0670d52e7
ARG RUNTIME_IMAGE=debian:trixie-slim@sha256:62bd423aeee912639ca8ee681c2b762cb3e25137e0a92b20be455898a9f034ad

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETARCH
ARG BUILDARCH
ARG VERSION=dev
RUN set -eux; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in \
        arm64) pkgs="gcc-aarch64-linux-gnu libc6-dev-arm64-cross" ;; \
        amd64) pkgs="gcc-x86-64-linux-gnu libc6-dev-amd64-cross" ;; \
        *) echo "unsupported TARGETARCH $TARGETARCH" >&2; exit 1 ;; \
      esac; \
      apt-get update; apt-get install -y --no-install-recommends $pkgs; rm -rf /var/lib/apt/lists/*; \
    fi
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in arm64) export CC=aarch64-linux-gnu-gcc ;; amd64) export CC=x86_64-linux-gnu-gcc ;; esac; \
    fi; \
    CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH GOTOOLCHAIN=local \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.Version=${VERSION}" -o /out/whatsapp-mcp ./cmd/whatsapp-mcp

# Minimal FFmpeg: only the containers/codecs this service probes or converts.
# Debian's ffmpeg package pulls ~450 MB of unrelated libraries (LLVM, Mesa
# GPU drivers, speech synthesis). Built without --enable-gpl, so it is
# LGPL-2.1-or-later; libopus is BSD-3-Clause.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS ffmpeg
ARG TARGETARCH
ARG BUILDARCH
ARG FFMPEG_VERSION=8.1.3
ARG FFMPEG_SHA256=7138d28c96d9d3e3af4ee3d8cad72741f8ffb40da90c1112235dea3ecd3178a3
RUN set -eux; \
    dpkg --add-architecture "$TARGETARCH"; apt-get update; \
    pkgs="xz-utils pkg-config libopus-dev:$TARGETARCH"; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then \
      case "$TARGETARCH" in arm64) pkgs="$pkgs gcc-aarch64-linux-gnu libc6-dev-arm64-cross" ;; amd64) pkgs="$pkgs gcc-x86-64-linux-gnu libc6-dev-amd64-cross" ;; esac; \
    fi; \
    apt-get install -y --no-install-recommends $pkgs; rm -rf /var/lib/apt/lists/*
WORKDIR /ff
RUN set -eux; \
    curl -fsSLo ff.tar.xz "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"; \
    echo "${FFMPEG_SHA256}  ff.tar.xz" | sha256sum -c -; \
    tar -xJf ff.tar.xz --strip-components=1; rm ff.tar.xz; \
    case "$TARGETARCH" in arm64) triple=aarch64-linux-gnu; arch=aarch64 ;; amd64) triple=x86_64-linux-gnu; arch=x86_64 ;; esac; \
    cross=""; \
    if [ "$TARGETARCH" != "$BUILDARCH" ]; then cross="--enable-cross-compile --cross-prefix=${triple}- --arch=${arch} --target-os=linux"; fi; \
    PKG_CONFIG_LIBDIR="/usr/lib/${triple}/pkgconfig:/usr/share/pkgconfig" ./configure $cross \
      --prefix=/opt/ffmpeg --pkg-config=pkg-config \
      --disable-everything --disable-autodetect --disable-doc --disable-debug --disable-network \
      --disable-ffplay --disable-avdevice --disable-swscale --disable-x86asm --enable-small \
      --enable-libopus --enable-swresample \
      --enable-protocol=file,pipe \
      --enable-demuxer=ogg,mov,mp3,wav,matroska,flac,aac,image_webp_pipe,image_png_pipe,image_jpeg_pipe,gif \
      --enable-parser=opus,vorbis,mpegaudio,aac,flac,h264,png,mjpeg,webp,gif \
      --enable-decoder=opus,vorbis,mp3float,mp3,aac,flac,alac,pcm_s16le,pcm_s24le,pcm_s32le,pcm_f32le,pcm_u8 \
      --enable-encoder=libopus,pcm_s16le --enable-muxer=ogg,wav \
      --enable-filter=abuffer,abuffersink,aresample,aformat,anull,atrim; \
    make -j"$(nproc)"; make install; \
    install -D -m 0644 COPYING.LGPLv2.1 /opt/ffmpeg/share/licenses/COPYING.LGPLv2.1

FROM ${RUNTIME_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="whatsapp-mcp" \
      org.opencontainers.image.description="Self-hosted MCP server for personal WhatsApp accounts (unofficial; not affiliated with Meta)" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="GPL-3.0-or-later AND MIT AND MPL-2.0 AND LGPL-2.1-or-later"
# ca-certificates for HTTPS to WhatsApp and Gemini; libopus for the minimal
# FFmpeg. No language runtimes and no GPU libraries.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates libopus0; \
    rm -rf /var/lib/apt/lists/* /usr/share/man/* /usr/share/info/*; \
    find /usr/share/doc -type f ! -name copyright -delete; \
    groupadd --system --gid 10001 app; \
    useradd --system --uid 10001 --gid app --home-dir /data --no-create-home --shell /usr/sbin/nologin app; \
    install -d -o 10001 -g 10001 -m 0700 /data; \
    install -d -m 0755 /config
COPY --from=ffmpeg /opt/ffmpeg/bin/ffmpeg /opt/ffmpeg/bin/ffprobe /usr/local/bin/
COPY --from=ffmpeg /opt/ffmpeg/share/licenses/COPYING.LGPLv2.1 /usr/share/licenses/ffmpeg/
COPY --from=build /out/whatsapp-mcp /usr/local/bin/whatsapp-mcp
COPY LICENSE NOTICE LICENSE-UPSTREAM-MIT THIRD_PARTY_NOTICES.md THIRD_PARTY_LICENSES.txt UPSTREAM.md /usr/share/licenses/whatsapp-mcp/
USER 10001:10001
ENV HOME=/tmp WHATSAPP_MCP_CONFIG=/config/config.yaml
VOLUME ["/data"]
EXPOSE 8765
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/usr/local/bin/whatsapp-mcp", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/whatsapp-mcp"]
CMD ["serve"]
