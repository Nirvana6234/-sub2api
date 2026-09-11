# add-openai-codex-strict-passthrough

在现有 OpenAI 透传（`extra.openai_passthrough`，"仅替换认证"）之上，新增一个叠加的**严格字节保真透传**开关 `extra.openai_passthrough_strict`：门禁成立时不再对 Codex 请求做那些为非官方客户端兜底的规范化，把请求体与请求头按官方 Codex 的形状原样送到上游。

参照实现：`zyycn/codex-proxy-rs`（Rust/axum，Apache-2.0）。它的透明是用客户端门禁换来的——不收 `/v1/chat/completions`、客户端版本不够回 426。本变更把同一笔买卖搬到 sub2api 上：sub2api 已有等价门禁（`codex_cli_only` + 引擎指纹门 + 版本门），缺的只是"门禁成立时不做兜底"的开关。

阅读顺序：`proposal.md` → `design.md`（改动点与每处判据）→ `tasks.md` → `verification.md`。
