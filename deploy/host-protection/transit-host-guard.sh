#!/usr/bin/env bash
set -uo pipefail

readonly GUARD_VERSION="1.0.0"
readonly DEFAULT_CONFIG_FILE="/etc/transit-host-guard.conf"
readonly INSTALL_GUARD_PATH="/usr/local/sbin/transit-host-guard"
readonly INSTALL_SERVICE_PATH="/etc/systemd/system/transit-host-guard.service"
readonly INSTALL_DOCKER_WRAPPER_PATH="/usr/local/bin/docker"
readonly INSTALL_DOC_PATH="/usr/share/doc/transit-host-guard/README.md"
readonly WRAPPER_MARKER="TRANSIT_HOST_GUARD_DOCKER_WRAPPER"

CONFIG_FILE="${TRANSIT_HOST_GUARD_CONFIG:-${DEFAULT_CONFIG_FILE}}"
PACKAGE_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MATCH_REASON=""

DOCKER_BIN="${DOCKER_BIN:-/usr/bin/docker}"
SCAN_INTERVAL_SECONDS="${SCAN_INTERVAL_SECONDS:-1}"
PROTECTION_INTERVAL_SECONDS="${PROTECTION_INTERVAL_SECONDS:-30}"
HEALTH_INTERVAL_SECONDS="${HEALTH_INTERVAL_SECONDS:-15}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-3}"
HEALTH_FAILURE_THRESHOLD="${HEALTH_FAILURE_THRESHOLD:-5}"
HEALTH_RESTART_COOLDOWN_SECONDS="${HEALTH_RESTART_COOLDOWN_SECONDS:-300}"
HEALTH_RESTART_ENABLED="${HEALTH_RESTART_ENABLED:-1}"
STOP_BUILDKIT_ON_DETECTION="${STOP_BUILDKIT_ON_DETECTION:-1}"
ENABLE_DOCKER_WRAPPER="${ENABLE_DOCKER_WRAPPER:-1}"
DRY_RUN="${DRY_RUN:-0}"
SSH_PROCESS_NAMES="${SSH_PROCESS_NAMES:-sshd}"
PROTECTED_APP_CONTAINERS="${PROTECTED_APP_CONTAINERS:-sub2api transithub}"
HEALTHCHECKS="${HEALTHCHECKS:-sub2api|http://127.0.0.1:8080/health;transithub|http://127.0.0.1:10621/api/health}"
SSH_OOM_SCORE_ADJ="${SSH_OOM_SCORE_ADJ:--900}"
APP_OOM_SCORE_ADJ="${APP_OOM_SCORE_ADJ:--800}"

declare -A RECENTLY_HANDLED=()
declare -A HEALTH_FAILURES=()
declare -A HEALTH_LAST_RESTART=()

log() {
  printf '%s transit-host-guard[%s]: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$$" "$*"
}

die() {
  log "ERROR: $*" >&2
  exit 1
}

load_config() {
  if [[ -r "${CONFIG_FILE}" ]]; then
    # The config is installed root-owned and contains shell-compatible KEY=value lines.
    # shellcheck disable=SC1090
    source "${CONFIG_FILE}"
  fi
}

command_basename() {
  local value=${1:-}
  value=${value##*/}
  printf '%s' "${value,,}"
}

is_reserved_data_container() {
  local name=${1,,}
  case "${name}" in
    *postgres*|*postgresql*|*redis*) return 0 ;;
    *) return 1 ;;
  esac
}

contains_exact_arg() {
  local expected=$1
  shift
  local arg
  for arg in "$@"; do
    [[ "${arg}" == "${expected}" ]] && return 0
  done
  return 1
}

find_docker_command_index() {
  local -a argv=("$@")
  local i token
  for ((i = 1; i < ${#argv[@]}; i++)); do
    token=${argv[i]}
    case "${token}" in
      --config|--context|--host|-H|--log-level|-l) ((i++)) ;;
      --config=*|--context=*|--host=*|-H?*|--log-level=*|-l?*) ;;
      --debug|-D|--tls|--tlsverify|--help|-h|--version|-v) ;;
      -*) ;;
      *) printf '%s' "${i}"; return 0 ;;
    esac
  done
  return 1
}

