#!/usr/bin/env python3
"""Builds the macOS .icns from the Windows .ico the client already ships.

Why this is a repackage rather than an image conversion
------------------------------------------------------
Every entry inside LanAi.RelayClient.ico is already a PNG (verified: 8 entries,
16 through 256, all with a PNG signature), and the modern .icns format stores
PNG payloads directly. So this copies bytes into a different container. Nothing
is decoded, resampled, or re-encoded, which means the macOS icon cannot drift
from the Windows one -- they are the same pixels.

That is also why there is no Pillow dependency: there is nothing to process.

The one honest limitation
-------------------------
The source tops out at 256x256, so the 512 and 1024 slots are left empty and
macOS upscales for the largest Finder preview. Upscaling here instead would
look worse and would hide the fact that the master art is only 256. If a crisp
1024 is wanted, the fix is a bigger source icon, not a smarter script.

Usage:
    python build-icns.py [source.ico] [output.icns]
"""

import struct
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


HERE = Path(__file__).resolve().parent
DEFAULT_ICO = HERE.parent.parent / "src" / "LanAi.RelayClient.App" / "Assets" / "LanAi.RelayClient.ico"
DEFAULT_ICNS = HERE / "LanAi.RelayClient.icns"

# The window and tray icon, as a PNG the runtime can decode on every platform.
# .ico decoding is a Windows-specific convenience; Skia -- which is what Avalonia
# decodes with on macOS -- is not obliged to handle it, and TrayPresence.LoadIcon
# falls back to *no icon* rather than failing, so a decode miss shows up as a blank
# spot in the menu bar and nothing in the log to explain it.
DEFAULT_PNG = HERE.parent.parent / "src" / "LanAi.RelayClient.App" / "Assets" / "LanAi.RelayClient.png"
PNG_ASSET_SIZE = 256

PNG_SIGNATURE = b"\x89PNG\r\n\x1a\x1a"[:4] + b"\r\n\x1a\n"

# icns chunk type -> the pixel size it must contain.
#
# The @2x types matter more than they look: macOS picks ic11/ic12/ic13 on a
# Retina display, and an .icns without them renders the 1x art scaled up on
# exactly the machines this client targets (v1 is Apple Silicon only).
CHUNKS = [
    ("icp4", 16),
    ("icp5", 32),
    ("icp6", 64),
    ("ic07", 128),
    ("ic08", 256),
    ("ic11", 32),   # 16@2x
    ("ic12", 64),   # 32@2x
    ("ic13", 256),  # 128@2x
]


def read_ico(path):
    """Returns {size: png_bytes} for every PNG-encoded entry in the .ico."""
    blob = path.read_bytes()
    _reserved, image_type, count = struct.unpack_from("<HHH", blob, 0)
    if image_type != 1:
        raise SystemExit(f"{path} is not an icon file (type={image_type})")

    images = {}
    for index in range(count):
        width, height, _colors, _r, _planes, _bpp, size, offset = struct.unpack_from(
            "<BBBBHHII", blob, 6 + index * 16
        )
        width = width or 256
        height = height or 256
        payload = blob[offset : offset + size]

        if not payload.startswith(PNG_SIGNATURE):
            # A BMP/DIB entry would need decoding and an alpha-mask fixup, which
            # is exactly the complexity this script avoids. Skipped loudly rather
            # than silently, so a re-exported icon that drops PNG encoding is
            # noticed here and not on a Mac.
            print(f"  skipped {width}x{height}: not PNG-encoded")
            continue

        if width != height:
            print(f"  skipped {width}x{height}: not square")
            continue

        images[width] = payload

    return images


def build_icns(images):
    chunks = bytearray()
    written = []
    for chunk_type, size in CHUNKS:
        png = images.get(size)
        if png is None:
            print(f"  missing {size}x{size}, {chunk_type} omitted")
            continue

        chunks += chunk_type.encode("ascii")
        chunks += struct.pack(">I", len(png) + 8)
        chunks += png
        written.append(f"{chunk_type}({size})")

    if not written:
        raise SystemExit("no usable images; refusing to write an empty .icns")

    print("  wrote " + ", ".join(written))
    return b"icns" + struct.pack(">I", len(chunks) + 8) + bytes(chunks)


def verify(path):
    """Reads the file back and checks every chunk against what it claims to be.

    Worth doing here rather than trusting the writer: a malformed .icns does not
    fail loudly on macOS, it just renders as a generic application icon -- and
    nobody on this side of the build can see that happen.
    """
    blob = path.read_bytes()
    magic, declared = struct.unpack_from(">4sI", blob, 0)
    if magic != b"icns" or declared != len(blob):
        raise SystemExit(f"bad header: magic={magic!r} declared={declared} actual={len(blob)}")

    sizes = dict(CHUNKS)
    offset = 8
    while offset < len(blob):
        chunk_type, length = struct.unpack_from(">4sI", blob, offset)
        chunk_type = chunk_type.decode("ascii")
        payload = blob[offset + 8 : offset + length]

        if not payload.startswith(PNG_SIGNATURE):
            raise SystemExit(f"{chunk_type}: payload is not a PNG")

        # PNG IHDR sits at a fixed offset: 8-byte signature, 4-byte chunk length,
        # 4-byte "IHDR", then width and height.
        width, height = struct.unpack_from(">II", payload, 16)
        expected = sizes.get(chunk_type)
        if width != height or width != expected:
            raise SystemExit(f"{chunk_type}: contains {width}x{height}, expected {expected}")

        offset += length

    if offset != len(blob):
        raise SystemExit(f"chunk lengths do not add up: ended at {offset} of {len(blob)}")

    print("  verified: header, chunk lengths, and every payload size")


def main():
    source = Path(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_ICO
    target = Path(sys.argv[2]) if len(sys.argv) > 2 else DEFAULT_ICNS

    print(f"reading {source}")
    images = read_ico(source)
    print(f"  found sizes: {sorted(images)}")

    data = build_icns(images)
    target.write_bytes(data)
    print(f"wrote {target} ({len(data):,} bytes)")
    verify(target)

    png = images.get(PNG_ASSET_SIZE)
    if png is None:
        raise SystemExit(f"no {PNG_ASSET_SIZE}x{PNG_ASSET_SIZE} image for the shared PNG asset")

    DEFAULT_PNG.write_bytes(png)
    print(f"wrote {DEFAULT_PNG} ({len(png):,} bytes)")


if __name__ == "__main__":
    main()
