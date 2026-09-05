#!/usr/bin/env python3
"""Assembles the macOS .app bundle and its .tar.gz, on Windows.

Why a .tar.gz and not a .dmg or a .zip
--------------------------------------
hdiutil is macOS-only, so a .dmg cannot be produced here. A plain Windows .zip
would be worse than useless: it does not carry the Unix permission bits, so the
extracted binary arrives without its execute bit and the app cannot start. This
script writes the tar entries with explicit modes, which is the whole reason it
builds the archive itself rather than shelling out.

Why it refuses to produce an archive from an unsigned bundle
------------------------------------------------------------
Apple Silicon requires every arm64 executable to carry a signature -- even an
ad-hoc one. The kernel does not warn; it kills the process. .NET signs when it
builds on macOS and does NOT when cross-compiling from Windows, so a bundle
assembled here is unsigned until `rcodesign sign` has been run over it.

An unsigned tarball looks completely normal from this side and is dead on
arrival on every target machine, so the check is enforced here rather than
remembered. Use --allow-unsigned only to inspect the layout.

Usage (three steps, because signing happens between assembly and archiving):
    python build-app.py --publish <dir> --out <dir> --assemble-only
    rcodesign sign "<out>/共飞-ChatGPT助手.app"
    python build-app.py --publish <dir> --out <dir> --archive-only
"""

import argparse
import os
import re
import shutil
import sys
import tarfile
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
CLIENT_ROOT = HERE.parent.parent
REPO_ROOT = CLIENT_ROOT.parent.parent

APP_NAME = "共飞-ChatGPT助手"
CLIENT_OPTIONS = CLIENT_ROOT / "src" / "LanAi.RelayClient.Core" / "ClientOptions.cs"
# 上游布局：前端在仓库根的 frontend/，不在 sub2api/ 之下。
# 这条路径写错不会报错，只会让版本一致性检查静默跳过 —— 也就是把一道
# 防线变成一行「manifest check skipped」。
VERSION_MANIFEST = REPO_ROOT / "frontend" / "public" / "client-version.json"

# Files that must be executable inside the bundle. Everything else is 0644.
EXECUTABLE_SUFFIXES = {".dylib", ".so"}


def read_client_version():
    """The version this build reports, read from the one place that defines it."""
    text = CLIENT_OPTIONS.read_text(encoding="utf-8")
    match = re.search(r"CurrentVersion\s*=\s*new\((\d+),\s*(\d+)\)", text)
    if not match:
        raise SystemExit(f"could not find CurrentVersion in {CLIENT_OPTIONS}")
    return f"{match.group(1)}.{match.group(2)}"


def check_manifest_agrees(version):
    """Fails the build when the manifest and the binary disagree about the version.

    Not a nicety: a build whose version trails the served manifest offers its own
    users an update to the release they are already running, forever. The check is
    cheap and the symptom is otherwise invisible from the build machine.
    """
    if not VERSION_MANIFEST.exists():
        print(f"  ! {VERSION_MANIFEST} not found, manifest check skipped")
        return

    import json

    manifest = json.loads(VERSION_MANIFEST.read_text(encoding="utf-8"))
    declared = str(manifest.get("version", ""))
    if declared != version:
        raise SystemExit(
            f"version mismatch: ClientOptions says {version}, "
            f"client-version.json says {declared}"
        )
    print(f"  manifest agrees: {declared}")


def read_bundle_executable(plist_text):
    match = re.search(
        r"<key>CFBundleExecutable</key>\s*<string>([^<]+)</string>", plist_text
    )
    if not match:
        raise SystemExit("Info.plist has no CFBundleExecutable")
    return match.group(1)


def assemble(publish_dir, out_dir, version):
    app = out_dir / f"{APP_NAME}.app"
    contents = app / "Contents"
    macos = contents / "MacOS"
    resources = contents / "Resources"

    if app.exists():
        # Replaced outright, never merged. A stale file left from an earlier layout
        # would ship inside the bundle and nothing would point at it.
        shutil.rmtree(app)

    macos.mkdir(parents=True)
    resources.mkdir(parents=True)

    for item in publish_dir.iterdir():
        if item.is_dir():
            shutil.copytree(item, macos / item.name)
        else:
            shutil.copy2(item, macos / item.name)

    plist_text = (HERE / "Info.plist").read_text(encoding="utf-8")
    plist_text = plist_text.replace("__VERSION__", version)
    (contents / "Info.plist").write_text(plist_text, encoding="utf-8")

    icns = HERE / "LanAi.RelayClient.icns"
    if not icns.exists():
        raise SystemExit(f"{icns} is missing; run build-icns.py first")

    # CFBundleIconFile says "AppIcon", and macOS appends .icns and looks in
    # Resources. The name has to match there, not here.
    shutil.copy2(icns, resources / "AppIcon.icns")

    executable = read_bundle_executable(plist_text)
    if not (macos / executable).is_file():
        raise SystemExit(
            f"Info.plist names CFBundleExecutable '{executable}', "
            f"but Contents/MacOS/{executable} does not exist.\n"
            f"    present: {sorted(p.name for p in macos.iterdir() if p.is_file())[:6]} ...\n"
            "    A wrong name here gives no error on macOS -- the app simply does "
            "nothing when double-clicked."
        )

    print(f"  assembled {app.name} (executable: {executable})")
    return app, executable


