//! 把**每一个**我们会发的方法的参数，对着真二进制导出的 schema 校一遍。
//!
//! # 为什么单有 `replay.rs` 不够
//!
//! `replay.rs` 里那条「拿我们拼的参数比对真实发出去的字节」只能覆盖**录制里恰好出现过**的
//! 方法。没被录到的方法等于没验 —— 而这正是 `turn/interrupt` 漏掉必填的 `turnId`、
//! `thread/list` 把 `limit` 写成 `pageSize` 的原因：两个都编译得过、都跑得起来、
//! 都会被服务端静默拒绝。
//!
//! 这里的检查很浅（必填字段在不在、有没有 schema 不认识的键），但它**覆盖全部方法**，
//! 而且不需要真跑一次调用。深度换广度，抓的是最常犯的两类错。

use std::collections::BTreeSet;
use std::path::PathBuf;

use codex_adapter::{ApprovalPolicy, ClientRequest, SandboxMode, WorkspaceDir};
use serde_json::Value;

fn protocol_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("protocol")
        .join(codex_adapter::PINNED_CODEX_VERSION)
}

fn load_schema(rel: &str) -> Value {
    let path = protocol_dir().join(rel);
    let text = std::fs::read_to_string(&path)
        .unwrap_or_else(|e| panic!("读不到 schema {}: {e}", path.display()));
    serde_json::from_str(&text).unwrap_or_else(|e| panic!("schema {} 不是合法 JSON: {e}", path.display()))
}

/// 校验一个对象是否满足某个对象 schema。返回 `Err(说明)`。
///
/// 只看两件事：`required` 里的键在不在，params 里有没有 `properties` 不认识的键。
fn check_object(params: &Value, schema: &Value) -> Result<(), String> {
    let obj = params.as_object().ok_or("params 不是对象")?;

    let known: BTreeSet<&str> = schema
        .get("properties")
        .and_then(Value::as_object)
        .map(|p| p.keys().map(String::as_str).collect())
        .unwrap_or_default();

    if let Some(required) = schema.get("required").and_then(Value::as_array) {
        for key in required.iter().filter_map(Value::as_str) {
            if !obj.contains_key(key) {
                return Err(format!("缺必填字段 `{key}`"));
            }
        }
    }

    // 没有 properties 的 schema（例如纯 $ref）不做未知键检查，避免误报。
    if !known.is_empty() {
        for key in obj.keys() {
            if !known.contains(key.as_str()) {
                return Err(format!(
                    "schema 不认识的字段 `{key}`（它认识的是 {known:?}）"
                ));
            }
        }
    }

    Ok(())
}

/// 有些 params 是打了 tag 的联合体（如 `account/login/start`）。任一分支满足即可。
fn check(params: &Value, schema: &Value) -> Result<(), String> {
    if let Some(variants) = schema
        .get("oneOf")
        .or_else(|| schema.get("anyOf"))
        .and_then(Value::as_array)
    {
        let mut reasons = Vec::new();
        for variant in variants {
            match check_object(params, variant) {
                Ok(()) => return Ok(()),
                Err(why) => reasons.push(why),
            }
        }
        return Err(format!("没有任何一个分支匹配：{reasons:?}"));
    }
    check_object(params, schema)
}

