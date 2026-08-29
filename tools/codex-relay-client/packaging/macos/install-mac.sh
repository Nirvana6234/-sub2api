#!/bin/bash
#
# 共飞-ChatGPT助手 —— macOS 安装 / 更新脚本
#
#   curl -fsSL https://gongfeiai.com/install-mac.sh | bash
#
# 这个脚本同时是安装器和更新器：重跑一次就是升级。
#
# 为什么走终端而不是下载 .dmg 双击
# --------------------------------
# com.apple.quarantine 是浏览器打上去的标记，不是文件本身的属性。用 curl 取回来的
# 文件不带这个标记，因此不会触发 Gatekeeper 拦截。没有开发者账号就没有公证，
# 而未公证的应用双击后要走「系统设置 → 隐私与安全性 → 仍要打开 → 输密码」五步，
# 这一步会劝退的正是本产品的目标用户。
#
# 不做备份
# --------
# 升级时直接替换 /Applications 下的应用，不创建任何备份或回滚副本。
# 用户的登录状态与配置在 ~/Library/Application Support 下，不随应用走。

set -euo pipefail

APP_NAME="共飞-ChatGPT助手"
APP_PATH="/Applications/${APP_NAME}.app"
BUNDLE_ID="com.gongfeiai.chatgpt-assistant"
# 刻意不带版本号。这个脚本同时是更新器，用户手里的那份可能是几个版本以前的，
# 它必须永远取到最新的包 —— 所以发布流程要把 build-app.py 产出的
# codex-relay-client_v<版本>_macos-arm64.tar.gz 以这个固定名字对外提供。
DOWNLOAD_URL="${GONGFEI_DOWNLOAD_URL:-https://gongfeiai.com/download/codex-relay-client_macos-arm64.tar.gz}"

say() { printf '%s\n' "$*"; }
fail() { printf '\n错误：%s\n' "$*" >&2; exit 1; }

# macOS 的 pgrep -x 比对的是进程记账名，长度有历史限制，而可执行文件叫
# LanAi.RelayClient.App（21 字符）。改用 -f 比对完整命令行里的应用路径：
# 既避开长度问题，也不会误伤同名的其它进程。
client_running() {
    pgrep -f "${APP_PATH}/Contents/MacOS/" >/dev/null 2>&1
}


# ---------------------------------------------------------------------------
# 1. 架构检查
#
# v1 只出 arm64。Intel 机器装上去会「安装成功、打开没反应」——必须在下载前就说清楚，
# 而不是让用户装完再面对一个打不开的图标。
# ---------------------------------------------------------------------------
arch="$(uname -m)"
if [ "$arch" != "arm64" ]; then
    fail "当前是 ${arch} 架构的 Mac（Intel 芯片），本版本只支持 Apple 芯片（M 系列）。
      装上去会打不开。请联系客服确认 Intel 版本的进展。"
fi

# ---------------------------------------------------------------------------
# 2. 退出正在运行的旧版本
#
# 走 Apple Events 而不是 kill：客户端退出时要把用户原本的 ~/.codex 配置还回去，
# 并回收托管的中转 key。强杀会跳过这一步，用户的 ChatGPT 会留在一个已被吊销的
# key 上——而这个故障要到下次打开 ChatGPT 才会显现。
# ---------------------------------------------------------------------------
if client_running; then
    say "正在退出运行中的旧版本…"
    osascript -e "tell application id \"${BUNDLE_ID}\" to quit" >/dev/null 2>&1 || true

    for _ in $(seq 1 30); do
        client_running || break
        sleep 1
    done

    if client_running; then
        fail "旧版本没有退出。请手动退出「${APP_NAME}」后重新运行本脚本。
      （不自动强制结束：强杀会跳过配置还原，可能让 ChatGPT 停在一个失效的授权上。）"
    fi
fi

# ---------------------------------------------------------------------------
# 3. 下载并解压
#
# 先解压到临时目录再整体搬运：直接就地解压时，一旦下载不完整，
# /Applications 里会留下一个半截的应用，而它看起来和装好的没有区别。
# ---------------------------------------------------------------------------
workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

say "正在下载…"
curl -fSL --progress-bar "${DOWNLOAD_URL}" -o "${workdir}/client.tar.gz" \
    || fail "下载失败，请检查网络后重试。"

say "正在解压…"
tar -xzf "${workdir}/client.tar.gz" -C "${workdir}" || fail "安装包损坏，请重新运行本脚本。"

staged="${workdir}/${APP_NAME}.app"
[ -d "${staged}" ] || fail "安装包内容异常：找不到 ${APP_NAME}.app"
[ -x "${staged}/Contents/MacOS/LanAi.RelayClient.App" ] \
    || fail "安装包内的程序缺少可执行权限，请联系客服。"

# ---------------------------------------------------------------------------
# 4. 就地替换
# ---------------------------------------------------------------------------
if [ -d "${APP_PATH}" ]; then
    say "正在替换旧版本…"
    rm -rf "${APP_PATH}" || fail "无法删除旧版本，请检查 /Applications 的权限。"
fi

mv "${staged}" "${APP_PATH}" || fail "无法写入 /Applications，请检查权限。"

# 双保险：curl 取回的文件本就不带 quarantine，但如果用户是先用浏览器下了
# tar.gz 再手动跑这个脚本，标记会跟着解压出来的文件走。
xattr -dr com.apple.quarantine "${APP_PATH}" >/dev/null 2>&1 || true

# ---------------------------------------------------------------------------
# 5. 启动
# ---------------------------------------------------------------------------
say "正在启动…"
open "${APP_PATH}" || fail "安装完成，但没能自动启动。请到「应用程序」里手动打开。"

say ""
say "安装完成：${APP_PATH}"
say "以后升级重跑这条命令即可。"
