# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS runtime-build
WORKDIR /src/runtime
COPY runtime/go.mod runtime/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY runtime/ ./
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/quanyejun-runtime ./cmd/server

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 quanyejun \
    && adduser -S -D -H -u 10001 -G quanyejun quanyejun \
    && install -d -o quanyejun -g quanyejun \
       /var/lib/quanyejun/home \
       /var/lib/quanyejun/workspace \
       /var/cache/quanyejun \
    && install -d -o root -g quanyejun -m 0750 /etc/quanyejun
WORKDIR /app
COPY --from=runtime-build /out/quanyejun-runtime /usr/local/bin/quanyejun-runtime
COPY --chown=root:quanyejun --chmod=0640 config/mcp-servers.example.json /etc/quanyejun/mcp-servers.json
COPY --chown=root:quanyejun --chmod=0640 config/mcp-authority.example.json /etc/quanyejun/mcp-authority.json
COPY --chown=quanyejun:quanyejun skills/ /opt/quanyejun/skills/
ENV PORT=8080 \
    GIN_MODE=release \
    QUANYEJUN_ENVIRONMENT_KIND=container \
    QUANYEJUN_ENVIRONMENT_PROFILE=server \
    QUANYEJUN_RUNTIME_ROOT=/var/lib/quanyejun \
    QUANYEJUN_HOME=/var/lib/quanyejun/home \
    QUANYEJUN_WORKSPACE=/var/lib/quanyejun/workspace \
    QUANYEJUN_CONFIG_DIR=/etc/quanyejun \
    QUANYEJUN_CACHE_DIR=/var/cache/quanyejun \
    QUANYEJUN_SKILLS_DIR=/opt/quanyejun/skills \
    QUANYEJUN_MCP_SERVERS_FILE=/etc/quanyejun/mcp-servers.json \
    QUANYEJUN_MCP_AUTHORITY_FILE=/etc/quanyejun/mcp-authority.json
USER quanyejun
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/quanyejun-runtime"]
