#!/bin/bash
#
# 共飞-ChatGPT助手 —— macOS 卸载脚本
#
#   bash uninstall-mac.sh            删除应用与开机自启，保留本地数据
#   bash uninstall-mac.sh --purge    连同本地数据一起删除
#
# 为什么默认不删数据
# ------------------
# ~/Library/Application Support/LanAi.RelayClient 里存着 codex-snapshot ——
# 用户「原本自己的」Codex 配置备份，也就是他停用共飞后回到自己 ChatGPT 账号所依赖的副本。
# 正常退出时客户端会自动还原它，但如果上一次是崩溃或强杀，这份备份就是唯一的退路。
# 卸载脚本把它删掉，等于在用户最需要它的那一刻拿走它，而且没有任何提示。
# 所以要删得显式说 --purge。

set -euo pipefail

APP_NAME="共飞-ChatGPT助手"
APP_PATH="/Applications/${APP_NAME}.app"
BUNDLE_ID="com.gongfeiai.chatgpt-assistant"
LAUNCH_AGENT="${HOME}/Library/LaunchAgents/${BUNDLE_ID}.plist"
DATA_DIR="${HOME}/Library/Application Support/LanAi.RelayClient"

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

say() { printf '%s\n' "$*"; }

# macOS 的 pgrep -x 比对的是进程记账名，长度有历史限制，而可执行文件叫
# LanAi.RelayClient.App（21 字符）。改用 -f 比对完整命令行里的应用路径：
# 既避开长度问题，也不会误伤同名的其它进程。
client_running() {
    pgrep -f "${APP_PATH}/Contents/MacOS/" >/dev/null 2>&1
}


# ---------------------------------------------------------------------------
# 1. 正常退出，而不是强杀
#
# 这一步是整个脚本里最重要的：客户端退出时会把用户原本的 ~/.codex 配置还回去，
# 并回收托管的中转 key。跳过它，用户卸载完会发现自己的 ChatGPT 指向一个
# 已经不存在的授权——卸载本身反而制造了故障。
# ---------------------------------------------------------------------------
if client_running; then
    say "正在退出客户端（会还原你原本的 ChatGPT 配置）…"
    osascript -e "tell application id \"${BUNDLE_ID}\" to quit" >/dev/null 2>&1 || true

    for _ in $(seq 1 30); do
        client_running || break
        sleep 1
    done

    if client_running; then
        say ""
        say "客户端没有退出，卸载中止。"
        say "请手动退出「${APP_NAME}」后重新运行本脚本。"
        say "（不强制结束：强杀会跳过配置还原，你的 ChatGPT 可能停在一个失效的授权上。）"
        exit 1
    fi
fi

# ---------------------------------------------------------------------------
# 2. 开机自启
# ---------------------------------------------------------------------------
if [ -f "${LAUNCH_AGENT}" ]; then
    say "正在移除开机自启…"
    launchctl unload "${LAUNCH_AGENT}" >/dev/null 2>&1 || true
    rm -f "${LAUNCH_AGENT}"
fi

# ---------------------------------------------------------------------------
# 3. 应用本体
# ---------------------------------------------------------------------------
if [ -d "${APP_PATH}" ]; then
    say "正在删除 ${APP_PATH}…"
    rm -rf "${APP_PATH}"
else
    say "未找到 ${APP_PATH}，跳过。"
fi

# ---------------------------------------------------------------------------
# 4. 本地数据（仅 --purge）
# ---------------------------------------------------------------------------
if [ "${PURGE}" -eq 1 ]; then
    if [ -d "${DATA_DIR}" ]; then
        say "正在删除本地数据 ${DATA_DIR}…"
        rm -rf "${DATA_DIR}"
    fi

    # 钥匙串里的加密密钥。删掉它，磁盘上任何残留的密文都再也解不开——
    # 这正是 --purge 的意义，但也是它不能作为默认行为的原因。
    security delete-generic-password \
        -s "com.gongfeiai.chatgpt-assistant.secrets.v1" \
        -a "master-key" >/dev/null 2>&1 || true

    say "本地数据已删除。"
else
    say ""
    say "本地数据保留在：${DATA_DIR}"
    say "（其中包含你原本的 Codex 配置备份。确认不再需要后，可运行 bash uninstall-mac.sh --purge 删除。）"
fi

say ""
say "卸载完成。"