find_compose_command_index() {
  local start=$1
  shift
  local -a argv=("$@")
  local i token
  for ((i = start; i < ${#argv[@]}; i++)); do
    token=${argv[i]}
    case "${token}" in
      -f|--file|-p|--project-name|--project-directory|--env-file|--profile|--parallel|--ansi|--progress) ((i++)) ;;
      --file=*|--project-name=*|--project-directory=*|--env-file=*|--profile=*|--parallel=*|--ansi=*|--progress=*) ;;
      --compatibility|--dry-run|--help|-h|--verbose) ;;
      -*) ;;
      *) printf '%s' "${i}"; return 0 ;;
    esac
  done
  return 1
}

find_buildx_command_index() {
  local start=$1
  shift
  local -a argv=("$@")
  local i token
  for ((i = start; i < ${#argv[@]}; i++)); do
    token=${argv[i]}
    case "${token}" in
      --builder) ((i++)) ;;
      --builder=*) ;;
      --debug|--help|-h) ;;
      -*) ;;
      *) printf '%s' "${i}"; return 0 ;;
    esac
  done
  return 1
}

docker_compose_is_forbidden() {
  local start=$1
  shift
  local -a argv=("$@")
  local index command
  index=$(find_compose_command_index "${start}" "${argv[@]}") || return 1
  command=${argv[index],,}

  case "${command}" in
    build)
      MATCH_REASON="Docker Compose builds are forbidden on production hosts"
      return 0
      ;;
    up|create|run)
      if contains_exact_arg "--build" "${argv[@]}"; then
        MATCH_REASON="Docker Compose ${command} --build is forbidden on production hosts"
        return 0
      fi
      if ! contains_exact_arg "--no-build" "${argv[@]}"; then
        MATCH_REASON="Docker Compose ${command} must include --no-build on production hosts"
        return 0
      fi
      ;;
  esac
  return 1
}

