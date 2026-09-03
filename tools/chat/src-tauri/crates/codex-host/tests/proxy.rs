//! 本地转发层的验收。
//!
//! 用一个**假中转站**（自己手写 HTTP，不引框架）接住转发出去的请求，
//! 于是可以逐条断言「上游到底收到了什么」—— 这条链路上最要命的几个错
//! （把本地令牌当账号凭据带上去、分组猜错、把流攒成一坨）都只有在这一侧才看得见。

use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use codex_host::proxy::guard::{check, Rejected};
use codex_host::LocalRelay;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

/// 假中转站收到的一条请求。
#[derive(Debug, Clone, Default)]
struct Seen {
    head: String,
    body: String,
}

impl Seen {
    fn header(&self, name: &str) -> Option<String> {
        let needle = format!("{}:", name.to_lowercase());
        self.head
            .lines()
            .find(|line| line.to_lowercase().starts_with(&needle))
            .map(|line| line[needle.len()..].trim().to_owned())
    }
}

/// 起一个假中转站。`chunks` 会被逐块吐出来，块与块之间隔 `gap`。
async fn fake_relay(chunks: Vec<&'static str>, gap: Duration) -> (String, Arc<Mutex<Vec<Seen>>>) {
    let listener = TcpListener::bind(("127.0.0.1", 0)).await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let seen: Arc<Mutex<Vec<Seen>>> = Arc::new(Mutex::new(Vec::new()));
    let sink = Arc::clone(&seen);

    tokio::spawn(async move {
        loop {
            let Ok((mut stream, _)) = listener.accept().await else {
                continue;
            };
            let sink = Arc::clone(&sink);
            let chunks = chunks.clone();
            tokio::spawn(async move {
                let mut raw = Vec::new();
                let mut buf = [0u8; 8192];
                // 读到头部结束，再按 Content-Length 把 body 读完。
                let (head_end, content_length) = loop {
                    let n = match stream.read(&mut buf).await {
                        Ok(0) | Err(_) => return,
                        Ok(n) => n,
                    };
                    raw.extend_from_slice(&buf[..n]);
                    if let Some(pos) = find(&raw, b"\r\n\r\n") {
                        let head = String::from_utf8_lossy(&raw[..pos]).to_string();
                        let len = head
                            .lines()
                            .find(|l| l.to_lowercase().starts_with("content-length:"))
                            .and_then(|l| l.split(':').nth(1))
                            .and_then(|v| v.trim().parse::<usize>().ok())
                            .unwrap_or(0);
                        break (pos + 4, len);
                    }
                };
                while raw.len() < head_end + content_length {
                    let n = match stream.read(&mut buf).await {
                        Ok(0) | Err(_) => break,
                        Ok(n) => n,
                    };
                    raw.extend_from_slice(&buf[..n]);
                }
                sink.lock().unwrap().push(Seen {
                    head: String::from_utf8_lossy(&raw[..head_end - 4]).to_string(),
                    body: String::from_utf8_lossy(&raw[head_end..]).to_string(),
                });

                let _ = stream
                    .write_all(
                        b"HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\n\
                          transfer-encoding: chunked\r\n\r\n",
                    )
                    .await;
                let _ = stream.flush().await;
                for (i, chunk) in chunks.iter().enumerate() {
                    if i > 0 {
                        tokio::time::sleep(gap).await;
                    }
                    let framed = format!("{:x}\r\n{}\r\n", chunk.len(), chunk);
                    if stream.write_all(framed.as_bytes()).await.is_err() {
                        return;
                    }
                    let _ = stream.flush().await;
                }
                let _ = stream.write_all(b"0\r\n\r\n").await;
                let _ = stream.flush().await;
            });
        }
    });

    (format!("http://127.0.0.1:{port}"), seen)
}

fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// codex 真会发的那种载荷（截过）。
const PAYLOAD: &str = r#"{"model":"gpt-5","instructions":"...","tools":[{"type":"function"}],"stream":true}"#;

async fn relay_with_route() -> (LocalRelay, String, Arc<Mutex<Vec<Seen>>>) {
    let (upstream, seen) = fake_relay(vec!["event: x\ndata: 1\n\n"], Duration::ZERO).await;
    let relay = LocalRelay::start(&upstream).await.unwrap();
    relay.set_session_token(Some("jwt-account-session".to_owned())).await;
    relay.bind_thread("thread-a", 7).await;
    let url = format!("{}/responses", relay.base_url());
    (relay, url, seen)
}

