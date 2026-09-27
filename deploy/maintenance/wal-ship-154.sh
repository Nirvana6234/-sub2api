#!/usr/bin/env bash
# =============================================================================
# sub2api 生产库 → 154.9.26.202 只读备库：每小时 WAL 增量推送
# =============================================================================
# 原理（物理复制 / WAL 日志传送）：
#   生产 Postgres 开了 archive_mode，每写满一个 16MB WAL 段就由 archive_command
#   复制到 /opt/sub2api/deploy/wal_archive。本脚本每小时：
#     1. pg_switch_wal() 把当前未写满的段也切出来，保证"这一小时的改动"全部落到归档；
#     2. 等归档进程把 .ready 段都复制完；
#     3. rsync 推到 154 的 /www/sub2api-replica/wal_incoming（传输压缩，成功后删本地）。
#   154 上的 sub2api-replica 容器处于持续恢复模式，restore_command 从 wal_incoming
#   取段回放，archive_cleanup_command 回收已用过的段。表结构迁移同样随 WAL 同步。
#   备库只读，永远不会反向写回生产。
#
# SSH 密钥 /root/.ssh/wal_ship_154 在 154 上被限制为
#   restrict,command="/usr/local/bin/rrsync -wo /www/sub2api-replica/wal_incoming"
# 即只能往那一个目录写文件，拿不到 shell。所以远端路径写 "/" 就是 wal_incoming。
#
# 磁盘保护：154 长时间不可达时本地归档会堆积。超过 MAX_LOCAL_MB 时删除最旧的段
# （保生产磁盘优先），同时写 RESEED_MARKER，表示备库断链、需要重新做基础备份。
#
# 用法：
#   ./wal-ship-154.sh              正常推送
#   ./wal-ship-154.sh --no-switch  不切 WAL，只推已归档的段
# =============================================================================
set -uo pipefail

ARCHIVE_DIR="${ARCHIVE_DIR:-/opt/sub2api/deploy/wal_archive}"
PG_CONTAINER="${PG_CONTAINER:-sub2api-postgres}"
PG_USER="${PG_USER:-sub2api}"
REMOTE="${REMOTE:-root@154.9.26.202}"
SSH_KEY="${SSH_KEY:-/root/.ssh/wal_ship_154}"
MAX_LOCAL_MB="${MAX_LOCAL_MB:-3072}"
ARCHIVE_WAIT_SECONDS="${ARCHIVE_WAIT_SECONDS:-120}"
LOG_FILE="${LOG_FILE:-/var/log/sub2api-wal-ship.log}"
RESEED_MARKER="${RESEED_MARKER:-${ARCHIVE_DIR}/../WAL_REPLICA_NEEDS_RESEED}"
LOCK_FILE="${LOCK_FILE:-/run/sub2api-wal-ship.lock}"

SWITCH=1
[ "${1:-}" = "--no-switch" ] && SWITCH=0

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

psql_at() {
  docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d postgres -X -At -v ON_ERROR_STOP=1 -c "$1"
}

local_mb() { du -sm "$ARCHIVE_DIR" 2>/dev/null | cut -f1; }

main() {
  exec 9>"$LOCK_FILE"
  if ! flock -n 9; then
    log "上一次推送仍在进行，跳过本次"
    return 0
  fi

  if [ "$SWITCH" = "1" ]; then
    local lsn
    if lsn=$(psql_at "select pg_switch_wal()" 2>&1); then
      log "pg_switch_wal -> ${lsn}"
    else
      log "警告：pg_switch_wal 失败（${lsn}），继续推送已归档的段"
    fi
    local waited=0 ready
    while [ "$waited" -lt "$ARCHIVE_WAIT_SECONDS" ]; do
      ready=$(psql_at "select count(*) from pg_ls_archive_statusdir() where name like '%.ready'" 2>/dev/null || echo "?")
      [ "$ready" = "0" ] && break
      sleep 2; waited=$((waited + 2))
    done
    [ "$ready" = "0" ] || log "警告：等待 ${ARCHIVE_WAIT_SECONDS}s 后仍有 ${ready} 个段未归档，本次先推已完成的"
  fi

  local count
  count=$(find "$ARCHIVE_DIR" -maxdepth 1 -type f ! -name '*.tmp' | wc -l)
  if [ "$count" = "0" ]; then
    log "没有待推送的段"
    return 0
  fi

  log "开始推送 ${count} 个文件（本地 $(local_mb)MB）"
  local out rc sent
  out=$(rsync -a --numeric-ids --compress --remove-source-files --exclude='*.tmp' --stats \
      -e "ssh -i ${SSH_KEY} -o BatchMode=yes -o ConnectTimeout=20 -o ServerAliveInterval=30" \
      "${ARCHIVE_DIR}/" "${REMOTE}:/" 2>&1)
  rc=$?
  if [ "$rc" = "0" ]; then
    # "Total bytes sent" 是压缩后实际走网络的出站字节数（含 rsync 协议开销）
    sent=$(awk -F': ' '/^Total bytes sent/ {gsub(/,/, "", $2); print $2}' <<<"$out")
    log "推送成功，实际发送 $(( ${sent:-0} / 1024 ))KB，本地剩余 $(find "$ARCHIVE_DIR" -maxdepth 1 -type f | wc -l) 个文件"
    return 0
  fi

  log "错误：推送失败（rsync 退出码 ${rc}），本地积压 $(local_mb)MB：$(tail -3 <<<"$out" | tr '\n' ' ')"
  if [ "$(local_mb)" -gt "$MAX_LOCAL_MB" ]; then
    log "严重：本地积压超过 ${MAX_LOCAL_MB}MB，删除最旧的段以保护生产磁盘；备库已断链，需要重新做基础备份"
    date -u '+%Y-%m-%dT%H:%M:%SZ' >"$RESEED_MARKER"
    local f
    while [ "$(local_mb)" -gt "$MAX_LOCAL_MB" ]; do
      f=$(find "$ARCHIVE_DIR" -maxdepth 1 -type f -printf '%f\n' | sort | head -1)
      [ -n "$f" ] || break
      log "  [丢弃] ${f}"
      rm -f -- "${ARCHIVE_DIR}/${f}"
    done
  fi
  return 1
}

main "$@" 2>&1 | tee -a "$LOG_FILE"
exit "${PIPESTATUS[0]}"
