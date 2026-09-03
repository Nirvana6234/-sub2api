//! 托管 key 选择规则的测试。
//!
//! 这几条规则的共同点是：**写反了不会报错**，只会安静地跑在错误的分组上、
//! 或者永远续期一把永不过期的 key。所以每一条都要有一个测试盯着。

use codex_host::keylease::{key_name, needs_renewal, pick_current, KeyMeta, PRODUCT};
use codex_host::DeviceIdentity;

const DAY: i64 = 86_400_000;

fn id(install: &str) -> DeviceIdentity {
    DeviceIdentity {
        machine_name: "BORG-PC".to_owned(),
        install_id: install.to_owned(),
    }
}

fn key(id_: i64, name: &str, group: Option<i64>, expires_ms: Option<i64>) -> KeyMeta {
    KeyMeta {
        id: id_,
        name: name.to_owned(),
        group_id: group,
        expires_at_ms: expires_ms,
    }
}

#[test]
fn name_carries_the_group_for_humans_to_read() {
    let me = id("aaa");
    assert_eq!(key_name(&me, None), format!("{PRODUCT}-BORG-PC-aaa"));
    assert_eq!(key_name(&me, Some(7)), format!("{PRODUCT}-BORG-PC-aaa-g7"));
}

/// **分组绑在 key 上**，所以选 key 必须按分组选 —— 这是「不同会话不同分组」的唯一实现方式。
#[test]
fn picks_the_key_belonging_to_the_requested_group() {
    let me = id("aaa");
    let keys = vec![
        key(1, &key_name(&me, Some(1)), Some(1), Some(30 * DAY)),
        key(2, &key_name(&me, Some(2)), Some(2), Some(30 * DAY)),
    ];

    assert_eq!(pick_current(&keys, &me, Some(1)), Some(1));
    assert_eq!(pick_current(&keys, &me, Some(2)), Some(2));
    // 要一个没人有的分组时，宁可一个都不给（让调用方去新建），
    // 也不能把别的分组那把塞过去 —— 那会让会话跑在用户没选的分组上。
    assert_eq!(pick_current(&keys, &me, Some(9)), None);
}

/// 分组归属看**数据**，不看名字 —— 用户随时可能手动改名。
#[test]
fn group_membership_comes_from_the_data_not_the_name() {
    let me = id("aaa");
    // 名字说 g1，数据说分组 2。以数据为准。
    let mislabeled = key(1, &format!("{PRODUCT}-BORG-PC-aaa-g1"), Some(2), Some(30 * DAY));
    let keys = vec![mislabeled];

    assert_eq!(pick_current(&keys, &me, Some(1)), None, "名字骗过了分组判断");
    assert_eq!(pick_current(&keys, &me, Some(2)), Some(1));
}

/// 不指定分组时找的是**绑定为空**的那把，不是随便挑一把。
#[test]
fn no_group_requested_means_the_unbound_key_not_just_any_key() {
    let me = id("aaa");
    let keys = vec![
        key(1, &key_name(&me, Some(5)), Some(5), Some(30 * DAY)),
        key(2, &key_name(&me, None), None, Some(30 * DAY)),
    ];
    assert_eq!(pick_current(&keys, &me, None), Some(2));

    // 只有绑定了分组的时候，「不指定分组」不该退而求其次去用它。
    let only_bound = vec![key(1, &key_name(&me, Some(5)), Some(5), Some(30 * DAY))];
    assert_eq!(pick_current(&only_bound, &me, None), None);
}

/// **反直觉但正确**：没有过期时间的 key 排最后，不是最前。
///
/// 租约模型下「永不过期」是缺陷 —— 授权活得比客户端还久，多半是某次更新把
/// `expires_at` 清掉了。把它当最佳候选，等于收养了模型本身要防的那个东西，
/// 然后永远替它续下去。
#[test]
fn a_key_without_an_expiry_sorts_last_because_it_is_a_defect() {
    let me = id("aaa");
    let name = key_name(&me, Some(1));
    let keys = vec![
        key(1, &name, Some(1), None),               // 永不过期 —— 最差
        key(2, &name, Some(1), Some(10 * DAY)),     // 快到期
        key(3, &name, Some(1), Some(40 * DAY)),     // 最晚到期 —— 最好
    ];
    assert_eq!(pick_current(&keys, &me, Some(1)), Some(3));

    // 只剩「永不过期」那把时才会用它 —— 用完会被续期规则拉回租约模型。
    let only_permanent = vec![key(1, &name, Some(1), None)];
    assert_eq!(pick_current(&only_permanent, &me, Some(1)), Some(1));
}

/// 本次安装的那把无条件优先；没有才放宽到同机旧安装的。
#[test]
fn this_installs_own_key_wins_over_an_older_installs() {
    let me = id("new");
    let older = format!("{PRODUCT}-BORG-PC-old-g1");
    let keys = vec![
        // 旧安装那把到期更晚，但仍然不该盖过本次安装自己的。
        key(1, &older, Some(1), Some(90 * DAY)),
        key(2, &key_name(&me, Some(1)), Some(1), Some(10 * DAY)),
    ];
    assert_eq!(pick_current(&keys, &me, Some(1)), Some(2));

    // 自己没有的时候，旧安装那把是可以认领的。
    let adoptable = vec![key(1, &older, Some(1), Some(90 * DAY))];
    assert_eq!(pick_current(&adoptable, &me, Some(1)), Some(1));
}

/// 别的机器的租约一概不碰 —— 在这台机器上动它，等于把另一台悄悄登出。
#[test]
fn keys_from_other_machines_are_never_touched() {
    let me = id("aaa");
    let keys = vec![key(
        1,
        &format!("{PRODUCT}-SOMEONE-ELSE-bbb-g1"),
        Some(1),
        Some(90 * DAY),
    )];
    assert_eq!(pick_current(&keys, &me, Some(1)), None);
}

/// 和这个产品无关的 key 也一概不碰（用户自己建的那些）。
#[test]
fn unrelated_keys_are_ignored() {
    let me = id("aaa");
    let keys = vec![key(1, "我自己建的 key", Some(1), Some(90 * DAY))];
    assert_eq!(pick_current(&keys, &me, Some(1)), None);
}

#[test]
fn renewal_kicks_in_before_expiry_and_always_for_permanent_keys() {
    let now = 1_000 * DAY;
    let name = "任意";

    // 还早，不用续。
    assert!(!needs_renewal(&key(1, name, None, Some(now + 30 * DAY)), now, 7));
    // 快到了，该续。
    assert!(needs_renewal(&key(1, name, None, Some(now + 3 * DAY)), now, 7));
    // 已经过期，当然要续。
    assert!(needs_renewal(&key(1, name, None, Some(now - DAY)), now, 7));
    // 永不过期 —— 是个要修的状态，也算「要续」，续一次就拉回租约模型。
    assert!(needs_renewal(&key(1, name, None, None), now, 7));
}