fn client() -> reqwest::Client {
    reqwest::Client::builder().build().unwrap()
}

/// 这条是整个转发层存在的理由：**codex 那个本地令牌一个字节都不许流到上游**，
/// 上游看到的必须是账号会话。写反了照样能跑通，只是把 codex 的令牌当凭据发了出去。
#[tokio::test]
async fn the_local_token_never_reaches_the_relay() {
    let (relay, url, seen) = relay_with_route().await;

    let response = client()
        .post(&url)
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    let _ = response.bytes().await;

    let seen = seen.lock().unwrap();
    assert_eq!(seen.len(), 1);
    let request = &seen[0];

    assert_eq!(
        request.header("authorization").as_deref(),
        Some("Bearer jwt-account-session"),
        "上游拿到的必须是账号会话"
    );
    assert!(
        !request.head.contains(relay.token()) && !request.body.contains(relay.token()),
        "本地令牌泄漏到了上游请求里"
    );
    assert!(
        request.head.contains("/api/v1/paw/responses"),
        "打错了后端的路：{}",
        request.head.lines().next().unwrap_or_default()
    );
}

/// 分组按 thread 走，并且以请求头告诉后端 —— 这是「不同会话不同分组」的实现方式。
#[tokio::test]
async fn the_group_comes_from_the_thread_that_sent_the_request() {
    let (upstream, seen) = fake_relay(vec!["data: 1\n\n"], Duration::ZERO).await;
    let relay = LocalRelay::start(&upstream).await.unwrap();
    relay.set_session_token(Some("jwt".to_owned())).await;
    relay.bind_thread("thread-a", 7).await;
    relay.bind_thread("thread-b", 9).await;
    let url = format!("{}/responses", relay.base_url());

    for thread in ["thread-a", "thread-b"] {
        let response = client()
            .post(&url)
            .header("authorization", format!("Bearer {}", relay.token()))
            .header("thread-id", thread)
            .body(PAYLOAD)
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        let _ = response.bytes().await;
    }

    let seen = seen.lock().unwrap();
    assert_eq!(seen[0].header("x-paw-group-id").as_deref(), Some("7"));
    assert_eq!(seen[1].header("x-paw-group-id").as_deref(), Some("9"));
}

/// 报了一个没登记过的 thread，**宁可报错也不拿别的顶上** ——
/// 顶上就意味着这一轮悄悄跑在用户没选的分组上，额度记到别处，两端都没提示。
#[tokio::test]
async fn an_unknown_thread_is_refused_rather_than_routed_somewhere() {
    let (upstream, seen) = fake_relay(vec!["data: 1\n\n"], Duration::ZERO).await;
    let relay = LocalRelay::start(&upstream).await.unwrap();
    relay.set_session_token(Some("jwt".to_owned())).await;
    relay.bind_thread("thread-a", 7).await;
    let url = format!("{}/responses", relay.base_url());

    let response = client()
        .post(&url)
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("thread-id", "never-registered")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();

    assert_eq!(response.status(), 400);
    assert!(seen.lock().unwrap().is_empty(), "不该打到上游去");
}

/// 请求体必须原样送到上游：codex 的载荷里有我们不认识的字段，
/// 少一个就是悄悄改了 agent 的行为，而且不会报错。
#[tokio::test]
async fn the_body_is_forwarded_verbatim() {
    let (relay, url, seen) = relay_with_route().await;
    let response = client()
        .post(&url)
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();
    let _ = response.bytes().await;

    assert_eq!(seen.lock().unwrap()[0].body, PAYLOAD);
}

/// 没登录就明着回 401，而不是带着一个空的 Authorization 打到上游去。
#[tokio::test]
async fn without_an_account_session_nothing_is_forwarded() {
    let (upstream, seen) = fake_relay(vec!["data: 1\n\n"], Duration::ZERO).await;
    let relay = LocalRelay::start(&upstream).await.unwrap();
    relay.bind_thread("thread-a", 7).await;
    let url = format!("{}/responses", relay.base_url());

    let response = client()
        .post(&url)
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();

    assert_eq!(response.status(), 401);
    assert!(seen.lock().unwrap().is_empty());
}

