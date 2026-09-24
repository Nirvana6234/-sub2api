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

Channels
--------
A build talks to exactly one of three servers, chosen by a publish property
(never by editing source), and this script checks the bytes against the channel
you say you built:

    --channel production   (default)   dotnet publish ...                      https://gongfeiai.com/
    --channel test                     dotnet publish ... -p:TestServer=true   http://test.gongfeiai.com/
    --channel local                    dotnet publish ... -p:LocalServer=true  http://127.0.0.1:8080/

The expected address must be present and the other two absent. Anything else is a
build that would talk to a server nobody meant it to.

Usage:
    python check-server-address.py [--channel production|test|local] <path-to-exe-or-dll> [...]

Exit code is non-zero if the channel's address is missing, or another channel's is present.
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


CHANNELS = {
    "production": "https://gongfeiai.com/",
    "test": "http://test.gongfeiai.com/",
    "local": "http://127.0.0.1:8080/",
}


def count(blob, text):
    return blob.count(text.encode("utf-16-le")) + blob.count(text.encode("utf-8"))


def check(path, channel):
    blob = path.read_bytes()
    print(f"== {path.name} ({len(blob):,} bytes), channel: {channel}")

    problems = []
    for name, address in CHANNELS.items():
        hits = count(blob, address)
        marker = "expected" if name == channel else "forbidden"
        print(f"   {name} {address}: {hits} ({marker})")
        if name == channel and hits < 1:
            problems.append(f"{path.name} does not contain the {name} address {address}")
        if name != channel and hits:
            problems.append(f"{path.name} contains the {name} address {address}, but was built for the {channel} channel")

    return problems


def main(argv):
    args = argv[1:]
    channel = "production"
    if "--channel" in args:
        i = args.index("--channel")
        if i + 1 >= len(args) or args[i + 1] not in CHANNELS:
            raise SystemExit(f"--channel must be one of: {', '.join(CHANNELS)}")
        channel = args[i + 1]
        del args[i:i + 2]

    if not args:
        raise SystemExit(__doc__)

    problems = []
    for name in args:
        path = Path(name)
        if not path.is_file():
            raise SystemExit(f"not a file: {path}")
        problems.extend(check(path, channel))

    if problems:
        for problem in problems:
            print(f"::error::{problem}")
        return 1

    print(f"server address OK ({channel})")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
