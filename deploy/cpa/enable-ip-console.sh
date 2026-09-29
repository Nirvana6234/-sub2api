#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID} -ne 0 ]]; then
  printf 'Run as root.\n' >&2
  exit 1
fi

public_ip=${1:?Pass the current public IPv4 address}
if [[ ! $public_ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Expected an IPv4 address.\n' >&2
  exit 1
fi

install -d -m 700 /etc/pki/cpa-ip
openssl req -x509 -newkey rsa:3072 -nodes -days 365 \
  -keyout /etc/pki/cpa-ip/key.pem \
  -out /etc/pki/cpa-ip/cert.pem \
  -subj "/CN=$public_ip" \
  -addext "subjectAltName=IP:$public_ip" 2>/dev/null
chmod 600 /etc/pki/cpa-ip/key.pem
install -m 644 /opt/cli-proxy-api/nginx-ip.conf.template /etc/nginx/conf.d/cpa-ip.conf
nginx -t
systemctl reload nginx
