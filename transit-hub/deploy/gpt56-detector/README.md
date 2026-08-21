# gpt56-detector 旁路服务

把 GPT-5.6 混用检测器包成一个只在内网监听的 HTTP 服务，供 transithub 的
`purity_check` 模块调用。

## 上游来源与许可（先读这一节）

Required Notice: Copyright 2026 chen-006 and contributors.
Original project: https://github.com/chen-006/gpt56_api_detector

`vendor/` 是上游发行版 **v4.1.1** 的逐字节副本，许可证原文见 `vendor/LICENSE`
（PolyForm Noncommercial License 1.0.0），中文边界说明见
`vendor/NONCOMMERCIAL_NOTICE_CN.md`。

校验 vendor 未被改动：

```bash
cd vendor && tr -d '\r' < SHA256SUMS.txt > /tmp/sums.txt && sha256sum -c /tmp/sums.txt
```

应输出 35 个 `OK`，没有 FAILED。

### 本目录相对上游的修改

按许可证「必须清楚说明修改内容」的要求，逐条列出：

1. `vendor/` 内**没有任何修改**，与上游发行版一致（见上面的校验命令）。
2. 新增 `serve.py`：上游 `gpt56_vnext_web.py` 调用的 `create_server()` 把监听地址
   写死为 `127.0.0.1`，容器内绑回环则同网络的 transithub 连不上。`serve.py` 改用
   上游 `__all__` 公开的 `AppServer` / `AppState` 自行组装服务器，只把绑定地址
   换成环境变量可配。检测逻辑、评分、阈值、基线一行未动。
3. 新增 `Dockerfile`：Python 3.13 + Node（`native_codex` 请求格式需要），
   无 pip / npm 依赖。

修改版由本仓库运营，与原作者无关，也不代表原作者背书。

### ⚠️ 商用边界（未解决）

PolyForm Noncommercial 明确把「作为付费服务的一部分」「用于企业内部的预期
商业应用」「为商业中转引流」排除在授权之外。sub2api 是收费中转，transithub 是
它的内部运维台——**在这里跑这个检测器，落在被排除的范围内。**

上线前需要二选一：

- 联系原作者取得书面商业授权；或
- 确认本用途不构成商业使用（由决策人书面判断并留痕）。

在此之前，本服务只应在测试环境验证，不要接入生产运维流程。

## 运行

镜像自带的环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `GPT56_BIND_HOST` | `0.0.0.0` | 监听地址 |
| `GPT56_BIND_PORT` | `8760` | 监听端口 |
| `GPT56_RUNS_ROOT` | `/data/runs` | 会话 SQLite 与报告落盘根目录，需挂卷 |
| `GPT56_NODE` | 空 | node 可执行路径，留空则 `which node` |
| `GPT56_USER_AGENT` | 空 | 覆盖普通请求的 UA（不影响原生 Codex 请求） |

构建与启动（在 `transit-hub/deploy` 下）：

```bash
docker compose -f docker-compose.prod.yml build gpt56-detector
```

**不要给它加 `ports:` 映射。** 这个服务只有一层随机 session token，没有任何
业务鉴权，暴露到公网等于把「拿我们的上游 key 打请求」的能力送出去。

## HTTP 契约（transithub 侧照此实现）

鉴权：token 在进程启动时由 `secrets.token_urlsafe(32)` 随机生成，先
`GET /api/bootstrap` 取 `session_token`，后续请求带 `X-GPT56-Session` 头。
容器重启后 token 会变，调用方收到 403 要重新 bootstrap。

服务端还有一条 Origin 校验：**只在请求带了 Origin 头时**才要求 hostname 是
`127.0.0.1`/`localhost`。Go 客户端不发 Origin 头即可，不需要伪造。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 健康检查，无需 token |
| GET | `/api/bootstrap` | 取 `session_token` 与档位预设 |
| POST | `/api/detector/estimate` | 预估请求数 |
| POST | `/api/detector/start` | 开始检测 |
| GET | `/api/detector/status` | 轮询状态与进度 |
| GET | `/api/detector/report` | 取报告 |
| POST | `/api/detector/stop` | 停止 |

`POST /api/detector/start` 请求体：

```jsonc
{
  "base_url": "https://example.com/v1",   // 必填，缺路径时服务端自动补 /v1
  "api_key": "sk-...",                    // 必填，不落库、不写日志
  "claimed_model": "gpt-5.6-sol",         // 只能是 sol / terra / luna
  "request_model": "gpt-5.6-sol",         // 中转别名，默认同 claimed_model
  "config": {
    "mode": "single",                     // single | continuous
    "base_preset": "low",                 // low(19条) | medium(49条) | high(158条)
    "workers": 8,                         // 1-32，档位默认 8
    "retries": 2                          // 0-2
  }
}
```

响应：`{"started": true, "session_id": "...", "official": true, "config_hash": "..."}`。

**同一时刻只能有一个会话。** 状态是 `running` 或 `stopping` 时再 start 会报
`detector is already running or stopping`——所以 transithub 侧必须串行排队。

`GET /api/detector/status` 的 `status` 取值：`idle` / `running` / `stopping` /
`interrupted` / `complete` / `stopped` / `error`。运行中额外带 `progress`，进度是
`progress.logical_completed / progress.planned`（不是 completed/total）。

报告里的结论字段：`overall_verdict`、`outcome_code`、`juice_verdict_state`、
`fingerprint_model`、`fingerprint_verdict_state`、`fingerprint_claim_mismatch`、
`possible_models[]{model, match, score, threshold}`，以及
`auth_values_persisted: false`。

## API key 会不会泄露

上游源码层面的证据（用于回答审计问题）：

- 写 SQLite 的 `save_session` 不接收 `api_key` 参数（`store.py`）；
- 报告只写 `candidate_configuration_without_key`，并标 `auth_values_persisted: false`（`detector.py`）；
- 留存功能会剥掉 `authorization` / `x-api-key` 等头，并对 `sk-` 做模式脱敏（`retention.py`）；
- HTTP 访问日志被禁用，异常消息对 Bearer / `sk-` 做替换（`server.py`）。

留存（retention）默认关闭，transithub 侧也不要开——它会把完整请求/响应正文写盘。
