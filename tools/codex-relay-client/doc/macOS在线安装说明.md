# 共飞-ChatGPT助手 macOS 在线安装说明

## 一、服务器文件布局

在线安装脚本和 macOS 压缩包需要放在公网可访问的位置：

```text
网站根目录/
├─ install-mac.sh
├─ uninstall-mac.sh
└─ download/
   └─ codex-relay-client_macos-arm64.tar.gz
```

对应的公网地址必须是：

```text
https://gongfeiai.com/install-mac.sh
https://gongfeiai.com/uninstall-mac.sh
https://gongfeiai.com/download/codex-relay-client_macos-arm64.tar.gz
```

其中：

- `install-mac.sh` 是安装和升级脚本。
- `uninstall-mac.sh` 是卸载脚本，可选但建议一起提供。
- `codex-relay-client_macos-arm64.tar.gz` 是实际安装包。

脚本里使用的是不带版本号的固定地址：

```text
https://gongfeiai.com/download/codex-relay-client_macos-arm64.tar.gz
```

因此 GitHub Actions 生成的：

```text
codex-relay-client_v0.2_macos-arm64.tar.gz
```

上传到服务器后，需要复制或重命名为：

```text
codex-relay-client_macos-arm64.tar.gz
```

以后发布新版本时，只替换这个固定文件即可，不需要修改安装脚本。

## 二、用户在线安装

用户在 Mac 上打开“终端”，执行：

```bash
curl -fsSL https://gongfeiai.com/install-mac.sh | bash
```

脚本会自动完成：

1. 检查当前 Mac 是否为 Apple 芯片。
2. 下载 `codex-relay-client_macos-arm64.tar.gz`。
3. 解压到临时目录。
4. 将 `共飞-ChatGPT助手.app` 安装到 `/Applications`。
5. 删除旧版本并替换为新版本。
6. 自动启动客户端。

安装完成后，应用位置是：

```text
/Applications/共飞-ChatGPT助手.app
```

`.tar.gz` 不需要用户手动解压。安装脚本会自动解压，并且会保留 macOS 所需的可执行权限。

## 三、升级

重复执行同一条命令即可升级：

```bash
curl -fsSL https://gongfeiai.com/install-mac.sh | bash
```

升级时脚本会替换：

```text
/Applications/共飞-ChatGPT助手.app
```

登录状态和客户端配置保存在：

```text
~/Library/Application Support/LanAi.RelayClient
```

升级不会删除这部分本地数据。

## 四、卸载

下载并执行卸载脚本：

```bash
curl -fsSL https://gongfeiai.com/uninstall-mac.sh -o /tmp/uninstall-mac.sh
bash /tmp/uninstall-mac.sh
```

默认只删除应用和开机启动项，保留登录状态及本地配置。

如果确认连本地数据也不需要，再执行：

```bash
bash /tmp/uninstall-mac.sh --purge
```

## 五、从 GitHub Release 手动安装

如果用户下载了 GitHub Release 中的文件，可以先下载：

```text
install-mac.sh
codex-relay-client_v0.2_macos-arm64.tar.gz
```

但当前 `install-mac.sh` 默认仍会从固定在线地址下载压缩包，不会自动读取同目录下的本地压缩包。

手动运行本地脚本时，可以指定一个公网压缩包地址：

```bash
GONGFEI_DOWNLOAD_URL="https://gongfeiai.com/download/codex-relay-client_macos-arm64.tar.gz" \
bash install-mac.sh
```

如果只有本地 `.tar.gz` 文件，直接解压后将应用拖到“应用程序”即可，但推荐使用在线安装脚本，因为脚本会检查架构、替换旧版本、清理隔离属性并自动启动。

## 六、架构限制

当前 GitHub Actions 产物是：

```text
osx-arm64
```

只支持 Apple 芯片 Mac：

- M1
- M2
- M3
- M4

Intel Mac 的 `uname -m` 通常是 `x86_64`，当前安装脚本会直接提示不支持，不会继续安装一个无法启动的版本。

## 七、上线前检查

在浏览器或终端确认三个地址都能访问：

```bash
curl -I https://gongfeiai.com/install-mac.sh
curl -I https://gongfeiai.com/uninstall-mac.sh
curl -I https://gongfeiai.com/download/codex-relay-client_macos-arm64.tar.gz
```

压缩包地址不能返回 HTML 错误页，应该返回实际的 `.tar.gz` 文件。

最小上线清单：

- `install-mac.sh` 文件可读。
- `uninstall-mac.sh` 文件可读。
- 固定名称的 `codex-relay-client_macos-arm64.tar.gz` 已上传。
- 压缩包 URL 不需要登录或额外 Cookie。
- HTTPS 证书有效。
- 每次发布新版本后，替换固定名称的压缩包。
