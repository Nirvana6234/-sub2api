-- accounts.rate_multiplier_undeclared：让"从未声明过上游成本"成为一个显式、
-- 可判定的事实，把它与"声明成本为 1.0"区分开。
--
-- 背景（一次真实生产故障）：某 anthropic 分组售价倍率 0.1、profit_min_margin
-- 与 profit_safety_buffer 均为 0，利润门阈值因此是 0.1；组内三个中转账号从建号
-- 起 rate_multiplier 就是本列的默认值 1.0、从没有人维护过，运营只在账号名上标
-- 了 "0.04x"。利润门把这个默认值当成"运营者声明该上游成本为 1.0"，判定
-- 1.0 > 0.1 越线，把整池账号全部排除，6 小时内 1153 次请求返回
-- no available accounts。
--
-- 根因不是那三个账号填错了值，而是 rate_multiplier 的默认值要同时满足两个
-- 冲突的需求：
--   * 计费口径需要"未配置时按 1.0 不缩放"——1.0 是中性值；
--   * 利润准入需要"未配置时能被识别为未配置"——1.0 是最贵的值，是最危险的
--     默认，且与运营者真的声明 1.0 在库里完全同形、不可区分。
--
-- 为什么不是把 rate_multiplier 改成可空：领域层 service.Account.RateMultiplier
-- 已是 *float64，而 nil 当前有一个被测试钉死的确切含义——
-- scheduler_cache_rate_multiplier_unit_test.go 写明"DB 列有 Default(1.0) 且非
-- nillable，因此 nil 只可能来自缓存反序列化缺字段"，利润门据此对 nil 保守拒绝
-- （fail-closed）。一旦本列可空，nil 就同时表示"未声明"与"快照漏字段"，利润门
-- 无法区分，而漏字段的后果会从保守拒绝翻转成静默放行：利润门失效且无人察觉。
-- 用独立字段承载"未声明"，两个事实各自有各自的编码，互不干扰。
--
-- 为什么用否定式命名：使零值落在安全的一侧。布尔字段缺省为 false，快照结构
-- 调整漏列这一项时账号被视为"已声明"，利润门照常严格判定，安全姿态不变；只有
-- 明确写入 true 才表示"没人声明过"。若命名为 declared（正向），漏列会让所有
-- 账号变成"未声明"而被整体放行——那正是本迁移要避免的静默失效。
--
-- 兼容性：存量账号一律 false（视为已声明），准入与计费行为逐行不变，无需停机。
-- 此后由应用层在"创建账号时未显式提供倍率"的路径上写入 true，并在运营填写倍率
-- 时置回 false。
--
-- 回滚：DROP COLUMN 后所有账号回到"已声明"语义，即恢复本次故障的形态
-- （未维护的 1.0 会重新被当成成本声明而否决）。
--   ALTER TABLE accounts DROP COLUMN IF EXISTS rate_multiplier_undeclared;

ALTER TABLE accounts
  ADD COLUMN IF NOT EXISTS rate_multiplier_undeclared BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN accounts.rate_multiplier_undeclared IS
    'true 表示从未声明过该账号的上游成本倍率：利润准入无判定依据，放行并告警；false（默认，含全部存量账号）表示 rate_multiplier 是运营者的明确声明，参与严格判定';
