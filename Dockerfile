# syntax=docker/dockerfile:1.7
#
# One Dockerfile, four targets:
#   web-build  - builds the SolidJS bundle
#   builder    - compiles the Go binary with the bundle embedded
#   runtime    - minimal image used for both `serve` and `ingest`
#   dev        - hot-reload image used by compose.dev.yaml

ARG GO_VERSION=1.27
ARG NODE_VERSION=22
ARG ALPINE_VERSION=3.23
ARG YTDLP_VERSION=2026.08.19
ARG AIR_VERSION=v1.67.4
ARG GOLANGCI_LINT_VERSION=v2.13.2

# The build stages run on the build platform and produce files for the
# target one (TARGETARCH), so multi-arch images need no emulated compilers;
# only the runtime stage runs on the target platform.

# --- yt-dlp -----------------------------------------------------------------
# Static musl build so the same binary works in alpine-based runtime and dev.
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS ytdlp
ARG TARGETARCH
ARG YTDLP_VERSION
RUN apk add --no-cache curl \
    && case "${TARGETARCH}" in \
         amd64) suffix=musllinux ;; \
         arm64) suffix=musllinux_aarch64 ;; \
         *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && curl -fsSL -o /usr/local/bin/yt-dlp \
         "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp_${suffix}" \
    && chmod +x /usr/local/bin/yt-dlp

# --- frontend ---------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web-build
WORKDIR /src/web
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
ARG VERSION=dev
RUN APP_VERSION=${VERSION} pnpm build

# --- backend ----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/couchcast ./cmd/couchcast

# --- runtime ----------------------------------------------------------------
FROM alpine:${ALPINE_VERSION} AS runtime
RUN apk add --no-cache ca-certificates ffmpeg tzdata \
    && adduser -D -u 10001 couchcast \
    && mkdir -p /tmp/couchcast && chown couchcast:couchcast /tmp/couchcast
COPY --from=ytdlp /usr/local/bin/yt-dlp /usr/local/bin/yt-dlp
COPY --from=builder /out/couchcast /usr/local/bin/couchcast
USER couchcast
WORKDIR /app
ENTRYPOINT ["couchcast"]
CMD ["serve"]

# --- dev --------------------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS dev
ARG AIR_VERSION
ARG GOLANGCI_LINT_VERSION
# gcc and musl-dev let `just test` run with the race detector, as CI does.
RUN apk add --no-cache ca-certificates ffmpeg gcc git musl-dev tzdata
COPY --from=ytdlp /usr/local/bin/yt-dlp /usr/local/bin/yt-dlp
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go install github.com/air-verse/air@${AIR_VERSION} \
    && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}
WORKDIR /src
CMD ["air", "-c", ".air.web.toml"]
