# 验证用例

分三类：**离线**（不连上游，假中转站/单测即可）、**联调**（真上游，消耗额度）、**回归**（证明没开开关的路径一字未变）。

联调用例建议先用一个低价值账号跑，且 `codex_cli_only` 必须已开启（否则 strict 会按设计降级，用例会静默测到普通透传）。

---

## V-0 测试编译基线（**已修复，2026-09-10**）

> **先划清范围：坏的只有测试包，生产构建从未受影响。**
> `go build ./cmd/server` 一直正常（`.local/` 里 9/9–9/10 编出的 5 个二进制均为 153.9MB，与修复后重编的结果一致），后端也能正常启动。缺失的方法全在 `_test.go` 里的测试桩上，而 `go build` 不编译 `_test.go`——只有 `go test` / `go vet` 构建测试二进制时才会撞上。
> 这正是这类腐烂难被发现的原因：日常 build + 启动完全无感，直到有人真去跑测试。

动手前 `internal/service` 与 `internal/server_test` 两个**测试包**编译不过，所有回归门因此是死的。根因是 `DEV_GUIDE.md` 记的那条老坑——加接口方法要更新每一个实现它的 stub/mock——两个接口各加了方法，四个测试桩没跟上：

| 桩 | 文件 | 补的方法 |
|---|---|---|
| `redeemCodeRepoStub` | `internal/service/auth_oauth_email_flow_test.go` | `FindAdminAdjustment` |
| `paymentOrderLifecycleRedeemRepo` | `internal/service/payment_order_lifecycle_test.go` | `FindAdminAdjustment` |
| `stubRedeemCodeRepo` | `internal/server/api_contract_test.go` | `FindAdminAdjustment` |
| `stubUsageLogRepo` | `internal/server/api_contract_test.go` | `GetHeadroomModelStats`、`GetHeadroomTrend`、`FetchPendingLatencyCompensationRows`、`MarkLatencyCompensated`、`UnmarkLatencyCompensated` |

（原以为是「三个文件、一个方法」；实际是**四个桩、两个接口、五个方法**——`payment_fulfillment_test.go` 用的是别处定义的 `redeemCodeRepoStub`，不是独立的桩。）

现状：`go vet -tags=unit ./...` 与 `go vet -tags=integration ./...` 均干净。

**修好编译后暴露出一批既有失败，已于 2026-09-11 全部清掉**（分支 `fix/pre-existing-test-failures`，三个提交）。现状：**`go test -tags=unit ./internal/...` 49 个包全绿，退出码 0。**

其中 **3 条是生产代码的真问题**，不是陈旧断言：

| 问题 | 影响 |
|---|---|
| `groupAllowsContributionPool` / `resolveGroupByID` 未守 `s.groupRepo == nil` | 调度热路径 nil deref；同文件 2096/2332 行早有该守卫，这两处漏了 |
| `OpenAIGatewayService.getSchedulableAccount` 没有 Grok 免费额度软门 | 列表路径会过滤、**粘滞取号不过滤**，越过软门的免费账号只要被会话粘住就能一直复用。Gateway 侧同名方法一直有这道门 |
| `SystemSettings.AccountShareRewardRate` / `AccountOwnUsageFeeRate` **从未被填充** | 管理端显示 0、计费实际按 80/1 执行；保存设置会把 0 落库，分成比例真的归零。两个 Min 都是 0，clamp 救不回来 |

另有 1 处生产缺口属于配置面：group 与 composite-route 的 `oneof` 标签漏了 `kimi`/`zhipu`/`deepseek`，这三个平台在绑定层被拒。

其余是陈旧断言（失败阈值 1 vs 3、缺 `User`/`APIKey`、预热了没人读的那份缓存、插入列表加了两列导致偏移位移）与契约漂移（33 个新增字段，用实际响应重新生成）。

**已知偶发**：`TestAliyunCaptchaVerifier_TransportError` 在一次 `./internal/...` 满载运行中失败过一次，单独跑 5/5、整包 3/3 均绿，与本变更无关，疑似负载敏感。