/// **流式**：第一块必须在第二块还没发出来之前就到手。
///
/// 攒完再吐会同时打坏两件事 —— 正文不再逐字出现，而且「停止」要等这一轮跑完才生效。
/// 而这两件事在功能测试里都看不出来，只有掐时间才验得到。
#[tokio::test]
async fn the_response_streams_instead_of_being_buffered() {
    let gap = Duration::from_millis(400);
    let (upstream, _) = fake_relay(vec!["data: one\n\n", "data: two\n\n"], gap).await;
    let relay = LocalRelay::start(&upstream).await.unwrap();
    relay.set_session_token(Some("jwt".to_owned())).await;
    relay.bind_thread("thread-a", 7).await;

    let started = Instant::now();
    let mut response = client()
        .post(format!("{}/responses", relay.base_url()))
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();

    let first = response.chunk().await.unwrap().expect("第一块没到");
    let first_at = started.elapsed();
    assert!(String::from_utf8_lossy(&first).contains("one"));
    assert!(
        first_at < gap,
        "第一块等了 {first_at:?}，比整条流的间隔 {gap:?} 还久 —— 响应被攒起来了"
    );

    let mut rest = Vec::new();
    while let Some(chunk) = response.chunk().await.unwrap() {
        rest.extend_from_slice(&chunk);
    }
    assert!(String::from_utf8_lossy(&rest).contains("two"));
}

/// 令牌不对就进不来 —— 这台机器上别的进程也在同一个回环上。
#[tokio::test]
async fn a_wrong_token_is_refused() {
    let (relay, url, seen) = relay_with_route().await;
    let response = client()
        .post(&url)
        .header("authorization", "Bearer not-the-token")
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();

    assert_eq!(response.status(), 401);
    assert!(seen.lock().unwrap().is_empty());
    let _ = relay;
}

/// 网页发起的请求一律拒。实测 codex 从不发 `Origin`，所以这条不会误伤。
///
/// 网页读不到我们的响应（没有 CORS 头），但**发得出去就已经在花用户的额度了**。
#[tokio::test]
async fn a_request_from_a_web_page_is_refused_even_with_the_right_token() {
    let (relay, url, seen) = relay_with_route().await;
    let response = client()
        .post(&url)
        .header("authorization", format!("Bearer {}", relay.token()))
        .header("origin", "https://evil.example")
        .header("thread-id", "thread-a")
        .body(PAYLOAD)
        .send()
        .await
        .unwrap();

    assert_eq!(response.status(), 403);
    assert!(seen.lock().unwrap().is_empty());
}

/// 闸门逐条 —— 端到端只能验通过与不通过，验不到「是被哪一条挡下来的」。
#[test]
fn every_gate_rejects_for_its_own_reason() {
    const TOKEN: &str = "t0ken";
    const PORT: u16 = 4242;
    let host = format!("127.0.0.1:{PORT}");
    let auth = format!("Bearer {TOKEN}");
    let ok = |method, path, host, origin, auth| {
        check(method, path, host, origin, auth, TOKEN, PORT)
    };

    assert_eq!(
        ok("POST", "/v1/responses", Some(host.as_str()), None, Some(auth.as_str())),
        Ok(())
    );

    // 只有一条路。GET、别的路径、子路径，全都不认。
    for (method, path) in [
        ("GET", "/v1/responses"),
        ("POST", "/v1/models"),
        ("POST", "/v1/responses/cancel"),
        ("POST", "/"),
    ] {
        assert_eq!(
            ok(method, path, Some(host.as_str()), None, Some(auth.as_str())),
            Err(Rejected::NotFound),
            "{method} {path}"
        );
    }

    assert_eq!(
        ok("POST", "/v1/responses", Some(host.as_str()), Some("null"), Some(auth.as_str())),
        Err(Rejected::BrowserOrigin),
        "连 Origin: null 也要挡 —— 沙箱 iframe 发出来的就长这样"
    );

    // DNS 重绑定：域名先解析到真 IP、再改指到 127.0.0.1，同源策略就被绕开了。
    for bad_host in ["evil.example", "localhost:4242", "127.0.0.1:9999"] {
        assert_eq!(
            ok("POST", "/v1/responses", Some(bad_host), None, Some(auth.as_str())),
            Err(Rejected::ForeignHost),
            "{bad_host}"
        );
    }
    assert_eq!(
        ok("POST", "/v1/responses", None, None, Some(auth.as_str())),
        Err(Rejected::ForeignHost)
    );

    for bad_auth in [None, Some(""), Some("Bearer "), Some("Bearer wrong"), Some(TOKEN)] {
        assert_eq!(
            ok("POST", "/v1/responses", Some(host.as_str()), None, bad_auth),
            Err(Rejected::BadToken),
            "{bad_auth:?}"
        );
    }
}
