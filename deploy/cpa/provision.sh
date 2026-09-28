#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  printf 'Run as root.\n' >&2
  exit 1
fi

stage=/tmp/cpa-stage
root=/opt/cli-proxy-api
if [[ -e "$root/config.yaml" ]]; then
  printf 'CPA is already provisioned at %s.\n' "$root" >&2
  exit 1
fi

install -d -m 700 "$root" "$root/auths" "$root/logs" "$root/plugins"
install -m 644 "$stage/docker-compose.yml" "$root/docker-compose.yml"
install -m 700 "$stage/enable-ip-console.sh" "$root/enable-ip-console.sh"
install -m 700 "$stage/verify.sh" "$root/verify.sh"
install -m 700 "$stage/verify.py" "$root/verify.py"
install -m 700 "$stage/reset-management-key.sh" "$root/reset-management-key.sh"
install -m 644 "$stage/nginx-ip.conf.template" "$root/nginx-ip.conf.template"

management_key=$(openssl rand -hex 32)
client_key=$(openssl rand -hex 32)
printf '%s\n' "$management_key" > "$root/management.key"
chmod 600 "$root/management.key"
sed \
  -e "s/__CPA_MANAGEMENT_KEY__/$management_key/" \
  -e "s/__CPA_CLIENT_KEY__/$client_key/" \
  "$stage/config.template.yaml" > "$root/config.yaml"
chmod 600 "$root/config.yaml"

docker compose -f "$root/docker-compose.yml" config --quiet
docker compose -f "$root/docker-compose.yml" up -d --no-build
