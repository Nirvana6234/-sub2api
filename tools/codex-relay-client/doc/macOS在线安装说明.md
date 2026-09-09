# 共飞-ChatGPT助手 macOS 在线安装说明

## 一、服务器文件布局

安装/卸载脚本和 macOS 压缩包放在独立的静态文件服务器上（不是 sub2api 主应用服务器，主应用是 SPA，没有静态文件路由，访问不存在的路径会被路由兜底返回网页 HTML）：

```text
download.gongfeiai.com/downloads/
├─ install-mac.sh
├─ uninstall-mac.sh
└─ codex-relay-client_v<版本号>_macos-arm64.tar.gz   例如 codex-relay-client_v0.2_macos-arm64.tar.gz
```

对应的公网地址：

```text
https://download.gongfeiai.com/downloads/install-mac.sh
https://download.gongfeiai.com/downloads/uninstall-mac.sh
https://download.gongfeiai.com/downloads/codex-relay-client_v0.2_macos-arm64.tar.gz
```

压缩包**带版本号**上传即可，不需要额外重命名成固定文件名——`install-mac.sh` 不会硬编码某个具体文件名，而是运行时实时向官网（`https://gongfeiai.com/api/v1/settings/public`）查询当前发布的地址，取的是后台设置项 `client_download_direct_url_mac`。这跟客户端下载页（`/download`）用的是同一个数据源。

因此发布新版本的步骤是：

1. 把 GitHub Actions 产出的 `codex-relay-client_v<新版本>_macos-arm64.tar.gz` 上传到 `download.gongfeiai.com/downloads/`（保留旧版本文件，不必删除）。
2. 在后台把设置项 `client_download_direct_url_mac` 改成指向新文件的地址，`client_latest_version_mac` 改成新版本号。
3. **通过后台面板保存设置**，而不是直接改数据库——sub2api 有一层进程内 HTML 渲染缓存，只有走后台保存的更新路径才会触发缓存失效；如果是直接执行 SQL 改的，改完需要重启一次 `sub2api` 容器，否则下载页显示的还是旧值（`/api/v1/settings/public` 接口本身没有这层缓存，是实时的）。

`install-mac.sh` 和 `uninstall-mac.sh` 本身内容不跟版本号绑定，不需要跟着每次发版重新上传。

## 二、用户在线安装

用户在 Mac 上打开“终端”，执行：

```bash
curl -fsSL https://download.gongfeiai.com/downloads/install-mac.sh | bash
```

脚本会自动完成：

1. 检查当前 Mac 是否为 Apple 芯片。
2. 向官网查询当前发布的 macOS 安装包地址。
3. 下载安装包。
4. 解压到临时目录。
5. 将 `共飞-ChatGPT助手.app` 安装到 `/Applications`，替换旧版本。
6. 自动启动客户端。

安装完成后，应用位置是：

```text
/Applications/共飞-ChatGPT助手.app
```

`.tar.gz` 不需要用户手动解压。安装脚本会自动解压，并且会保留 macOS 所需的可执行权限。

## 三、升级

重复执行同一条命令即可升级：

```bash
curl -fsSL https://download.gongfeiai.com/downloads/install-mac.sh | bash
```

脚本每次都会重新向官网查询当前版本，不需要用户记住或替换版本号。升级时脚本会替换：

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
curl -fsSL https://download.gongfeiai.com/downloads/uninstall-mac.sh -o /tmp/uninstall-mac.sh
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

`install-mac.sh` 默认会向官网查询在线地址下载压缩包，不会自动读取同目录下的本地压缩包。

手动运行本地脚本时，可以用 `GONGFEI_DOWNLOAD_URL` 跳过在线查询，直接指定一个公网压缩包地址（本地 `.tar.gz` 文件本身不行，脚本内部是 `curl` 下载，不支持本地路径）：

```bash
GONGFEI_DOWNLOAD_URL="https://download.gongfeiai.com/downloads/codex-relay-client_v0.2_macos-arm64.tar.gz" \
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

在浏览器或终端确认脚本和安装包地址都能访问，且官网设置接口已经返回新值：

```bash
curl -I https://download.gongfeiai.com/downloads/install-mac.sh
curl -I https://download.gongfeiai.com/downloads/uninstall-mac.sh
curl -I https://download.gongfeiai.com/downloads/codex-relay-client_v0.2_macos-arm64.tar.gz
curl -s https://gongfeiai.com/api/v1/settings/public | grep -o '"client_download_direct_url_mac":"[^"]*"'
```

压缩包地址不能返回 HTML 错误页，应该返回实际的 `.tar.gz` 文件。

最小上线清单：

- `install-mac.sh` 文件可读，且里面的下载地址已改成 `download.gongfeiai.com`（旧版本硬编码了从未配置过静态托管的域名，会静默下载到一个 HTML 错误页，报错成"安装包损坏"，误导排查方向）。
- `uninstall-mac.sh` 文件可读。
- 当前版本的压缩包已上传，文件名带版本号即可。
- `/api/v1/settings/public` 返回的 `client_download_direct_url_mac` 指向刚上传的这个文件。
- 压缩包 URL 不需要登录或额外 Cookie。
- HTTPS 证书有效。
- 每次发布新版本：上传新文件 + 后台保存设置（不要用裸 SQL 绕过后台，会撞上前面提到的缓存问题）。
