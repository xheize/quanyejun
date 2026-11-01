#!/bin/sh
set -eu

# Read event data at runtime, never interpolate it into shell source.
test "${CI_PIPELINE_EVENT:-}" = tag
printf '%s\n' "${CI_COMMIT_TAG:-}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$'
test "${#CI_COMMIT_TAG}" -le 128
: "${BUILDKIT_HOST:?BuildKit address is required}"
: "${DOCKER_CONFIG:?Registry credentials directory is required}"
: "${NEXT_PUBLIC_RUNTIME_URL:?Public runtime URL is required}"
test -s "${DOCKER_CONFIG}/config.json"
case "$NEXT_PUBLIC_RUNTIME_URL" in
  https://?*) ;;
  *) echo 'NEXT_PUBLIC_RUNTIME_URL must be an HTTPS URL' >&2; exit 1 ;;
esac

buildctl --addr "$BUILDKIT_HOST" build \
  --frontend=dockerfile.v0 \
  --opt platform=linux/amd64 \
  --opt filename=runtime/Dockerfile \
  --local context=. \
  --local dockerfile=. \
  --output "type=image,name=registry.xheize.cc/quanyejun-backend:${CI_COMMIT_TAG},push=true"

buildctl --addr "$BUILDKIT_HOST" build \
  --frontend=dockerfile.v0 \
  --opt platform=linux/amd64 \
  --opt "build-arg:NEXT_PUBLIC_RUNTIME_URL=${NEXT_PUBLIC_RUNTIME_URL}" \
  --local context=frontend \
  --local dockerfile=frontend \
  --output "type=image,name=registry.xheize.cc/quanyejun-frontend:${CI_COMMIT_TAG},push=true"
