#!/usr/bin/env python3
"""容器内启动 GPT-5.6 检测器，并把监听地址开放到容器网络。

上游 `gpt56_vnext_web.py` 走 `create_server()`，那个函数把地址写死成
`("127.0.0.1", port)`（见 vendor/gpt56_vnext/server.py 的 create_server）。
容器里绑 127.0.0.1 意味着只有容器自己能连，transithub 连不上。

这里不去改 vendor 里的任何一行——vendor/ 保持与上游发行版逐字节一致，
方便日后直接换版本。改用它公开的 AppServer / AppState 自己组装服务器：
把绑定地址换成可配置的，并加一个 vendor 没有的会话重置端点（见下）。

代价：进程不再只听 127.0.0.1，容器网络内任何人都能访问。因此：
  - compose 里不给这个服务做 ports 映射，只挂内部网络；
  - 它自带的 X-GPT56-Session token 鉴权仍然生效（token 每次启动随机生成，
    调用方必须先 GET /api/bootstrap 取），重置端点同样要求这个 token。

环境变量：
  GPT56_BIND_HOST  监听地址，默认 0.0.0.0
  GPT56_BIND_PORT  监听端口，默认 8760
  GPT56_RUNS_ROOT  会话 SQLite 与报告的落盘根目录，默认 /data/runs
"""
from __future__ import annotations

from http.server import ThreadingHTTPServer
import os
import sqlite3
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parent / "vendor"
sys.path.insert(0, str(ROOT))

from gpt56_vnext import server as vendor_server  # noqa: E402
from gpt56_vnext.server import AppServer, AppState  # noqa: E402

# 会话重置端点的路径。vendor 没有这个接口，属于我们自己加的运维口子。
RESET_PATH = "/api/detector/reset-session"


class ResilientAppState(AppState):
    """Keep status polling alive when a restored SQLite session is unreadable.

    The upstream ``safe_status`` catches ``RuntimeError`` but not
    ``sqlite3.Error`` from ``progress_snapshot``. A stale/partially recovered
    session can therefore turn a harmless status poll into HTTP 500 and block
    the next run forever. The Go worker already has a reset-session recovery
    path; returning the last safe state lets it reach that path.
    """

    def safe_status(self, name: str) -> dict[str, Any]:
        try:
            return super().safe_status(name)
        except sqlite3.Error:
            with self.lock:
                status = dict(self.detector if name == "detector" else self.generator)
            if status.get("status") in {"running", "stopping"}:
                status["status"] = "interrupted"
            status.setdefault("status", "interrupted")
            status["status_read_error"] = "sqlite_unavailable"
            status.pop("progress", None)
            return status


class Handler(vendor_server.Handler):
    """在 vendor 的 Handler 上多挂一个会话重置端点。

    【为什么必须有这个口子】检测器进程重启时，会把上一次没跑完的会话标成
    `interrupted`（server.py 的 _restore_detector_state）。而它的 start 接口
    看到 interrupted 就会**自动尝试续跑那个会话**，只要新任务的 config_hash /
    申报型号 / 端点跟旧会话对不上，就抛 ValueError 返回 400——
    再被它自己的脱敏逻辑盖成「本地运行发生未分类异常」。

    结果是：任意一次容器重启（或一次没跑完的会话）都会让**所有账号**的检测
    永久 400，且错误信息完全指不到真正原因。vendor 的 stop 接口对非 running
    的会话什么都不做，重启容器也只会从 SQLite 里把同一个会话再恢复成
    interrupted，自己爬不出来。

    这个端点把那个残留会话落成 stopped 终态并清空内存状态，让下一次 start
    走全新会话。只在检测器空闲/中断时可用，running/stopping 一律拒绝——
    否则会把别人正在跑的检测悄悄丢掉。
    """

    def do_POST(self) -> None:
        if urlsplit(self.path).path != RESET_PATH:
            super().do_POST()
            return
        if not self._require_token():
            return
        try:
            self._send_json(self._reset_detector_session())
        except Exception as exc:  # noqa: BLE001 - 与 vendor 的错误约定保持一致
            self._send_json({"error": vendor_server._safe_exception_message(exc)}, 500)

    def _reset_detector_session(self) -> dict[str, Any]:
        state = self.server.state
        with state.lock:
            status = str(state.detector.get("status") or "idle")
            session_id = str(state.detector.get("session_id") or "")
            if status in {"running", "stopping"}:
                return {"reset": False, "status": status, "session_id": session_id or None}

            store = state.current_detector_store()
            if store is not None and session_id:
                try:
                    store.update_session_status(session_id, "stopped")
                except Exception:  # noqa: BLE001 - 落盘失败不该挡住内存状态复位
                    pass
            if state.detector_session is not None:
                state.detector_session.close()
            elif store is not None:
                store.close()
            state.detector_session = None
            state.detector_store = None
            state.detector_run_dir = None
            state.detector = {
                "status": "idle",
                "session_id": None,
                "updated_at": vendor_server.utc_now(),
            }
            return {"reset": True, "previous_status": status, "session_id": session_id or None}


class BoundAppServer(AppServer):
    """只为换掉 handler：vendor 的 AppServer.__init__ 把 Handler 写死了。"""

    def __init__(self, address: tuple[str, int], state: AppState):
        ThreadingHTTPServer.__init__(self, address, Handler)
        self.state = state


def main() -> int:
    host = os.environ.get("GPT56_BIND_HOST", "0.0.0.0")
    port = int(os.environ.get("GPT56_BIND_PORT", "8760"))
    runs_root = Path(os.environ.get("GPT56_RUNS_ROOT", "/data/runs"))
    runs_root.mkdir(parents=True, exist_ok=True)

    server = BoundAppServer((host, port), ResilientAppState(runs_root))
    print(f"gpt56-detector listening on {host}:{server.server_address[1]}", flush=True)
    print(f"runs_root={runs_root}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
