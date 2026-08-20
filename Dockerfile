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

FROM node:22-bookworm-slim AS web-build
ENV PNPM_HOME=/pnpm
ENV PATH=$PNPM_HOME:$PATH
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11.19.0 --activate
COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store pnpm install --frozen-lockfile
COPY . ./
ARG NEXT_PUBLIC_RUNTIME_URL=http://localhost:8080
ENV NEXT_PUBLIC_RUNTIME_URL=$NEXT_PUBLIC_RUNTIME_URL
RUN pnpm build

FROM node:22-bookworm-slim AS web
ENV PNPM_HOME=/pnpm \
    PATH=/pnpm:$PATH \
    NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=3000
WORKDIR /app
RUN corepack enable && corepack prepare pnpm@11.19.0 --activate
COPY --from=web-build --chown=node:node /app /app
USER node
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3000/').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"]
CMD ["pnpm", "start"]