---

## 关于「跑了多少」的硬约束

下面所有 `go test -run '<pattern>'` 的用例都必须**同时断言实际执行的用例数**。`go test -run` 匹配到零个用例时会打印 `ok` 并返回 0——一个没有分母的门是装饰品。

2026-09-10 编译修复后用 `go test -tags=unit ./internal/service -list '<pattern>'` 实测：

| pattern | 修复编译时 | 阶段 1 后 | 阶段 3+4 后 | 状态 |
|---|---|---|---|---|
| `Passthrough\|Codex` | 587 | 599 | **614** | ok (33.3s) |
| `CodexOAuthTransform\|Fingerprint\|TurnState` | 121 | 121 | 121 | ok (0.6s) |
| `WS\|Websocket` | 279 | 279 | 279 | ok (34.1s) |

**基线只许涨不许跌**——每完成一个阶段把新值写回本表。

断言方式：

```bash
cd backend && go test -tags=unit ./internal/service -run '<pattern>' -count=1 -v 2>&1 | grep -c '^=== RUN'
```

低于基线数即视为门失效（用例被误改名、被 skip、或 pattern 写错），不是「通过」。

---

## A. 离线用例

### V-1 开关三态与降级（对应 design §1.1、§1.2）

| 输入 | 期望 |
|---|---|
| `extra` 无 `openai_passthrough_strict` | `IsOpenAIPassthroughStrictEnabled()==false`，走普通透传 |
| `openai_passthrough=false` + `strict=true` | false（叠加语义：透传关则 strict 无意义） |
| `strict=true` + `codex_cli_only=false` | 降级为普通透传，WARN 日志含 `codex_cli_only_disabled` |
| `strict=true` + `codex_cli_only=true` + `gateway.force_codex_cli=true` | 降级，reason `force_codex_cli_enabled` |
| 三者齐备 | strict 生效 |
| `extra["openai_passthrough_strict"]` 为字符串 `"true"` | false（类型不符按关闭） |

**判据**：零值与错值都必须落在「现状」一侧。

### V-2 请求体逐字段 diff（对应 design §2）

**步骤 0（必做）：先造 fixture。** 2026-09-10 查过，`backend/internal/service/testdata/` 下只有 `ollama_settings_usage.html` 与 `security_monitor_system_prompt.txt`，**没有任何 Codex `/v1/responses` 请求体样本**。所以这条用例目前无米下锅。

录制方式（不花钱、不要 key、不连网）：工作台仓库的 `codex-adapter/scripts/capture-fixtures.py` 用假中转站按剧本吐工具调用，协议那一半仍由真 codex 进程产生。把捕获到的请求体存成 `internal/service/testdata/codex_responses_request.json`。

录制时**必须包含**：非空 `instructions`、数组形态 `input`、`tools`、`client_metadata`（含 `x-codex-window-id` / `x-codex-installation-id`）、`reasoning`。这几项分别是 §2.3 ~ §2.7 各条判据的落点；少任何一项，对应那条断言就变成空转。

拿这份 fixture（真实体约 47KB，`instructions` 约 21KB）分别过 strict 与非 strict 出站构造，断言：

- strict 出站体与输入体**除 `stream` 外逐字段相同，且字段顺序不变**
- 非 strict 出站体额外出现：`store:false`、8 个字段被删（若 fixture 含）
- 两条路径的 `client_metadata` 都被身份影射/指纹收敛改写，且**改写结果一致**

**判据**：这条用例同时证明了「strict 确实少做事」和「strict 没少做身份收敛」。只测前一半是不够的——身份收敛漏做是这个变更最贵的失败模式。

### V-3 store=true 不在本地拦（对应 design §2.2）

strict 下客户端发 `store:true` → 出站体保留 `store:true`，本地不改写、不报错。

**判据**：strict 的语义是「错在调用方就让上游说」。本地静默改写会掩盖客户端的问题。

### V-4 请求头黑名单（对应 design §3）

构造一个带以下头的请求，断言出站：

