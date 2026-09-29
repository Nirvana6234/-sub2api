#!/usr/bin/env bash
# 生成 macOS 包的代码签名证书（自签名）。整个项目只跑一次。
#
#   bash packaging/macos/new-signing-cert.sh <保存私钥的目录>
#
# 在 Windows 的 Git Bash 或 Mac 的终端里都能跑，只要有 openssl。
#
# 为什么要一张固定的证书
# ----------------------
# macOS 按签名里的「指定要求」认一个 App。ad-hoc 签名的指定要求是这次构建的 cdhash，
# 每个版本都不同，于是每次升级后系统都当它是另一个程序：「屏幕录制」要重新授权，钥匙串
# 「始终允许」也会再问。用同一张证书签，指定要求就是
#   identifier "com.gongfeiai.chatgpt-assistant" and certificate root = H"<本证书的 SHA-1>"
# 每个版本都一样，授权随升级保留。它不是 Apple 签发的，不能公证，Gatekeeper 照旧拦浏览器
# 下载的包——这一点与 ad-hoc 相同，所以仍然用 install-mac.sh 安装。
#
# 产出
#   packaging/macos/signing-cert.pem   公开证书，提交进仓库。流水线用它核对签出来的指定要求，
#                                      防止 secret 被换成别的证书而没人发现。
#   <目录>/signing.p12                 私钥 + 证书，用下面的密码加密。离线备份一份，不要提交。
#   <目录>/signing.p12.password        p12 的密码。
#   <目录>/signing.p12.base64          p12 的 base64，填进 GitHub secret 用。
#
# 私钥丢了：下个版本只能换新证书，所有用户再重新授权一次。
# 私钥泄露：拿到的人能签出「被系统当成共飞助手」的程序，直接继承用户给过的屏幕录制与钥匙串授权。
# 所以它只该存在于 GitHub secret 和一份离线备份里。
#
# 已经有 signing-cert.pem 时拒绝运行：换证书就是让所有用户重新授权，不能是误操作。
# 真要换（私钥泄露），先删掉 signing-cert.pem 再跑，并在发版说明里告诉用户。

set -euo pipefail

if [ $# -ne 1 ]; then
  echo "用法：bash packaging/macos/new-signing-cert.sh <保存私钥的目录（不要放在仓库里）>" >&2
  exit 2
fi

here="$(cd "$(dirname "$0")" && pwd)"
public="$here/signing-cert.pem"
out="$1"

if [ -e "$public" ]; then
  echo "已经有 $public。换证书会让所有用户重新授权屏幕录制，所以这里不覆盖。" >&2
  echo "确实要换（私钥泄露了），先删掉它再运行。" >&2
  exit 1
fi

mkdir -p "$out"
out="$(cd "$out" && pwd)"
repo="$(cd "$here/../../../.." && pwd)"
case "$out/" in
  "$repo"/*) rmdir "$out" 2>/dev/null || true
             echo "私钥目录 $out 在仓库里面，换一个仓库外的目录。" >&2; exit 1 ;;
esac
for f in signing.p12 signing.p12.password signing.p12.base64; do
  if [ -e "$out/$f" ]; then echo "$out/$f 已存在，不覆盖。" >&2; exit 1; fi
done

work="$(mktemp -d)"
trap 'cd / && rm -rf "$work"' EXIT
umask 077

# 只在临时目录里用相对路径：Git Bash 带的 openssl 是原生 Windows 程序，认不得 /tmp/… 这种路径。
cd "$work"

cat > cert.cnf <<'CNF'
[req]
distinguished_name = dn
prompt = no
x509_extensions = ext
[dn]
CN = Gongfei AI Code Signing
O = Gongfei AI
C = CN
[ext]
basicConstraints = critical, CA:FALSE
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
CNF

# 30 年：中途过期就得换证书，而换证书等于让所有用户重新授权。
openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 10950 \
  -config cert.cnf -keyout key.pem -out cert.pem 2>/dev/null \
  || { echo "openssl req 失败" >&2; exit 1; }

# 去掉 \r 和 \n：Git Bash 带的 openssl 输出 \r\n 结尾，只删 \n 会把回车留进密码——p12 的真实密码
# 就多了一个看不见的 \r，而粘贴进 GitHub secret 时它会丢，流水线报「密码错误」。
openssl rand -base64 24 | tr -d '\r\n' > password

# 3DES + SHA-1 MAC：rcodesign 用的 p12 解析库只认这种老格式，OpenSSL 3 的默认（AES + PBKDF2）它读不了。
openssl pkcs12 -export -inkey key.pem -in cert.pem \
  -name "Gongfei AI Code Signing" \
  -keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 \
  -passout file:password -out signing.p12

openssl base64 -A -in signing.p12 | tr -d '\r\n' > signing.p12.base64

cp signing.p12 "$out/signing.p12"
cp password "$out/signing.p12.password"
cp signing.p12.base64 "$out/signing.p12.base64"
cp cert.pem "$public"

fingerprint="$(openssl x509 -in cert.pem -noout -fingerprint -sha1 | sed 's/.*=//; s/://g' | tr 'A-F' 'a-f')"

cat <<EOF

已生成。证书 SHA-1：$fingerprint

1. 把公开证书提交进仓库：
     git add tools/codex-relay-client/packaging/macos/signing-cert.pem

2. 在仓库的 GitHub secret 里填两项（Settings → Secrets and variables → Actions，或用 gh）：
     gh secret set MACOS_SIGNING_P12_BASE64   < "$out/signing.p12.base64"
     gh secret set MACOS_SIGNING_P12_PASSWORD < "$out/signing.p12.password"

3. 把 $out 整个离线备份（加密 U 盘、密码管理器等），然后从这台机器上删掉。
EOF
