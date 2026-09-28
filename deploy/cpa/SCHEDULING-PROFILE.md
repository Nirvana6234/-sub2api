# CPA 调度配置读取接口

S2 提供 `GET /api/v1/internal/cpa/scheduling-profile`。请求使用
`Authorization: Bearer <专用同步凭据>`，响应沿用 S2 的 `code/message/data` 格式。
该凭据仅被这个只读接口接受，不授予管理或模型调用权限。

生产配置使用环境变量 `CPA_SCHEDULING_SYNC_TOKEN` 和
`CPA_SCHEDULING_SOURCE_ID`，对应 `cpa_scheduling.sync_token` 与
`cpa_scheduling.source_id`。同步凭据至少 32 个字符，通过部署系统注入；不得提交到
版本库或打印到日志。Source ID 使用稳定、非空且不超过 128 个字符的部署标识，
更换机器时保留，不使用进程启动时间或随启动变化的主机地址。
未配置同步凭据时接口拒绝所有请求。

`data.schema_version=1`。`revision` 是删除 `revision` 和 `generated_at` 后的完整
data 对象的 SHA256 十六进制摘要。摘要前使用标准 Go JSON 的递归排序键编码，
保留整数精度；接收方应使用 `json.Decoder.UseNumber()` 后再序列化校验。
账号按 `source_account_id` 升序，分组按 ID 升序，空集合输出 `[]`。
响应带 `Cache-Control: no-store` 和对应 revision 的 ETag。

配置按作用范围输出：

- `scheduler`：正在运行的 S2 调度开关、最终 TopK、10 个最终权重、粘性策略、
  EWMA 规则及延迟感知回退参数。高级调度开启时应用数据库覆盖值，否则使用已加载
  配置。EWMA 的 `ewma_ttl_seconds=0` 表示 S2 未设置过期；延迟分位统计独立维护
  20 个样本、normal/high 两类推理强度和样本有效期。
- `waiting`：S2 等待上限按账号生效，分别给出粘性等待和普通回退等待；超时统一用
  毫秒。`concurrency_slot_ttl_seconds` 是 S2 分布式槽位的过期参数，并不要求
  CPA 为活跃长请求提前释放槽位。
- `retry`：换号次数不含首次调用；同账号重试和 WebSocket 重连单独列出。
  这些是 S2 的策略参数，不是“各层各自都重试这么多次”的授权。CPA 与 S2 必须
  协调同一个请求的实际预算。错误类型的 deadline、专有协议恢复和已发送流式数据
  仍然限制能否重试。
- `cooldown`：S2 实际解析后的 529 和无上游重置时间时的 429 冷却策略，时长用毫秒。
- `provider_creation_defaults`：普通账号创建界面默认并发 10，Grok 默认 1；
  `scope=new_accounts_only`，不是 Plus/Pro 官方限额，也不覆盖现有账号配置。
- `accounts`：完整非删除账号配置，包括禁用账号；不受列表页分页大小限制。
  只输出显式字段，禁止返回整个 Credentials、Extra、代理、密钥或 OAuth token。

账号 `concurrency<=0` 的 S2 语义是不限并发。负载分母独立计算为正数 `load_factor`，
否则正数 `concurrency`，否则 1。账号的 `pool_mode_retry_status_codes` 总是解析后的
数组：未配置时为 `[401,403,429]`，显式 `[]` 表示禁用；是否适用还需结合
`is_pool_mode` 和请求错误类型。

账号绑定以 `source_instance_id + source_account_id` 标识，再核对平台、类型和稳定
身份。`oauth_identity_matchable` 仅在具有稳定身份、不是 shadow/池包装的 OAuth
账号上为 true；重复身份仍需接收端拒绝自动选择，不能按名字或订阅标签猜测。
shadow 的 `parent_account_id` 和 `quota_dimension` 显式输出，避免误用母账号配置。
API Key 包装上游的并发不能直接作为 CPA 池中各真实账号的并发。

`group_ids/group_priorities`、`subscription_priority_eligible`、`reset_window_end`、
`quota_headroom_factor`、`upstream_cost_rate` 是来源账号的配置或观察值。CPA 必须先
完成显式分组与账号绑定，再应用适用策略；这些字段不授予新的 Key 分组权限，也
不自动创建跨池回退。S2 的低价/延迟跨组回退规则仍需单独的授权映射。

配置读取不复制实时负载。CPA 真实账号的请求数、等待数和健康统计由 CPA 记录，
S2 看见的 CPA 上游聚合负载不等同内部账号负载。读取失败返回 503；接收端应保留
最后一次完整、校验通过的配置并显示同步状态，不能把失败或字段缺失当成放宽权限。