/// 每个 `ClientRequest` 变体，配上它在 schema 里对应的文件。
///
/// **新增方法时必须在这里加一行** —— 少一行就等于那个方法没人验。
fn all_requests() -> Vec<(ClientRequest, &'static str)> {
    let cwd = WorkspaceDir::new(std::env::temp_dir()).expect("临时目录应当存在");
    vec![
        (
            ClientRequest::Initialize {
                client_name: "cofly-workbench".into(),
                client_version: "0.1.0".into(),
            },
            "v1/InitializeParams.json",
        ),
        (
            ClientRequest::LoginApiKey { api_key: "sk-placeholder".into() },
            "v2/LoginAccountParams.json",
        ),
        (
            ClientRequest::ThreadStart {
                cwd,
                sandbox: SandboxMode::ReadOnly,
                approval_policy: ApprovalPolicy::OnRequest,
            },
            "v2/ThreadStartParams.json",
        ),
        (
            ClientRequest::ThreadResume { thread_id: "t-1".into() },
            "v2/ThreadResumeParams.json",
        ),
        (ClientRequest::ThreadList { limit: Some(50) }, "v2/ThreadListParams.json"),
        (ClientRequest::ThreadList { limit: None }, "v2/ThreadListParams.json"),
        (
            ClientRequest::ThreadArchive { thread_id: "t-1".into() },
            "v2/ThreadArchiveParams.json",
        ),
        (
            ClientRequest::ThreadCompactStart { thread_id: "t-1".into() },
            "v2/ThreadCompactStartParams.json",
        ),
        (
            ClientRequest::TurnStart {
                thread_id: "t-1".into(),
                text: "hi".into(),
                model: None,
                effort: None,
            },
            "v2/TurnStartParams.json",
        ),
        // model/effort 是多会话并发改造新加的——两条路都要过闸门，
        // 免得"有值时"的编码单独漂移了没人发现。
        (
            ClientRequest::TurnStart {
                thread_id: "t-1".into(),
                text: "hi".into(),
                model: Some("gpt-5.5".into()),
                effort: Some("high".into()),
            },
            "v2/TurnStartParams.json",
        ),
        (
            ClientRequest::TurnInterrupt { thread_id: "t-1".into(), turn_id: "turn-1".into() },
            "v2/TurnInterruptParams.json",
        ),
    ]
}

#[test]
fn every_request_we_can_emit_matches_the_pinned_schema() {
    let mut failures = Vec::new();

    for (req, schema_file) in all_requests() {
        let method = req.method();
        let params = req.params();
        let schema = load_schema(schema_file);
        if let Err(why) = check(&params, &schema) {
            failures.push(format!("{method} ({schema_file}): {why}\n  发的是 {params}"));
        }
    }

    assert!(
        failures.is_empty(),
        "有 {} 个方法和 codex {} 的 schema 对不上：\n{}",
        failures.len(),
        codex_adapter::PINNED_CODEX_VERSION,
        failures.join("\n")
    );
}

/// 每个变体都得在 [`all_requests`] 里出现，否则这道闸门会随着新增方法悄悄失效。
#[test]
fn the_gate_covers_every_variant() {
    let covered: BTreeSet<&str> = all_requests().iter().map(|(r, _)| r.method()).collect();
    let expected = [
        "initialize",
        "account/login/start",
        "thread/start",
        "thread/resume",
        "thread/list",
        "thread/archive",
        "thread/compact/start",
        "turn/start",
        "turn/interrupt",
    ];
    for method in expected {
        assert!(covered.contains(method), "{method} 没被这道闸门覆盖");
    }
    assert_eq!(
        covered.len(),
        expected.len(),
        "ClientRequest 加了新方法但没加进 all_requests()：{covered:?}"
    );
}

/// 反过来验一次：故意写错的参数必须被抓住。
///
/// 没有这条，上面那个测试可能因为校验逻辑本身失灵而永远绿。
#[test]
fn the_gate_actually_catches_the_two_mistakes_it_exists_for() {
    let schema = load_schema("v2/TurnInterruptParams.json");
    // 就是 A1 犯的那个错：漏了必填的 turnId。
    let err = check(&serde_json::json!({ "threadId": "t-1" }), &schema).unwrap_err();
    assert!(err.contains("turnId"), "没抓到缺失的必填字段: {err}");

    let schema = load_schema("v2/ThreadListParams.json");
    // 另一个：字段名拼错成了 pageSize。
    let err = check(&serde_json::json!({ "pageSize": 50 }), &schema).unwrap_err();
    assert!(err.contains("pageSize"), "没抓到未知字段: {err}");
}
