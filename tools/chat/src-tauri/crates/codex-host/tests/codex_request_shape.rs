//! 用**真 codex 录下来的那条报文**验转发层的闸门。
//!
//! 前面那组测试是我们自己造的请求 —— 它们证明闸门按设计工作，但证明不了
//! 「设计对得上 codex 的实际行为」。这两件事是分开的，而且第二件出问题时的表现
//! 特别难查：agent 起得来、会话建得起来、只是**每一轮都 404 或 403**，
//! 而错误来自我们自己的转发层，不是上游。
//!
//! 报文由 `scripts/probe-local-proxy.py` 录制（codex-cli 0.144.2），
//! 机器标识与令牌已抹掉。**升级 codex 之后要重录一次**：路径、Host 形式、
//! 有没有多出 Origin，都属于类型检查抓不到的语义漂移。

use codex_host::proxy::guard::{check, Rejected};

#[derive(serde::Deserialize)]
struct Recorded {
    #[serde(rename = "codexVersion")]
    codex_version: String,
    method: String,
    path: String,
    headers: std::collections::HashMap<String, String>,
    #[serde(rename = "bodyTopLevelKeys")]
    body_top_level_keys: Vec<String>,
}

fn recorded() -> Recorded {
    let raw = include_str!("fixtures/codex-http-request.json");
    serde_json::from_str(raw).expect("fixture 不是预期的形状")
}

const TOKEN: &str = "the-local-token";

/// codex 真发出来的那条请求，必须能通过全部四道闸。
#[test]
fn the_real_codex_request_passes_every_gate() {
    let r = recorded();
    let host = r.headers.get("host").expect("录到的报文没有 host 头");
    let port: u16 = host
        .rsplit(':')
        .next()
        .and_then(|p| p.parse().ok())
        .expect("host 里解不出端口");
    let auth = format!("Bearer {TOKEN}");

    assert_eq!(
        check(
            &r.method,
            &r.path,
            Some(host.as_str()),
            r.headers.get("origin").map(String::as_str),
            Some(auth.as_str()),
            TOKEN,
            port,
        ),
        Ok(()),
        "codex {} 发的请求被我们自己的闸门挡下来了",
        r.codex_version
    );
}

/// 闸门里写死的那条路径，得就是 codex 真会打的那条。
///
/// `base_url` 我们给的是 `.../v1`，codex 自己追加 `/responses` —— 这是实测的，
/// 不是约定的。它要是改成追加别的（比如加上 `/backend-api/codex` 前缀），
/// 我们只会看到一路 404。
#[test]
fn codex_appends_exactly_the_path_the_relay_serves() {
    let r = recorded();
    assert_eq!(r.method, "POST");
    assert_eq!(r.path, "/v1/responses");
}

/// codex **不发 `Origin`** —— 「带 Origin 的一律拒」这道闸能设死，全靠这一条。
///
/// 哪天它开始发了，这条会先红；否则第一个发现的人会是「agent 全线 403」的用户。
#[test]
fn codex_sends_no_origin_header_so_the_browser_gate_can_stay_strict() {
    let r = recorded();
    assert!(
        !r.headers.contains_key("origin"),
        "codex 开始发 Origin 了：浏览器那道闸会把它自己挡在外面"
    );
}

/// 分组路由整个押在 `thread-id` 头上。它要是没了，转发层就不知道这一条属于谁，
/// 而"猜一个"正是我们明确拒绝做的事 —— 于是会话会直接停摆。
#[test]
fn codex_reports_the_thread_on_every_request() {
    let r = recorded();
    let thread = r
        .headers
        .get("thread-id")
        .expect("thread-id 没了：分组路由没有依据了");
    assert!(!thread.is_empty());

    // 同一个 id 也在 turn-metadata 里 —— 有个第二来源，万一头没了还有得救。
    let meta = r
        .headers
        .get("x-codex-turn-metadata")
        .expect("turn-metadata 没了");
    assert!(meta.contains(thread), "两处的 thread id 对不上");
}

/// 请求体是一份完整的 Responses 载荷，不是我们能重拼的东西 ——
/// 这条把「原样透传」这个决定钉在实测的字段规模上。
#[test]
fn the_body_is_a_full_responses_payload_we_have_no_business_rebuilding() {
    let r = recorded();
    for required in ["model", "input", "instructions", "tools", "stream"] {
        assert!(
            r.body_top_level_keys.iter().any(|k| k == required),
            "载荷里少了 {required}"
        );
    }
}

/// 闸门不认的那些走法，用真报文的头去试也照样不认 ——
/// 免得「真请求能过」是因为闸门其实什么都没拦。
#[test]
fn the_gates_still_bite_when_fed_the_real_headers() {
    let r = recorded();
    let host = r.headers.get("host").unwrap();
    let port: u16 = host.rsplit(':').next().unwrap().parse().unwrap();
    let auth = format!("Bearer {TOKEN}");

    assert_eq!(
        check("POST", &r.path, Some(host), Some("https://evil.example"), Some(&auth), TOKEN, port),
        Err(Rejected::BrowserOrigin)
    );
    assert_eq!(
        check("POST", &r.path, Some("evil.example"), None, Some(&auth), TOKEN, port),
        Err(Rejected::ForeignHost)
    );
    assert_eq!(
        check("POST", &r.path, Some(host), None, Some("Bearer nope"), TOKEN, port),
        Err(Rejected::BadToken)
    );
}
