ChatGPT desktop installer directory
共飞-ChatGPT助手 —— ChatGPT 桌面版安装包目录

You normally do NOT need this directory.
正常情况下你不需要用到这个目录。

The client downloads the ChatGPT desktop installer automatically when it is
needed, and shows the download progress on the "启动 ChatGPT" button.
客户端会在需要时自动下载 ChatGPT 桌面版安装包，下载进度会显示在
"启动 ChatGPT" 按钮上。

This directory only matters if the automatic download cannot work — for
example on an offline machine, or when the download keeps failing. In that
case:
只有当自动下载走不通时（例如离线的机器，或下载反复失败），才需要用到这个
目录。这种情况下：

  1. Download the official ChatGPT desktop installer yourself.
     自行下载官方 ChatGPT 桌面版安装包。
  2. Copy it into this directory.
     把它复制到本目录。
  3. Go back to the client and click "启动 ChatGPT".
     回到客户端，点击"启动 ChatGPT"。

The client prefers a package found here over downloading, so a file placed in
this directory always wins.
客户端会优先使用本目录中已有的安装包，不再联网下载。

Supported file types / 支持的文件类型:
  .exe
  .msi
  .msix
  .appx
  .msixbundle
  .appxbundle

If more than one supported package is present, the newest file is used.
如果目录中有多个安装包，客户端使用修改时间最新的那个。
