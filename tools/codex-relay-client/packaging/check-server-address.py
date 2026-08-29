#!/usr/bin/env python3
"""Checks which relay address a shipping build actually carries.

Why the bytes and not the build flags
-------------------------------------
This is the one value that, if wrong, points real users' credentials at the
wrong host, and every way of getting it wrong is silent: a build made with
-p:TestServer=true looks identical from the outside, and a placeholder address
produces "连不上服务器" rather than an error anyone can act on.

Why a script and not a grep
---------------------------
Two reasons a plain grep finds nothing even when the string is there:
single-file publishes embed the managed assemblies in a bundle, and .NET stores
string literals as UTF-16LE. So the search has to be encoding-aware.

Usage:
    python check-server-address.py <path-to-exe-or-dll> [...]

Exit code is non-zero if the production address is missing, or if the test or
placeholder address is present.
"""

import sys
from pathlib import Path

# 本脚本会把应用名（含中文）打到 stdout。GitHub 的 Windows runner 上 Python 的
# stdout 编码是 cp1252，编不出中文，print 会抛 UnicodeEncodeError —— 而这发生在
# 组装完成之后，表现为「明明干完了却退出码 1」。本机看不出来：中文 Windows 的
# 控制台代码页是 936，恰好编得出。
#
# 不能只靠 CI 里设 PYTHONUTF8：手动跑这个脚本的人不会带上那个环境变量。
if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")


PRODUCTION = "https://gongfeiai.com/"
FORBIDDEN = {
    "test server": "http://test.gongfeiai.com/",
    "placeholder": "http://127.0.0.1:8080/",
}


def count(blob, text):
    return blob.count(text.encode("utf-16-le")) + blob.count(text.encode("utf-8"))


def check(path):
    blob = path.read_bytes()
    print(f"== {path.name} ({len(blob):,} bytes)")

    production = count(blob, PRODUCTION)
    print(f"   production {PRODUCTION}: {production}")

    problems = []
    if production < 1:
        problems.append(f"{path.name} does not contain the production address")

    for label, address in FORBIDDEN.items():
        hits = count(blob, address)
        print(f"   {label} {address}: {hits}")
        if hits:
            problems.append(f"{path.name} contains the {label} address {address}")

    return problems


def main(argv):
    if len(argv) < 2:
        raise SystemExit(__doc__)

    problems = []
    for name in argv[1:]:
        path = Path(name)
        if not path.is_file():
            raise SystemExit(f"not a file: {path}")
        problems.extend(check(path))

    if problems:
        for problem in problems:
            print(f"::error::{problem}")
        return 1

    print("server address OK")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
