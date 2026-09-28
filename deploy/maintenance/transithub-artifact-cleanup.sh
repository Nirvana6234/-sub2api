#!/usr/bin/env bash
# =============================================================================
# TransitHub 生产环境发版产物保留清理
# =============================================================================
# TransitHub 每次发版会新建 /opt/transithub-releases/<版本>/（二进制 + public/），
# 并留下 pg_dump 和 docker-compose.yml.bak-*。这些不会自动回收，2026-09-26 人工
# 清理前已累积 70+ 个 compose 备份、十几份 dump。本脚本与 sub2api 的
# prod-artifact-cleanup.sh 同一思路：按"只保留最近 N 份"自动清理。
#
# 绝不删除的东西：
#   - 当前 compose 引用的任何 release 目录（从 compose 文件动态解析，全部强制保留，
#     且不计入 KEEP_RELEASE 配额；解析不到时整段跳过 release 清理，宁可不删）
#   - 当前的 docker-compose.yml 和 .env 本身
#   - 数据库数据、上传文件卷、detector-adapters 一律不碰
#
# 清理范围：
#   - /opt/transithub-releases/<版本>/       保留最近 KEEP_RELEASE 个（+ 当前引用的）
#   - 两个 backups 目录下的 *.dump / *.sql.gz  合并按时间保留最近 KEEP_DUMP 份
#   - 两个 backups 目录下的子目录（发版前快照） 超过 KEEP_SNAPSHOT_DAYS 天的删除
#   - /opt/transit-hub/docker-compose.yml.*   保留最近 KEEP_COMPOSE 份
#   - /opt/transit-hub/.env.*（不含 .env）     保留最近 KEEP_ENV 份
#
# 用法：
#   ./transithub-artifact-cleanup.sh              按默认保留份数清理
#   ./transithub-artifact-cleanup.sh --dry-run    只打印将删除什么，不实际删除
#   KEEP_RELEASE=5 KEEP_DUMP=3 ./transithub-artifact-cleanup.sh
# =============================================================================
set -uo pipefail

APP_DIR="${APP_DIR:-/opt/transit-hub}"
RELEASE_DIR="${RELEASE_DIR:-/opt/transithub-releases}"
COMPOSE_FILE="${COMPOSE_FILE:-${APP_DIR}/docker-compose.yml}"
BACKUP_DIRS="${BACKUP_DIRS:-${RELEASE_DIR}/backups ${APP_DIR}/backups}"
KEEP_RELEASE="${KEEP_RELEASE:-3}"
KEEP_DUMP="${KEEP_DUMP:-5}"
KEEP_SNAPSHOT_DAYS="${KEEP_SNAPSHOT_DAYS:-30}"
KEEP_COMPOSE="${KEEP_COMPOSE:-10}"
KEEP_ENV="${KEEP_ENV:-3}"
LOG_FILE="${LOG_FILE:-/var/log/transithub-artifact-cleanup.log}"

DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*"; }

remove() {
  local path="$1"
  log "  [删除] ${path} ($(du -sh -- "$path" 2>/dev/null | cut -f1))"
  [ "$DRY_RUN" = "0" ] && rm -rf -- "$path"
}

# 从 stdin 读取"mtime 路径"列表（新到旧），保留前 $1 个，其余删除
keep_newest() {
  local keep="$1" label="$2" kept=0 f
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    kept=$((kept + 1))
    if [ "$kept" -le "$keep" ]; then
      log "  [保留-最近${keep}] ${f}"
    else
      remove "$f"
    fi
  done < <(sort -rn | cut -d' ' -f2-)
  [ "$kept" = "0" ] && log "  （${label}：无文件）"
}

main() {
  log "=== 开始清理 (dry-run=${DRY_RUN})，release 保留 ${KEEP_RELEASE}，dump 保留 ${KEEP_DUMP}，compose 备份保留 ${KEEP_COMPOSE}，.env 备份保留 ${KEEP_ENV}，快照目录保留 ${KEEP_SNAPSHOT_DAYS} 天 ==="

  local before_avail after_avail
  before_avail=$(df --output=avail -m / | tail -1 | tr -d ' ')

  # --- 1. release 目录 ---
  log "[release] ${RELEASE_DIR}"
  local referenced=""
  if [ -f "$COMPOSE_FILE" ]; then
    referenced=$(grep -oP "(?<=${RELEASE_DIR}/)[^/:\s]+" "$COMPOSE_FILE" | sort -u)
  fi
  if [ -z "$referenced" ]; then
    log "警告：未能从 ${COMPOSE_FILE} 解析出当前引用的 release，跳过 release 清理"
  elif [ -d "$RELEASE_DIR" ]; then
    local r
    for r in $referenced; do log "  [保留-当前 compose 引用] ${r}"; done
    find "$RELEASE_DIR" -mindepth 1 -maxdepth 1 -type d ! -name backups -printf '%T@ %p\n' 2>/dev/null |
      while IFS= read -r line; do
        grep -qxF "$(basename "${line#* }")" <<<"$referenced" || echo "$line"
      done | keep_newest "$KEEP_RELEASE" "release"
  fi

  # --- 2. 数据库 dump（两个 backups 目录合并排序） ---
  log "[dump] ${BACKUP_DIRS}"
  # shellcheck disable=SC2086
  find $BACKUP_DIRS -maxdepth 1 -type f \( -name '*.dump' -o -name '*.sql.gz' \) -printf '%T@ %p\n' 2>/dev/null |
    keep_newest "$KEEP_DUMP" "dump"

  # --- 3. 发版前快照子目录，按天数 ---
  log "[snapshot] 超过 ${KEEP_SNAPSHOT_DAYS} 天的快照目录"
  local d
  # shellcheck disable=SC2086
  while IFS= read -r d; do
    [ -n "$d" ] && remove "$d"
  done < <(find $BACKUP_DIRS -mindepth 1 -maxdepth 1 -type d -mtime +"$KEEP_SNAPSHOT_DAYS" 2>/dev/null)

  # --- 4. compose 备份 ---
  log "[compose] ${APP_DIR}/docker-compose.yml.*"
  find "$APP_DIR" -maxdepth 1 -type f -name 'docker-compose.yml.?*' -printf '%T@ %p\n' 2>/dev/null |
    keep_newest "$KEEP_COMPOSE" "compose 备份"

  # --- 5. .env 备份 ---
  log "[env] ${APP_DIR}/.env.*"
  find "$APP_DIR" -maxdepth 1 -type f -name '.env.?*' -printf '%T@ %p\n' 2>/dev/null |
    keep_newest "$KEEP_ENV" ".env 备份"

  after_avail=$(df --output=avail -m / | tail -1 | tr -d ' ')
  log "磁盘可用: ${before_avail}MB -> ${after_avail}MB"
  log "=== 清理结束 ==="
}

main "$@" 2>&1 | tee -a "$LOG_FILE"