is_forbidden_argv() {
  local -a argv=("$@")
  local executable index command nested_index nested_command token previous=""
  MATCH_REASON=""
  ((${#argv[@]} > 0)) || return 1
  executable=$(command_basename "${argv[0]}")

  case "${executable}" in
    docker)
      index=$(find_docker_command_index "${argv[@]}") || return 1
      command=${argv[index],,}
      case "${command}" in
        build)
          MATCH_REASON="docker build is forbidden on production hosts"
          return 0
          ;;
        buildx)
          nested_index=$(find_buildx_command_index "$((index + 1))" "${argv[@]}") || return 1
          nested_command=${argv[nested_index],,}
          if [[ "${nested_command}" == "build" || "${nested_command}" == "bake" ]]; then
            MATCH_REASON="docker buildx ${nested_command} is forbidden on production hosts"
            return 0
          fi
          ;;
        compose)
          docker_compose_is_forbidden "$((index + 1))" "${argv[@]}" && return 0
          ;;
      esac
      ;;
    docker-compose)
      docker_compose_is_forbidden 1 "${argv[@]}" && return 0
      ;;
    docker-buildx|buildctl|buildctl-daemonless.sh)
      if contains_exact_arg "build" "${argv[@]}" || contains_exact_arg "bake" "${argv[@]}" || [[ "${executable}" == buildctl* ]]; then
        MATCH_REASON="BuildKit build command is forbidden on production hosts"
        return 0
      fi
      ;;
    go)
      for token in "${argv[@]:1}"; do
        if [[ "${previous}" == "-c" ]]; then previous=""; continue; fi
        if [[ "${token,,}" == "-c" ]]; then previous="-c"; continue; fi
        [[ "${token}" == -* ]] && continue
        command=${token,,}
        case "${command}" in
          build|install|test|run|generate)
            MATCH_REASON="go ${command} is forbidden on production hosts"
            return 0
            ;;
          tool)
            previous="tool"
            continue
            ;;
          compile|link)
            if [[ "${previous}" == "tool" ]]; then
              MATCH_REASON="go tool ${command} is forbidden on production hosts"
              return 0
            fi
            ;;
        esac
        break
      done
      ;;
    npm|pnpm|yarn|bun)
      for ((index = 1; index < ${#argv[@]}; index++)); do
        token=${argv[index],,}
        case "${token}" in
          build|rebuild|install|ci)
            MATCH_REASON="${executable} ${token} is forbidden on production hosts"
            return 0
            ;;
          run|run-script)
            if ((index + 1 < ${#argv[@]})); then
              nested_command=${argv[index + 1],,}
              case "${nested_command}" in
                build|compile|bundle|package)
                  MATCH_REASON="${executable} ${token} ${nested_command} is forbidden on production hosts"
                  return 0
                  ;;
              esac
            fi
            ;;
        esac
      done
      ;;
    npx|vite|vue-tsc|webpack|rollup|esbuild|tsc)
      MATCH_REASON="frontend build tool ${executable} is forbidden on production hosts"
      return 0
      ;;
    node)
      if ((${#argv[@]} > 1)); then
        command=$(command_basename "${argv[1]}")
        case "${command}" in
          vite|vite.js|vue-tsc|vue-tsc.js|webpack|webpack.js|rollup|rollup.js|esbuild|tsc|tsc.js)
            MATCH_REASON="frontend build tool ${command} is forbidden on production hosts"
            return 0
            ;;
        esac
      fi
      ;;
    cargo|rustc)
      MATCH_REASON="Rust compilation is forbidden on production hosts"
      return 0
      ;;
    make|gmake|cmake|ninja|meson|mvn|mvnw|gradle|gradlew|ant|javac)
      MATCH_REASON="build tool ${executable} is forbidden on production hosts"
      return 0
      ;;
    cc|c++|gcc|g++|clang|clang++|ld|ld.lld|gold)
      MATCH_REASON="native compiler ${executable} is forbidden on production hosts"
      return 0
      ;;
    python|python3)
      for ((index = 1; index < ${#argv[@]}; index++)); do
        token=${argv[index],,}
        if [[ "${token}" == "-m" ]] && ((index + 1 < ${#argv[@]})); then
          nested_command=${argv[index + 1],,}
          case "${nested_command}" in
            build|pyinstaller|nuitka)
              MATCH_REASON="python -m ${nested_command} is forbidden on production hosts"
              return 0
              ;;
            pip)
              if ((index + 2 < ${#argv[@]})) && [[ "${argv[index + 2],,}" == "wheel" ]]; then
                MATCH_REASON="pip wheel is forbidden on production hosts"
                return 0
              fi
              ;;
          esac
        fi
        if [[ "${token}" == *setup.py ]] && ((index + 1 < ${#argv[@]})) && [[ "${argv[index + 1],,}" == build* ]]; then
          MATCH_REASON="setup.py build is forbidden on production hosts"
          return 0
        fi
      done
      ;;
  esac
  return 1
}

read_process_argv() {
  local pid=$1
  PROCESS_ARGV=()
  [[ -r "/proc/${pid}/cmdline" ]] || return 1
  mapfile -d '' -t PROCESS_ARGV < "/proc/${pid}/cmdline" 2>/dev/null || return 1
  ((${#PROCESS_ARGV[@]} > 0))
}

process_parent_pid() {
  local pid=$1
  awk '/^PPid:/ { print $2; exit }' "/proc/${pid}/status" 2>/dev/null
}

is_guard_ancestor() {
  local candidate=$1 current=$$
  while [[ "${current}" =~ ^[0-9]+$ ]] && ((current > 1)); do
    [[ "${current}" == "${candidate}" ]] && return 0
    current=$(process_parent_pid "${current}") || break
  done
  return 1
}

collect_descendants() {
  local root=$1
  ps -eo pid=,ppid= 2>/dev/null | awk -v root="${root}" '
    {
      parent[$1] = $2
      ids[count++] = $1
    }
    END {
      for (i = 0; i < count; i++) {
        candidate = ids[i]
        current = candidate
        depth = 0
        while ((current in parent) && depth++ < 128) {
          current = parent[current]
          if (current == root) {
            print candidate
            break
          }
        }
      }
    }
  '
}

terminate_process_tree() {
  local root=$1 reason=$2 pid
  local -a descendants=()
  [[ "${root}" =~ ^[0-9]+$ ]] || return 1
  ((root > 1)) || return 1
  [[ "${root}" != "$$" ]] || return 1
  is_guard_ancestor "${root}" && return 1

  if [[ "${DRY_RUN}" != "1" ]]; then
    if ! kill -STOP -- "${root}" 2>/dev/null; then
      [[ -d "/proc/${root}" ]] && log "failed to freeze forbidden pid=${root} before tree collection"
    fi
  fi

  mapfile -t descendants < <(collect_descendants "${root}")
  log "blocked pid=${root} reason=${reason} descendants=${#descendants[@]}"
  if [[ "${DRY_RUN}" == "1" ]]; then
    return 0
  fi

  for ((pid = ${#descendants[@]} - 1; pid >= 0; pid--)); do
    kill -TERM -- "${descendants[pid]}" 2>/dev/null || true
  done
  kill -TERM -- "${root}" 2>/dev/null || true
  sleep 0.25
  for ((pid = ${#descendants[@]} - 1; pid >= 0; pid--)); do
    [[ -d "/proc/${descendants[pid]}" ]] && kill -KILL -- "${descendants[pid]}" 2>/dev/null || true
  done
  [[ -d "/proc/${root}" ]] && kill -KILL -- "${root}" 2>/dev/null || true
}

cleanup_buildkit_containers() {
  [[ "${STOP_BUILDKIT_ON_DETECTION}" == "1" ]] || return 0
  [[ -x "${DOCKER_BIN}" ]] || return 0
  local ids id
  if command -v timeout >/dev/null 2>&1; then
    ids=$(timeout 5 "${DOCKER_BIN}" ps --filter 'name=buildx_buildkit_' --format '{{.ID}}' 2>/dev/null || true)
  else
    ids=$("${DOCKER_BIN}" ps --filter 'name=buildx_buildkit_' --format '{{.ID}}' 2>/dev/null || true)
  fi
  for id in ${ids}; do
    [[ -n "${id}" ]] || continue
    log "stopping active BuildKit container id=${id}; container is not removed"
    [[ "${DRY_RUN}" == "1" ]] && continue
    if command -v timeout >/dev/null 2>&1; then
      timeout 10 "${DOCKER_BIN}" stop --time 2 "${id}" >/dev/null 2>&1 || true
    else
      "${DOCKER_BIN}" stop --time 2 "${id}" >/dev/null 2>&1 || true
    fi
  done
}

list_suspicious_processes() {
  ps -ww -eo pid=,ppid=,comm=,args= 2>/dev/null | awk '
    $3 ~ /^(docker|docker-compose|docker-buildx|buildctl|go|node|npm|pnpm|yarn|bun|npx|vite|vue-tsc|webpack|rollup|esbuild|tsc|cargo|rustc|make|gmake|cmake|ninja|meson|mvn|mvnw|gradle|gradlew|ant|javac|cc|c\+\+|cc1|cc1plus|gcc|g\+\+|clang|clang\+\+|ld|ld\.lld|gold|python|python3)$/ {
      pid = $1
      ppid = $2
      $1 = $2 = $3 = ""
      sub(/^ +/, "")
      printf "%s\t%s\t%s\n", pid, ppid, $0
    }
  '
}

scan_forbidden_processes() {
  local now pid ppid reason command_line
  now=$(date +%s)
  while IFS=$'\t' read -r pid ppid command_line; do
    [[ "${pid}" =~ ^[0-9]+$ ]] || continue
    [[ "${pid}" == "$$" ]] && continue
    if [[ -n "${RECENTLY_HANDLED[${pid}]:-}" ]]; then
      if ((RECENTLY_HANDLED[${pid}] > now)); then continue; fi
      unset "RECENTLY_HANDLED[${pid}]"
    fi
    PROCESS_ARGV=()
    read -r -a PROCESS_ARGV <<< "${command_line}"
    ((${#PROCESS_ARGV[@]} > 0)) || continue
    if is_forbidden_argv "${PROCESS_ARGV[@]}"; then
      reason=${MATCH_REASON}
      command_line=$(command_basename "${PROCESS_ARGV[0]}")
      RECENTLY_HANDLED["${pid}"]=$((now + 10))
      log "detected forbidden command pid=${pid} command=${command_line}"
      terminate_process_tree "${pid}" "${reason}" || true
      case "${PROCESS_ARGV[0],,} ${PROCESS_ARGV[*],,}" in
        *docker*|*buildkit*|*buildctl*) cleanup_buildkit_containers ;;
      esac
    fi
  done < <(list_suspicious_processes)
}

set_oom_score_adj() {
  local pid=$1 value=$2
  [[ -w "/proc/${pid}/oom_score_adj" ]] || return 0
  printf '%s' "${value}" > "/proc/${pid}/oom_score_adj" 2>/dev/null || true
}

protect_pid_tree() {
  local root=$1 value=$2 pid
  [[ "${root}" =~ ^[0-9]+$ ]] && ((root > 1)) || return 0
  set_oom_score_adj "${root}" "${value}"
  while IFS= read -r pid; do
    [[ -n "${pid}" ]] && set_oom_score_adj "${pid}" "${value}"
  done < <(collect_descendants "${root}")
}

protect_ssh_processes() {
  local pid_path pid comm wanted
  for pid_path in /proc/[0-9]*; do
    pid=${pid_path##*/}
    [[ -r "/proc/${pid}/comm" ]] || continue
    IFS= read -r comm < "/proc/${pid}/comm" || continue
    for wanted in ${SSH_PROCESS_NAMES}; do
      if [[ "${comm}" == "${wanted}" ]]; then
        set_oom_score_adj "${pid}" "${SSH_OOM_SCORE_ADJ}"
        renice -n -10 -p "${pid}" >/dev/null 2>&1 || true
        break
      fi
    done
  done
}

docker_container_pid() {
  local container=$1 output
  [[ -x "${DOCKER_BIN}" ]] || return 1
  if command -v timeout >/dev/null 2>&1; then
    output=$(timeout 5 "${DOCKER_BIN}" inspect --format '{{if .State.Running}}{{.State.Pid}}{{end}}' "${container}" 2>/dev/null) || return 1
  else
    output=$("${DOCKER_BIN}" inspect --format '{{if .State.Running}}{{.State.Pid}}{{end}}' "${container}" 2>/dev/null) || return 1
  fi
  [[ "${output}" =~ ^[0-9]+$ ]] || return 1
  printf '%s' "${output}"
}

protect_app_containers() {
  local container pid
  for container in ${PROTECTED_APP_CONTAINERS}; do
    [[ -n "${container}" ]] || continue
    if is_reserved_data_container "${container}"; then
      log "refusing to manage reserved data container name=${container}"
      continue
    fi
    pid=$(docker_container_pid "${container}") || continue
    protect_pid_tree "${pid}" "${APP_OOM_SCORE_ADJ}"
  done
}

probe_url() {
  local url=$1
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time "${HEALTH_TIMEOUT_SECONDS}" "${url}" >/dev/null 2>&1
  elif command -v wget >/dev/null 2>&1; then
    wget -q -T "${HEALTH_TIMEOUT_SECONDS}" -O /dev/null "${url}" >/dev/null 2>&1
  else
    return 1
  fi
}

restart_application_container() {
  local container=$1
  if is_reserved_data_container "${container}"; then
    log "refusing health restart for reserved data container name=${container}"
    return 1
  fi
  [[ -x "${DOCKER_BIN}" ]] || return 1
  log "health threshold reached; restarting application container name=${container}"
  [[ "${DRY_RUN}" == "1" ]] && return 0
  if command -v timeout >/dev/null 2>&1; then
    timeout 45 "${DOCKER_BIN}" restart --time 20 "${container}" >/dev/null 2>&1
  else
    "${DOCKER_BIN}" restart --time 20 "${container}" >/dev/null 2>&1
  fi
}

check_application_health() {
  [[ "${HEALTH_RESTART_ENABLED}" == "1" ]] || return 0
  local entry container url failures now last_restart
  local old_ifs=${IFS}
  IFS=';'
  read -r -a health_entries <<< "${HEALTHCHECKS}"
  IFS=${old_ifs}
  now=$(date +%s)
  for entry in "${health_entries[@]}"; do
    [[ -n "${entry}" ]] || continue
    container=${entry%%|*}
    url=${entry#*|}
    if [[ "${container}" == "${url}" || -z "${container}" || -z "${url}" ]]; then
      log "ignoring malformed health target=${entry}"
      continue
    fi
    if is_reserved_data_container "${container}"; then
      log "refusing health configuration for reserved data container name=${container}"
      continue
    fi
    if probe_url "${url}"; then
      HEALTH_FAILURES["${container}"]=0
      continue
    fi
    failures=$(( ${HEALTH_FAILURES[${container}]:-0} + 1 ))
    HEALTH_FAILURES["${container}"]=${failures}
    log "health probe failed container=${container} failures=${failures}/${HEALTH_FAILURE_THRESHOLD} url=${url}"
    ((failures >= HEALTH_FAILURE_THRESHOLD)) || continue
    last_restart=${HEALTH_LAST_RESTART[${container}]:-0}
    if ((now - last_restart < HEALTH_RESTART_COOLDOWN_SECONDS)); then
      continue
    fi
    if restart_application_container "${container}"; then
      HEALTH_LAST_RESTART["${container}"]=${now}
      HEALTH_FAILURES["${container}"]=0
    fi
  done
}

protect_now() {
  protect_ssh_processes
  protect_app_containers
}

run_guard() {
  local now last_protection=0 last_health=0
  [[ "$(id -u)" == "0" ]] || die "run mode requires root"
  log "starting version=${GUARD_VERSION} scan_interval=${SCAN_INTERVAL_SECONDS}s dry_run=${DRY_RUN}"
  protect_now
  while true; do
    scan_forbidden_processes
    now=$(date +%s)
    if ((now - last_protection >= PROTECTION_INTERVAL_SECONDS)); then
      protect_now
      last_protection=${now}
    fi
    if ((now - last_health >= HEALTH_INTERVAL_SECONDS)); then
      check_application_health
      last_health=${now}
    fi
    sleep "${SCAN_INTERVAL_SECONDS}"
  done
}

unit_exists() {
  systemctl list-unit-files "$1" --no-legend 2>/dev/null | grep -q .
}

install_ssh_dropins() {
  local unit dir
  for unit in ssh.service sshd.service; do
    unit_exists "${unit}" || continue
    dir="/etc/systemd/system/${unit}.d"
    install -d -m 0755 "${dir}"
    cat > "${dir}/90-transit-host-guard.conf" <<'EOF'
[Service]
OOMScoreAdjust=-900
CPUWeight=10000
IOWeight=10000
EOF
  done
}

install_guard() {
  local actual_docker
  [[ "$(id -u)" == "0" ]] || die "install mode requires root"
  command -v systemctl >/dev/null 2>&1 || die "systemd is required"
  [[ -f "${PACKAGE_DIR}/transit-host-guard.service" ]] || die "missing transit-host-guard.service"
  [[ -f "${PACKAGE_DIR}/transit-host-guard.conf.example" ]] || die "missing transit-host-guard.conf.example"
  [[ -f "${PACKAGE_DIR}/docker-wrapper.sh" ]] || die "missing docker-wrapper.sh"

  actual_docker=$(command -v docker 2>/dev/null || true)
  if [[ "${actual_docker}" == "${INSTALL_DOCKER_WRAPPER_PATH}" ]] && grep -q "${WRAPPER_MARKER}" "${actual_docker}" 2>/dev/null; then
    actual_docker=$(awk -F= '/^DOCKER_BIN=/{gsub(/["'\'' ]/, "", $2); value=$2} END{print value}' "${CONFIG_FILE}" 2>/dev/null || true)
  fi
  [[ -n "${actual_docker}" ]] || actual_docker="/usr/bin/docker"

  install -m 0755 "${PACKAGE_DIR}/transit-host-guard.sh" "${INSTALL_GUARD_PATH}"
  install -m 0644 "${PACKAGE_DIR}/transit-host-guard.service" "${INSTALL_SERVICE_PATH}"
  install -d -m 0755 "$(dirname -- "${INSTALL_DOC_PATH}")"
  install -m 0644 "${PACKAGE_DIR}/README.md" "${INSTALL_DOC_PATH}"
  if [[ ! -e "${CONFIG_FILE}" ]]; then
    install -m 0644 "${PACKAGE_DIR}/transit-host-guard.conf.example" "${CONFIG_FILE}"
  elif grep -Fqx 'PROTECTION_INTERVAL_SECONDS=5' "${CONFIG_FILE}"; then
    sed -i 's/^PROTECTION_INTERVAL_SECONDS=5$/PROTECTION_INTERVAL_SECONDS=30/' "${CONFIG_FILE}"
  fi
  if ! grep -q '^DOCKER_BIN=' "${CONFIG_FILE}" 2>/dev/null; then
    printf '\nDOCKER_BIN=%q\n' "${actual_docker}" >> "${CONFIG_FILE}"
  elif [[ "${actual_docker}" != "${INSTALL_DOCKER_WRAPPER_PATH}" ]]; then
    sed -i "s|^DOCKER_BIN=.*$|DOCKER_BIN=${actual_docker}|" "${CONFIG_FILE}"
  fi

  load_config
  if [[ "${ENABLE_DOCKER_WRAPPER}" == "1" ]]; then
    if [[ ! -e "${INSTALL_DOCKER_WRAPPER_PATH}" ]] || grep -q "${WRAPPER_MARKER}" "${INSTALL_DOCKER_WRAPPER_PATH}" 2>/dev/null; then
      install -m 0755 "${PACKAGE_DIR}/docker-wrapper.sh" "${INSTALL_DOCKER_WRAPPER_PATH}"
    else
      log "existing ${INSTALL_DOCKER_WRAPPER_PATH} is not managed by this guard; wrapper installation skipped"
    fi
  fi

  install_ssh_dropins
  systemctl daemon-reload
  systemd-analyze verify "${INSTALL_SERVICE_PATH}"
  "${INSTALL_GUARD_PATH}" protect-now || true
  systemctl enable transit-host-guard.service
  systemctl restart transit-host-guard.service
  log "installed; no source code was uploaded and no build command was executed"
}

remove_ssh_dropins() {
  local unit file dir
  for unit in ssh.service sshd.service; do
    dir="/etc/systemd/system/${unit}.d"
    file="${dir}/90-transit-host-guard.conf"
    rm -f -- "${file}"
    rmdir --ignore-fail-on-non-empty "${dir}" 2>/dev/null || true
  done
}

uninstall_guard() {
  [[ "$(id -u)" == "0" ]] || die "uninstall mode requires root"
  systemctl disable --now transit-host-guard.service >/dev/null 2>&1 || true
  if [[ -f "${INSTALL_DOCKER_WRAPPER_PATH}" ]] && grep -q "${WRAPPER_MARKER}" "${INSTALL_DOCKER_WRAPPER_PATH}" 2>/dev/null; then
    rm -f -- "${INSTALL_DOCKER_WRAPPER_PATH}"
  fi
  remove_ssh_dropins
  rm -f -- "${INSTALL_SERVICE_PATH}" "${INSTALL_GUARD_PATH}" "${INSTALL_DOC_PATH}"
  rmdir --ignore-fail-on-non-empty "$(dirname -- "${INSTALL_DOC_PATH}")" 2>/dev/null || true
  if [[ "${1:-}" == "--purge" ]]; then
    rm -f -- "${CONFIG_FILE}"
  fi
  systemctl daemon-reload
  log "uninstalled; application, PostgreSQL, Redis, volumes, and data were not modified"
}

show_status() {
  printf 'transit-host-guard version: %s\n' "${GUARD_VERSION}"
  printf 'config: %s\n' "${CONFIG_FILE}"
  printf 'docker wrapper: '
  if [[ -f "${INSTALL_DOCKER_WRAPPER_PATH}" ]] && grep -q "${WRAPPER_MARKER}" "${INSTALL_DOCKER_WRAPPER_PATH}" 2>/dev/null; then
    printf 'installed\n'
  else
    printf 'not installed\n'
  fi
  if command -v systemctl >/dev/null 2>&1; then
    systemctl status transit-host-guard.service --no-pager --lines=20 || true
  fi
}

allow_command() {
  (($# > 0)) || die "allow requires a command and arguments"
  if is_forbidden_argv "$@"; then
    printf 'transit-host-guard: blocked: %s\n' "${MATCH_REASON}" >&2
    return 126
  fi
  return 0
}

simulate_command() {
  [[ "${1:-}" == "--" ]] && shift
  (($# > 0)) || die "simulate requires a command and arguments"
  if is_forbidden_argv "$@"; then
    printf 'FORBIDDEN: %s\n' "${MATCH_REASON}"
    return 0
  fi
  printf 'ALLOWED\n'
}

usage() {
  cat <<'EOF'
Usage: transit-host-guard <command>

Commands:
  install                 Install and start the systemd guard.
  uninstall [--purge]     Remove the guard; keep config unless --purge is used.
  run                     Run the continuous guard loop (systemd entrypoint).
  protect-now             Apply OOM protection once to SSH and app containers.
  status                  Show installation and service status.
  allow <command...>      Exit 126 when a command violates the build policy.
  simulate -- <command>   Print whether a command would be blocked.

This program never builds images or source code. It never restarts, recreates,
removes, or modifies PostgreSQL/Redis containers, volumes, or data directories.
EOF
}

main() {
  local action=${1:-}
  [[ -n "${action}" ]] && shift || true
  load_config
  case "${action}" in
    install) install_guard "$@" ;;
    uninstall) uninstall_guard "$@" ;;
    run) run_guard ;;
    protect-now) protect_now ;;
    status) show_status ;;
    allow) allow_command "$@" ;;
    simulate) simulate_command "$@" ;;
    -h|--help|help|'') usage ;;
    *) usage >&2; exit 2 ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
