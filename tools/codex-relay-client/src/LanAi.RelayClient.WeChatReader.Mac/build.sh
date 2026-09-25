#!/bin/sh
# Builds wechat-reader for macOS, both architectures, release. Run on a Mac with Xcode 15+.
# The linker gives arm64 output an ad-hoc signature, which Apple Silicon requires to run it.
set -eu
cd "$(dirname "$0")"
swift build -c release --arch arm64 --arch x86_64
ls -l .build/apple/Products/Release/wechat-reader
