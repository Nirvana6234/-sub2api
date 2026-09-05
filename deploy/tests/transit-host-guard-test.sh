#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
guard="${repo_root}/deploy/host-protection/transit-host-guard.sh"
service="${repo_root}/deploy/host-protection/transit-host-guard.service"
wrapper="${repo_root}/deploy/host-protection/docker-wrapper.sh"

# shellcheck disable=SC1090
source "${guard}"

fail() {
  printf 'transit host guard test failed: %s\n' "$1" >&2
  exit 1
}

assert_forbidden() {
  is_forbidden_argv "$@" || fail "expected forbidden: $*"
}

assert_allowed() {
  if is_forbidden_argv "$@"; then
    fail "expected allowed: $* (${MATCH_REASON})"
  fi
}

assert_forbidden docker build -t test .
assert_forbidden docker buildx build --platform linux/amd64 .
assert_forbidden docker buildx --builder production build .
assert_forbidden docker compose build sub2api
assert_forbidden docker compose -f docker-compose.local.yml up -d sub2api
assert_forbidden docker compose -f docker-compose.local.yml up -d --build sub2api
assert_forbidden docker compose run sub2api migrate
assert_forbidden docker-compose up -d sub2api
assert_forbidden go test ./...
assert_forbidden npm run build
assert_forbidden pnpm install
assert_forbidden gcc main.c

assert_allowed docker load -i sub2api.tar
assert_allowed docker image inspect sub2api:release
assert_allowed docker compose -f docker-compose.local.yml up -d --no-build sub2api
assert_allowed docker compose run --no-build sub2api migrate
assert_allowed docker compose -f docker-compose.local.yml ps
assert_allowed go version
assert_allowed npm --version

is_reserved_data_container sub2api-postgres || fail 'PostgreSQL name must be reserved'
is_reserved_data_container sub2api-redis || fail 'Redis name must be reserved'
if is_reserved_data_container sub2api; then fail 'application name must not be reserved'; fi

grep -Fq 'OOMScoreAdjust=-1000' "${service}" || fail 'guard OOM protection is missing'
grep -Fq 'CPUQuota=10%' "${service}" || fail 'guard CPU cap is missing'
grep -Fq 'MemoryMax=64M' "${service}" || fail 'guard memory cap is missing'
grep -Fq 'transithub|http://127.0.0.1:10621/api/health' "${repo_root}/deploy/host-protection/transit-host-guard.conf.example" || fail 'TransitHub health target is missing'
grep -Fq 'TRANSIT_HOST_GUARD_DOCKER_WRAPPER' "${wrapper}" || fail 'wrapper marker is missing'

if grep -Eq 'docker (rm|system prune|volume rm)|compose (down|rm)' "${guard}"; then
  fail 'guard contains a destructive Docker command'
fi

printf 'transit host guard test passed\n'
