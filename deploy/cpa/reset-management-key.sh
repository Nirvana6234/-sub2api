#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  printf 'Run as root.\n' >&2
  exit 1
fi

root=/opt/cli-proxy-api
config="$root/config.yaml"
next_config=$(mktemp "$root/config.yaml.XXXXXX")
trap 'rm -f "$next_config"' EXIT

management_key=$(openssl rand -hex 32)
sed -E "s|^  secret-key:.*$|  secret-key: \"$management_key\"|" \
  "$config" > "$next_config"
chmod 600 "$next_config"
mv "$next_config" "$config"
printf '%s\n' "$management_key" > "$root/management.key"
chmod 600 "$root/management.key"

docker compose -f "$root/docker-compose.yml" up -d --no-deps --no-build --force-recreate cli-proxy-api
