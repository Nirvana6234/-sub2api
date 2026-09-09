#!/usr/bin/env bash
# 自愈式 hosts 钉住看门狗：验证当前钉住的上游 IP 是否可用；
# 不可用则用宿主机真实 DNS 解析出新 IP 并验证后重新钉住；
# 新 IP 也不可用才彻底移除钉住记录，回退到容器内置 DNS 正常解析域名。
set -euo pipefail

DOMAIN="www.mcgrox.top"
CONTAINER="sub2api"
LOG="/opt/sub2api/logs/mcgrox-host-pin-watchdog.log"
TIMEOUT=5

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >> "$LOG"
}

# 测试指定 IP 用正确 SNI/Host 访问域名是否可用
probe_ip() {
    local ip="$1"
    local code
    code=$(curl -s -o /dev/null -w '%{http_code}' \
        --resolve "${DOMAIN}:443:${ip}" \
        --max-time "$TIMEOUT" \
        "https://${DOMAIN}/" 2>/dev/null || echo "000")
    [[ "$code" != "000" ]]
}

current_ip=$(docker exec "$CONTAINER" sh -c "grep -w '${DOMAIN}' /etc/hosts 2>/dev/null | awk '{print \$1}'" || true)

if [[ -n "$current_ip" ]] && probe_ip "$current_ip"; then
    exit 0
fi

if [[ -n "$current_ip" ]]; then
    log "当前钉住 IP ${current_ip} 探测失败，尝试重新解析"
else
    log "尚未钉住任何 IP，尝试建立"
fi

# 在宿主机上做真实 DNS 解析（容器内的 hosts 覆盖不影响宿主机）
candidate_ip=$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u | head -n1 || true)

if [[ -n "$candidate_ip" ]] && [[ "$candidate_ip" != "$current_ip" || -z "$current_ip" ]] && probe_ip "$candidate_ip"; then
    docker exec "$CONTAINER" sh -c "sed -i '/[[:space:]]${DOMAIN}\$/d' /etc/hosts; echo '${candidate_ip} ${DOMAIN}' >> /etc/hosts"
    log "已重新钉住到新 IP ${candidate_ip}"
    exit 0
fi

if [[ -n "$current_ip" ]]; then
    docker exec "$CONTAINER" sh -c "sed -i '/[[:space:]]${DOMAIN}\$/d' /etc/hosts"
    log "新 IP 也不可用，已移除钉住记录，回退到域名正常解析"
fi
