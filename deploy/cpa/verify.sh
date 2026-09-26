#!/usr/bin/env bash
set -euo pipefail

config=/opt/cli-proxy-api/config.yaml
management_key=$(< /opt/cli-proxy-api/management.key)
client_key=$(awk -F '"' '/^  - "/ { print $2; exit }' "$config")
if [[ -z $management_key || -z $client_key ]]; then
  printf 'CPA keys are missing.\n' >&2
  exit 1
fi

management_status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $management_key" \
  http://127.0.0.1:8317/v0/management/config)
models_status=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $client_key" \
  http://127.0.0.1:8317/v1/models)
docker exec sub2api wget -q -O /dev/null \
  --header "Authorization: Bearer $client_key" \
  http://cli-proxy-api:8317/v1/models
printf 'management=%s models=%s sub2api-to-cpa=ok\n' "$management_status" "$models_status"
[[ $management_status == 200 && $models_status == 200 ]]
