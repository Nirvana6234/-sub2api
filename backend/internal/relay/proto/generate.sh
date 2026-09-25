#!/usr/bin/env bash
# 生成主从通信协议的 Go 代码（relayv1）。生成结果提交进仓库，构建和 CI 不需要 protoc。
#
# 需要：
#   protoc              36.x（PROTOC 环境变量可指定路径）
#   protoc-gen-go       v1.36.11（与 go.mod 里的 google.golang.org/protobuf 一致）
#   protoc-gen-go-grpc  1.6.2
set -euo pipefail

cd "$(dirname "$0")"
PROTOC="${PROTOC:-protoc}"

rm -f relayv1/*.pb.go
mkdir -p relayv1
"$PROTOC" \
  --proto_path=. \
  --go_out=relayv1 --go_opt=paths=source_relative --go_opt=Msub2api/relay/v1/relay.proto=github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1 \
  --go-grpc_out=relayv1 --go-grpc_opt=paths=source_relative --go-grpc_opt=Msub2api/relay/v1/relay.proto=github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1 \
  sub2api/relay/v1/relay.proto

# paths=source_relative 会按 proto 路径建子目录，拍平到 relayv1/。
mv relayv1/sub2api/relay/v1/*.pb.go relayv1/
rm -rf relayv1/sub2api
