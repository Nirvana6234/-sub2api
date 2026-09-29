# 主从分流开发的单测基线（2026-09-26）

分支 `feat/master-relay-nodes` 开工前，main（`c652d850`）上后端单元测试的状态。每个工作包提交前的门槛是**不新增失败**：下面这些失败是开工前就存在的，与主从分流无关，不在本分支修。

跑法（Windows 本机）：

```bash
cd backend
go test -tags=unit -count=1 -skip 'TestUserRepositoryCreateSerializesNormalizedEmailConflictsUnderConcurrency' ./...
```

`TestUserRepositoryCreateSerializesNormalizedEmailConflictsUnderConcurrency` 在本机会一直卡到 10 分钟超时，所以跳过。

## 开工前先修好的

- 测试编译不过：`AccountRepository` 加了 `UpdateGroupPriorities`，4 个测试桩没跟上（`stubAccountRepo`、`accountRepoStub`、`mockAccountRepoForPlatform`、`mockAccountRepoForGemini`），已补空实现。
- 测试编译不过：09-21 的后端同步删掉了功能代码，留下了对应测试。已删除这 4 个失效的测试函数：
  - `TestFilterAutoGroupCandidatesForModelHonorsConfiguredModelList`（`GroupModelsListConfig` 已删）
  - `TestEvaluateCodexClientIdentity_HonoursPolicy`、`TestEvaluateCodexClientIdentity_IgnoresForceCodexCLI`（`EvaluateCodexClientIdentity` 已删）
  - `TestSettingService_GetPublicSettings_PaymentBalanceDisabledStrictTrue`（`PaymentBalanceDisabled` 已删）
- `ent/migrate/schema.go` 过期（改了 Ent schema 没重新生成），测试用的 SQLite 表缺列，导致 100 多个测试失败。已重新生成（`go generate ./ent`）；生产走 SQL 迁移，不读这个文件。

## 比对规则

已经失败的测试，新改动可能让它"换一种方式失败"而被掩盖。所以比对时不只看测试名，还要看失败内容：

- `TestConfigKeysAreEnvReachable`：输出里只能列出 `gateway.openai_first_output_hard_cap_seconds` 这一个键。新加配置项必须注册默认值，否则会混进这条失败里。
- `TestAPIContracts`：只能是下面这 7 个子用例失败，每个子用例的差异内容与基线相同。改接口返回字段时，先确认差异没有变多。

## 仍然失败的（基线，不在本分支修）

| 包 | 测试 |
|---|---|
| `internal/config` | `TestConfigKeysAreEnvReachable`（`gateway.openai_first_output_hard_cap_seconds` 没注册默认值） |
| `internal/handler` | `TestOpenAIGatewayHandlerImages_ServerErrorFailsOverAndReturnsClearErrorWhenExhausted` |
| `internal/model` | `TestAllPlatformsIncludesEveryConcretePlatform` |
| `internal/repository` | `TestServerTimingConnectorRecordsDriverCallsWithoutRowLifetime` |
| `internal/server` | `TestAPIContracts` 的 7 个子用例：`GET /api/v1/auth/me`、`POST /api/v1/keys`、`GET /api/v1/keys (paginated)`、`GET /api/v1/groups/available`、`GET /api/v1/usage (paginated)`、`GET /api/v1/admin/settings`、`GET /api/v1/admin/settings falls back to config oauth defaults` |
| `internal/service` | `TestAdminServiceSimpleModeNormalizesAllUnsupportedUpdateFieldsDirectly`、`TestResolveAutoGroupExpiredPendingProbeReloadsPersonalMetricsAndSettlesOnMeasuredGroup` |

### service 包里被 panic 挡住的失败（2026-09-29 补记）

`TestResolveAutoGroup*` 里有用例 panic（`unexpected GetByUserID call`），整个 service 包的测试进程随之退出，排在它后面的用例根本没跑。要看全，跑：

```bash
go test -tags=unit -count=1 -skip 'TestResolveAutoGroup' ./internal/service/
```

合并 main（`19028d5b`）时这样跑的失败（与主从分流无关，基线）：`TestAdminServiceSimpleModeNormalizesAllUnsupportedUpdateFieldsDirectly`、`TestForward_NonCodexClientFallsBackToAuthOnlyPassthrough`、`TestForward_ResetsStrictStateBeforeClientRestrictionRejection`、`TestForward_StrictAppliesToAPIKeyAccountWithoutCodexCLIOnly`、`TestForward_StrictDecisionAppliesAfterNonStrictAttempt`、`TestForward_StrictDecisionDoesNotLeakIntoNonPassthroughAttempt`、`TestGetUserGroupVisibilityEmptyAndErrors`、`TestGetUserGroupVisibilityIncludesActiveSubscriptions`、`TestIsAccountQuotaNotifyEnabled`、`TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort`。

另有一个按时序的用例偶发失败（约一成）：`TestAntigravityGeminiStreamKeepsCommentKeepaliveForOrdinaryClients`（1 秒保活间隔对 1.2 秒空闲，机器忙时抢不到），不算新增失败。

## golangci-lint 基线

本机用 CI 同版本 v2.13.0（`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0`）。

```bash
cd backend
golangci-lint run --max-issues-per-linter=0 --max-same-issues=0 ./...
```

开工时主干上已有 89 条（errcheck 47、unused 23、gofmt 8、ineffassign 6、staticcheck 4、gosec 1），都不在主从分流改动的文件里，不在本分支修。
门槛：`internal/relay/...` 必须 0 条；改动过的其他文件不新增条目。
