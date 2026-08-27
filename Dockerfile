# ── build ───────────────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Dependencies first, so a source-only change does not re-download the module
# graph on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/zlrrr/multi-agent-system-turbo/internal/version.Version=${VERSION} \
        -X github.com/zlrrr/multi-agent-system-turbo/internal/version.Commit=${COMMIT} \
        -X github.com/zlrrr/multi-agent-system-turbo/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/mas ./cmd/mas

# ── runtime ─────────────────────────────────────────────────────────────────
# Alpine rather than scratch: source acquisition needs a git client, and an
# operator debugging a diagnosis needs a shell. CA certificates are required to
# reach HTTPS telemetry endpoints and model providers.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates git tzdata \
 && addgroup -g 65532 -S mas \
 && adduser -u 65532 -S -G mas -H -s /sbin/nologin mas \
 && mkdir -p /var/lib/mas/runs /var/lib/mas/src /etc/mas \
 && chown -R 65532:65532 /var/lib/mas

COPY --from=build /out/mas /usr/local/bin/mas

# Non-root by default: this tool never needs privilege, and running it as root
# would contradict everything it claims about restraint.
USER 65532:65532

ENV MAS_STORE_DIR=/var/lib/mas/runs \
    MAS_SOURCE_CACHE_DIR=/var/lib/mas/src \
    MAS_LOG_FORMAT=json

WORKDIR /var/lib/mas
VOLUME ["/var/lib/mas"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/mas", "version"]

ENTRYPOINT ["/usr/local/bin/mas"]
CMD ["--help"]

LABEL org.opencontainers.image.title="MAS-Turbo" \
      org.opencontainers.image.description="Read-only diagnostic multi-agent system for open-source middleware" \
      org.opencontainers.image.source="https://github.com/zlrrr/multi-agent-system-turbo" \
      org.opencontainers.image.licenses="Apache-2.0"
