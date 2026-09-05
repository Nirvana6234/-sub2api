#!/usr/bin/env bash
# TRANSIT_HOST_GUARD_DOCKER_WRAPPER
set -uo pipefail

guard_bin=${TRANSIT_HOST_GUARD_BIN:-/usr/local/sbin/transit-host-guard}
config_file=${TRANSIT_HOST_GUARD_CONFIG:-/etc/transit-host-guard.conf}
DOCKER_BIN=${DOCKER_BIN:-/usr/bin/docker}

if [[ -r "${config_file}" ]]; then
  # shellcheck disable=SC1090
  source "${config_file}"
fi

if [[ ! -x "${guard_bin}" ]]; then
  printf 'docker blocked: transit-host-guard is missing or not executable\n' >&2
  exit 126
fi
if [[ "${DOCKER_BIN}" == "$0" || ! -x "${DOCKER_BIN}" ]]; then
  printf 'docker blocked: invalid real Docker binary: %s\n' "${DOCKER_BIN}" >&2
  exit 126
fi

"${guard_bin}" allow docker "$@"
result=$?
((result == 0)) || exit "${result}"
exec "${DOCKER_BIN}" "$@"