def is_signed(app):
    """Whether rcodesign (or codesign) has been over this bundle."""
    return (app / "Contents" / "_CodeSignature" / "CodeResources").is_file()


def archive(app, out_dir, version, executable):
    target = out_dir / f"codex-relay-client_v{version}_macos-arm64.tar.gz"
    if target.exists():
        target.unlink()

    root = app.parent

    def add(path, arcname):
        info = tarfile.TarInfo(arcname)
        stat = path.stat()
        info.mtime = int(stat.st_mtime)

        if path.is_dir():
            info.type = tarfile.DIRTYPE
            info.mode = 0o755
            tar.addfile(info)
            return

        info.size = stat.st_size

        # The permission bits are set here rather than copied from disk, because
        # this runs on Windows where they do not exist. Getting the executable
        # wrong means an app that cannot start on every target machine.
        needs_exec = path.name == executable or path.suffix in EXECUTABLE_SUFFIXES
        info.mode = 0o755 if needs_exec else 0o644

        with path.open("rb") as handle:
            tar.addfile(info, handle)

    executable_entries = 0
    with tarfile.open(target, "w:gz") as tar:
        # The bundle directory first, so an extractor sees the root before its
        # contents rather than having to infer it.
        add(app, app.name)

        for path in sorted(app.rglob("*")):
            arcname = str(path.relative_to(root)).replace(os.sep, "/")
            add(path, arcname)
            if path.is_file() and (path.name == executable or path.suffix in EXECUTABLE_SUFFIXES):
                executable_entries += 1

    print(f"  wrote {target.name} ({target.stat().st_size:,} bytes)")
    print(f"  {executable_entries} entries marked executable")
    return target


def verify_archive(target, executable):
    """Reads the archive back and checks the bits that cannot be seen on Windows."""
    with tarfile.open(target, "r:gz") as tar:
        members = tar.getmembers()

    main = [m for m in members if m.name.endswith("/MacOS/" + executable)]
    if not main:
        raise SystemExit(f"archive contains no Contents/MacOS/{executable}")
    if not main[0].mode & 0o111:
        raise SystemExit(f"{main[0].name} is not executable in the archive (mode {main[0].mode:o})")

    plist = [m for m in members if m.name.endswith("/Contents/Info.plist")]
    if not plist:
        raise SystemExit("archive contains no Contents/Info.plist")

    icon = [m for m in members if m.name.endswith("/Resources/AppIcon.icns")]
    if not icon:
        raise SystemExit("archive contains no Resources/AppIcon.icns")

    print(f"  verified: {len(members)} entries, executable bit set, plist and icon present")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--publish", required=True, type=Path)
    parser.add_argument("--out", type=Path, default=CLIENT_ROOT / "artifacts" / "macos")
    parser.add_argument("--version")
    parser.add_argument("--assemble-only", action="store_true",
                        help="build the bundle and stop, so it can be signed before archiving")
    parser.add_argument("--archive-only", action="store_true",
                        help="skip assembly; archive an existing (now signed) bundle")
    parser.add_argument("--allow-unsigned", action="store_true",
                        help="archive without a signature; the result will not run")
    args = parser.parse_args()

    version = args.version or read_client_version()
    print(f"version {version}")
    check_manifest_agrees(version)

    args.out.mkdir(parents=True, exist_ok=True)

    if args.archive_only:
        app = args.out / f"{APP_NAME}.app"
        if not app.exists():
            raise SystemExit(f"{app} does not exist; run without --archive-only first")
        plist_text = (app / "Contents" / "Info.plist").read_text(encoding="utf-8")
        executable = read_bundle_executable(plist_text)
    else:
        if not args.publish.is_dir():
            raise SystemExit(f"{args.publish} is not a directory")
        app, executable = assemble(args.publish, args.out, version)

    if args.assemble_only:
        # Deliberately before the signature gate: this mode exists precisely to
        # produce the unsigned bundle that `rcodesign sign` is about to operate on.
        print("  assemble-only: sign the bundle, then re-run with --archive-only")
        return

    if is_signed(app):
        print("  signature present")
    elif args.allow_unsigned:
        print("  ! UNSIGNED -- this build will be killed by the kernel on Apple Silicon")
    else:
        raise SystemExit(
            f"\n{app.name} is not signed.\n"
            "  Apple Silicon requires a signature on every arm64 executable, even an\n"
            "  ad-hoc one, and .NET does not sign when cross-compiling from Windows.\n"
            "  An unsigned build looks fine here and dies on every target machine.\n\n"
            f'  Run:  rcodesign sign "{app}"\n'
            "  Then: python build-app.py --publish ... --out ... --archive-only\n\n"
            "  (--allow-unsigned skips this, for inspecting the layout only.)"
        )

    target = archive(app, args.out, version, executable)
    verify_archive(target, executable)


if __name__ == "__main__":
    sys.exit(main())
