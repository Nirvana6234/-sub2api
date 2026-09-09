#!/usr/bin/env bash
# =============================================================================
# sub2api 生产环境发版产物保留清理
# =============================================================================
# 高频发版（每改完一个问题就发一次）会在 /opt/sub2api/backend/bin 累积二进制、
# /opt/sub2api/backups 累积 pg_dump，2026-09-09 曾把 15G 根盘写满到 100%，
# 导致二进制上传中途报 "No space left on device"。本脚本按"只保留最近 N 份"
# 的策略自动清理，避免再靠人工发现磁盘写满才处理。
#
# 绝不删除的东西：
#   - 当前 docker-compose 挂载的生产二进制（从 compose 文件动态解析路径，
#     即使它排不进"最近 N 个"也强制保留，防止误删导致回滚失败）
#   - backups 目录里的非 .dump 文件（cleanup-manifest-*.txt、*.csv 等历史小文件，
#     体积可忽略，不是磁盘占用来源，不在本脚本清理范围内）
#   - 数据库数据、当前 compose 配置一律不碰
#
# 用法：
#   ./prod-artifact-cleanup.sh              按默认保留份数清理
#   ./prod-artifact-cleanup.sh --dry-run    只打印将删除什么，不实际删除
#   KEEP_BIN=8 KEEP_DUMP=3 ./prod-artifact-cleanup.sh
# =============================================================================
set -uo pipefail

BIN_DIR="${BIN_DIR:-/opt/sub2api/backend/bin}"
BACKUP_DIR="${BACKUP_DIR:-/opt/sub2api/backups}"
COMPOSE_FILE="${COMPOSE_FILE:-/opt/sub2api/deploy/docker-compose.local.yml}"
KEEP_BIN="${KEEP_BIN:-5}"
KEEP_DUMP="${KEEP_DUMP:-5}"
LOG_FILE="${LOG_FILE:-/var/log/sub2api-artifact-cleanup.log}"
MANIFEST="${BACKUP_DIR}/cleanup-manifest-$(date -u +%Y%m%d-%H%M%S).txt"

DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

main() {
  log "=== 开始清理 (dry-run=${DRY_RUN})，bin 保留最近 ${KEEP_BIN} 个，dump 保留最近 ${KEEP_DUMP} 份 ==="

  local before_avail after_avail
  before_avail=$(df --output=avail -m / | tail -1 | tr -d ' ')

  local current_bin=""
  if [ -f "$COMPOSE_FILE" ]; then
    current_bin=$(grep -oP '(?<=backend/bin/)[^:]+(?=:/app/sub2api)' "$COMPOSE_FILE" | head -1)
  fi
  if [ -n "$current_bin" ]; then
    log "当前生产挂载二进制: ${current_bin}（强制保留，不计入 ${KEEP_BIN} 份配额）"
  else
    log "警告：未能从 ${COMPOSE_FILE} 解析出当前挂载的二进制，仅按 mtime 保留最近 ${KEEP_BIN} 个"
  fi

  if [ -d "$BIN_DIR" ]; then
    local kept=0 name f
    while IFS= read -r f; do
      name=$(basename "$f")
      if [ "$name" = "$current_bin" ]; then
        log "  [保留-当前生产版本] ${name}"
        continue
      fi
      kept=$((kept + 1))
      if [ "$kept" -le "$KEEP_BIN" ]; then
        log "  [保留-最近${KEEP_BIN}] ${name}"
        continue
      fi
      log "  [删除] ${name} ($(du -h "$f" | cut -f1))"
      if [ "$DRY_RUN" = "0" ]; then
        echo "bin: ${name}" >>"$MANIFEST"
        rm -f -- "$f"
      fi
    done < <(find "$BIN_DIR" -maxdepth 1 -type f -printf '%T@ %p\n' 2>/dev/null | sort -rn | cut -d' ' -f2-)
  else
    log "警告：${BIN_DIR} 不存在，跳过二进制清理"
  fi

  if [ -d "$BACKUP_DIR" ]; then
    local kept=0 name f
    while IFS= read -r f; do
      kept=$((kept + 1))
      name=$(basename "$f")
      if [ "$kept" -le "$KEEP_DUMP" ]; then
        log "  [保留-最近${KEEP_DUMP}] ${name}"
        continue
      fi
      log "  [删除] ${name} ($(du -h "$f" | cut -f1))"
      if [ "$DRY_RUN" = "0" ]; then
        echo "backup: ${name}" >>"$MANIFEST"
        rm -f -- "$f"
      fi
    done < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.dump' -printf '%T@ %p\n' 2>/dev/null | sort -rn | cut -d' ' -f2-)
  else
    log "警告：${BACKUP_DIR} 不存在，跳过备份清理"
  fi

  after_avail=$(df --output=avail -m / | tail -1 | tr -d ' ')
  log "磁盘可用: ${before_avail}MB -> ${after_avail}MB"
  log "=== 清理结束 ==="
}

main "$@" 2>&1 | tee -a "$LOG_FILE"