| 头 | strict 期望 | 非 strict 期望 |
|---|---|---|
| `x-codex-routing-hint` | 到达 | 被吃掉 |
| `x-openai-subagent` | 到达 | 被吃掉 |
| `x-responsesapi-include-timing-metrics` | 到达 | 被吃掉 |
| `x-codex-parent-thread-id` | 到达 | 被吃掉 |
| `session-id`（连字符） | 到达 | 被吃掉（白名单只有下划线形式） |
| `authorization`（客户端伪造） | **不到达** | 不到达 |
| `cookie`（客户端伪造） | **不到达** | 不到达 |
| `chatgpt-account-id`（客户端伪造） | **不到达** | 不到达 |
| `x-codex-installation-id`（客户端原值） | **不到达**（由指纹收敛生成） | 不到达 |
| `x-forwarded-for` | 不到达 | 不到达 |

**判据**：黑名单最容易犯的错是「改成放行后忘了身份头」。表里后五行是这条用例的真正目的。

### V-5 zstd 出站（对应 design §4）

strict 下出站请求：`Content-Encoding: zstd`，body 可被 `zstd` 解出，解出结果与 V-2 断言的明文体逐字节相同，`Content-Length` 等于压缩后长度。

再断言：出站构造之后没有任何路径重读 `req.Body`（可用一个会 panic 的 Body wrapper 验证）。

### V-6 跨模式 failover（对应 design §5）

- strict 账号 A 首选 → 上游 5xx → 换非 strict 账号 B：断言 B 那次 attempt 的 body 走了**完整规范化**（有 `store:false`、8 字段被删），且不是 A 那次派生出的体
- 反方向（B 先 A 后）：断言 A 那次拿到的是干净的 canonical body

**判据**：`Forward` 是逐 attempt 调用的，这条用例锁死「不复用上一 attempt 派生体」这个不变量。

### V-7 计费不变（对应 proposal Impact）

同一份 fixture 走 strict 与非 strict，断言 `parseSSEUsageBytesWithType` 解析出的 input/output token 数、`requestedModel`、`billingModel` 完全相同。

**判据**：本变更对外承诺「计费保持」，这条是验收线。

### V-8 stream:false 下游重组（对应 design §2.1）

strict 下客户端发 `stream:false` → 上游仍收到 `stream:true` → 下游拿到完整 JSON（走 `reconstructResponseOutputFromSSE`），且用量正常记录。

---

## B. 联调用例（真上游，消耗额度）

### V-9 一轮真实 Codex 会话跑通

用真 Codex CLI/app-server 指向本网关的 strict 账号，跑一轮带工具调用的任务。断言：

- `turn/completed` 正常收束
- 工具调用（`exec_command` 或 `shell_command`）真的执行并返回
- 用量记录落库，数值与账号侧额度变化方向一致

### V-10 与非 strict 的上游行为对比

同一账号先关 strict 跑 10 轮、再开 strict 跑 10 轮，对比：

- HTTP 4xx 计数
- 流内 `server_is_overloaded` 出现次数
- TTFB 分布

**判据**：strict 的预期收益之一是出站身份更像官方、被上游降载的概率更低。如果 `server_is_overloaded` 没有下降甚至上升，说明某处身份改写在 strict 下漏了，要回头查 §2.7 / §3。

### V-11 门禁真的拦得住

用一个非官方 UA（例如 `curl/8.0`）打 strict 账号：断言被 `codex_cli_only` 拦在 403，**且没有任何请求到达上游**。

**判据**：strict 的全部合法性建立在这条上。它不通过，整个变更的前提不成立。

---

## C. 回归用例

### V-12 未开开关的账号逐字节不变

对现有 `openai_passthrough=true`（strict 未设）的账号，跑既有全部透传测试，**并断言执行数 ≥ 587**：

```
cd backend && go test -tags=unit ./internal/service -run 'Passthrough|Codex' -count=1
```

