#!/usr/bin/env python3
"""容器内启动 GPT-5.6 检测器，并把监听地址开放到容器网络。

上游 `gpt56_vnext_web.py` 走 `create_server()`，那个函数把地址写死成
`("127.0.0.1", port)`（见 vendor/gpt56_vnext/server.py 的 create_server）。
容器里绑 127.0.0.1 意味着只有容器自己能连，transithub 连不上。

这里不去改 vendor 里的任何一行——vendor/ 保持与上游发行版逐字节一致，
方便日后直接换版本。改用它 `__all__` 里公开的 AppServer / AppState 自己组装
服务器，只是把绑定地址换成可配置的。

代价：进程不再只听 127.0.0.1，容器网络内任何人都能访问。因此：
  - compose 里不给这个服务做 ports 映射，只挂内部网络；
  - 它自带的 X-GPT56-Session token 鉴权仍然生效（token 每次启动随机生成，
    调用方必须先 GET /api/bootstrap 取）。

环境变量：
  GPT56_BIND_HOST  监听地址，默认 0.0.0.0
  GPT56_BIND_PORT  监听端口，默认 8760
  GPT56_RUNS_ROOT  会话 SQLite 与报告的落盘根目录，默认 /data/runs
"""
from __future__ import annotations

import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent / "vendor"
sys.path.insert(0, str(ROOT))

from gpt56_vnext.server import AppServer, AppState  # noqa: E402


def main() -> int:
    host = os.environ.get("GPT56_BIND_HOST", "0.0.0.0")
    port = int(os.environ.get("GPT56_BIND_PORT", "8760"))
    runs_root = Path(os.environ.get("GPT56_RUNS_ROOT", "/data/runs"))
    runs_root.mkdir(parents=True, exist_ok=True)

    server = AppServer((host, port), AppState(runs_root))
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
