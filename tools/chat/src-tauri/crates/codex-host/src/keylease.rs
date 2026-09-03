//! 托管 key 的**选择规则** —— 挑哪一把、要不要续期、该叫什么名字。
//!
//! # 为什么这段逻辑在 Rust 而不在前端
//!
//! HTTP 那半留在前端（那里有 JWT 和 401 自动刷新，是已经调好的部分）。但**选哪一把**
//! 这段规则搬到了这里，就一个理由：**前端没有测试运行器**，而这段规则里有好几条
//! 反直觉、写反了还不会报错的判断 ——
//!
//! - 「没有过期时间」的 key 是**最差**候选，不是最好；
//! - 分组要看 `group_id` **数据**，不能看名字里的后缀；
//! - 不指定分组时找的是 `group_id` 为空的那把，不是「随便挑一把」。
//!
//! 每一条写反了，症状都是「某天开始跑在一个用户没选的分组上」或者「永远续期一把
//! 永不过期的 key」—— 都不会抛异常，只会安静地错下去。所以它们必须被测试盯着。
//!
//! # 这里**不碰密钥**
//!
//! 传进来的只有元数据（id、名字、分组、过期时间），**没有 key 本身**。选完返回一个 id，
//! 前端拿 id 去自己那份列表里取值。密钥多经过一层就多一处可能被日志、被崩溃转储带出去。

use serde::{Deserialize, Serialize};

use crate::identity::DeviceIdentity;

/// 产品前缀。**和小白端保持一致是刻意的**：两个端二选一，共用同一把租约，
/// 用户的 key 列表里就不会堆出两条。改这个字符串等于放弃认领已有租约。
pub const PRODUCT: &str = "共飞直连客户端";

/// 一把 key 的元数据 —— **不含密钥本身**。
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct KeyMeta {
    pub id: i64,
    pub name: String,
    /// 绑定的分组。`None` 表示「服务端按默认决定」。
    #[serde(default)]
    pub group_id: Option<i64>,
    /// 过期时间（毫秒时间戳）。**`None` 表示永不过期 —— 在租约模型下这是缺陷，不是优点。**
    ///
    /// 由前端 `Date.parse()` 解析好再传进来：这样这一层不必引日期库，
    /// 也不必猜 RFC3339 的各种偏移写法。
    #[serde(default)]
    pub expires_at_ms: Option<i64>,
}

/// 这次安装在某个分组下应当使用的 key 名字。
///
/// 分组后缀是**给人看的** —— 用户在自己的列表里能一眼看出哪把对应哪个分组。
/// 程序判断分组只看 [`KeyMeta::group_id`]，不看名字（用户随时可能手动改名）。
pub fn key_name(id: &DeviceIdentity, group_id: Option<i64>) -> String {
    let base = format!("{PRODUCT}-{}-{}", id.machine_name, id.install_id);
    match group_id {
        Some(g) => format!("{base}-g{g}"),
        None => base,
    }
}

/// 这台机器上、这个产品建的所有 key 共享的前缀。
pub fn machine_prefix(id: &DeviceIdentity) -> String {
    format!("{PRODUCT}-{}-", id.machine_name)
}

/// 挑出该用的那把，返回它的 id。
///
/// 三层筛选，**顺序不能换**：
///
/// 1. **同机**（名字前缀）—— 别去碰别的机器的租约。
/// 2. **同分组**（`group_id` 数据）。`group_id` 为 `None` 时找的是绑定为空的那把
///    （「服务端你决定」），**不是随便挑一把** —— 随便挑会让会话跑在一个用户没选的分组上。
/// 3. **本次安装的优先**；没有才放宽到同机旧安装留下的（可以认领，但绝不能盖过
///    本次安装要用的名字）。
///
/// 排序里那条「有没有过期时间」是要点：**没有的排最后**。租约模型下「永不过期」是
/// 缺陷（授权活得比客户端还久，多半是某次更新把 `expires_at` 清掉了），
/// 把它当最佳候选等于收养了模型本身要防的那个东西，然后永远替它续下去。
pub fn pick_current(
    keys: &[KeyMeta],
    identity: &DeviceIdentity,
    group_id: Option<i64>,
) -> Option<i64> {
    let prefix = machine_prefix(identity);
    let mine = key_name(identity, group_id);

    let candidates: Vec<&KeyMeta> = keys
        .iter()
        .filter(|k| k.name.starts_with(&prefix) && k.group_id == group_id)
        .collect();

    fn best<'a>(list: impl Iterator<Item = &'a &'a KeyMeta>) -> Option<i64> {
        list.max_by(|a, b| {
            // 有过期时间的胜过没有的；同类里到期晚的胜。
            let a_has = a.expires_at_ms.is_some();
            let b_has = b.expires_at_ms.is_some();
            a_has
                .cmp(&b_has)
                .then_with(|| a.expires_at_ms.unwrap_or(i64::MIN).cmp(&b.expires_at_ms.unwrap_or(i64::MIN)))
        })
        .map(|k| k.id)
    }

    let own: Vec<&KeyMeta> = candidates.iter().filter(|k| k.name == mine).copied().collect();
    best(own.iter()).or_else(|| best(candidates.iter()))
}

/// 该不该续期。
///
/// **没有过期时间也算「要续」** —— 那是个要修的状态（见 [`pick_current`]），
/// 续一次就把它拉回租约模型里。
pub fn needs_renewal(key: &KeyMeta, now_ms: i64, renew_when_days_left: i64) -> bool {
    match key.expires_at_ms {
        None => true,
        Some(at) => at - now_ms < renew_when_days_left * 86_400_000,
    }
}