再加一条 golden 断言：把 V-2 那份 fixture 在**动手前**过一遍非 strict 出站构造，结果存为 `internal/service/testdata/codex_responses_upstream_auth_only.golden.json`；改完之后同一输入必须逐字节相同。

**判据**：既有测试全绿只证明「没测到的地方没崩」。golden 才证明「出站字节没变」——这是本变更承诺不动现状的唯一硬证据。

### V-13 非透传路径不变（执行数 ≥ 121）

```
cd backend && go test -tags=unit ./internal/service -run 'CodexOAuthTransform|Fingerprint|TurnState' -count=1
```

### V-14 strict 在 WS 路径上是惰性的

**跑既有 WS 测试（执行数 ≥ 279）不算数**——那些用例在 strict 还不存在时就通过，改完也照样通过，无论 strict 有没有漏进 WS。必须新写一条：

> 同一个账号（`openai_passthrough=true` + `strict=true` + `codex_cli_only=true`），请求经 **WS 入站**（`openai_ws_forwarder_payload.go` 那条路）出站时，请求头集合、body 改写结果与把 `strict` 置 false 时**完全相同**。

顺带断言 `openai_ws_http_bridge.go:539` 那条 `c.Set("openai_passthrough", true)` 不会让 `resolveOpenAIStrictPassthrough` 在 WS 路径上返回 true。

**判据**：本变更的 Non-goals 明确不碰 WS。「不碰」需要一条会因为碰了而变红的用例，而不是一条无论如何都绿的用例。

**2026-09-11 结论：已覆盖，但根因比预想的更硬。**
`TestWSBridgeContextNeverStagesStrict`（`openai_passthrough_strict_failover_test.go`）：
未暂存判定的 context + 开着 strict 的账号，出站必须落在 auth_only（无 zstd、无 strict 头增量）。

上面「顺带断言 `c.Set("openai_passthrough", true)`」那句的担心是多余的——
`resolveOpenAIStrictPassthrough` 从来不读那个键。WS 惰性的真正依据是：
WS 用的是 **upgrade 请求自己的 `*gin.Context`**（`openai_gateway_handler.go:3020`
→ `ProxyResponsesWebSocketFromClient`），它永不流经 `Forward`，所以那条路径读到的
永远是「没暂存」，`stagedOpenAIStrictPassthrough` 的 fail-closed 语义直接兜住。
这是结构性的，不是巧合。

同一文件另有三条 failover 用例锁死跨 attempt 的状态生命周期（对应 tasks §4），
其中两条在移除 `Forward` 顶部的复位后实测变红。

### V-15 全量

```
cd backend && go test -tags=unit ./...
cd frontend && pnpm test:run
```

**`pnpm test` 是 watch 模式**（package.json 里 `test` 映射到裸 `vitest`，不退出），
跑一次要用 `pnpm test:run`。初稿写成 `pnpm test`，实测挂了 10 分钟没结束。

`golangci-lint` 这一条**本机跑不了**：本机版本 v2.9 是用 go1.26 构建的，拒绝对
go.mod 声明 1.27.0 的模块运行（"the Go language version used to build golangci-lint
is lower than the targeted Go version"）。CI 上是 v2.13，留给 CI 把关。

**2026-09-11 实测结果**：

| 门 | 结果 |
| --- | --- |
| `go test -tags=unit ./...` | exit 0，58 包 ok / 0 FAIL |
| `pnpm test:run` | 279 文件 / 2084 用例全绿 |
| `vue-tsc --noEmit` | 干净 |
| `golangci-lint` | 本机不可运行，见上 |

**两条已知 flake**（只在 `./...` 全量并行下偶发，单跑与整包重跑均稳定通过）：
`TestAliyunCaptchaVerifier_TransportError`（`internal/repository`）、
`TestFilterGrokFreeQuotaAccountsOnlyBlocksExplicitFreeOAuth`（`internal/service`）。
2026-09-11 各观察到一次；紧接着的全量重跑 58 包 0 失败。遇到时先重跑定性，别当成回归追。

注意统计退出码时别把 `go test` 接管道再读 `$?`——那取到的是管道末端命令的退出码。
