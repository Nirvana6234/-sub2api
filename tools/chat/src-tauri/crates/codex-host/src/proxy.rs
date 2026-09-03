//! **codex 看得见的唯一一个网络地址**：一个只听回环、只认一条路径的转发层。
//!
//! # 为什么要多这一跳
//!
//! 原先的做法是把中转站地址和一把真 API key 直接交给 codex。两个后果：
//!
//! 1. **那把 key 会被 codex 写进 `auth.json`**（实测，明文）。宿主再怎么小心，
//!    凭据也已经出了我们的手。
//! 2. **分组被绑死在 key 上** —— 网关那条路没有按请求选分组的入口，于是
//!    「不同会话用不同分组」只能靠换 key，换 key 又会牵动同时在用它的另一个端。
//!
//! 加这一跳之后，codex 拿到的是 `http://127.0.0.1:<随机端口>/v1` 加一个
//! **随机本地令牌**。令牌照样会落进 `auth.json`，但它离开这台机器就一文不值，
//! 而真正的账号会话（JWT）从头到尾没进过 codex 的地址空间。
//!
//! # 分组回到「按请求选」
//!
//! 实测 codex **每个请求都自报 `thread-id` 头**。转发层按它查出这一条属于哪个分组，
//! 再以 `X-Paw-Group-Id` 告诉后端。于是一个 codex 进程就能让不同会话走不同分组 ——
//! 不必一个会话起一个进程，也不必去改任何一把 key 的分组。
//!
//! # 威胁模型：**回环不等于私有**
//!
//! 这台机器上任何一个进程、以及用户浏览器里任何一个网页，都能往这个端口发请求。
//! 它们读不到响应（没有 CORS 头），但**发得出去就已经在花钱了**。所以令牌之外还有
//! 三道闸，每一道挡的都是一类具体的走法，见 [`guard`] 里逐条的说明。

use std::collections::HashMap;
use std::convert::Infallible;
use std::net::SocketAddr;
use std::sync::Arc;

use bytes::Bytes;
use futures_util::TryStreamExt;
use http_body_util::{combinators::BoxBody, BodyExt, Full, StreamBody};
use hyper::body::{Frame, Incoming};
use hyper::service::service_fn;
use hyper::{Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use tokio::net::TcpListener;
use tokio::sync::{oneshot, RwLock};

use crate::HostError;

/// codex 会打的那一条路径 —— `base_url` 是 `.../v1`，它自己追加 `/responses`（实测）。
const CODEX_PATH: &str = "/v1/responses";

/// 后端那条只认账号会话的路。
///
/// **这两个常量在 Go 那侧有一份副本**（`backend/internal/server/routes/paw.go`
/// 的 `PawGroupHeader` 与路由注册），两边靠肉眼对齐 —— 改名的话两边测试都还是
/// 绿的、而产品 404。所以两边各钉一次**字面量**：这侧在 `tests/proxy.rs`，
/// 那侧在 `TestPawResponsesRouteWireContractMatchesTheRustRelay`。
const UPSTREAM_PATH: &str = "/api/v1/paw/responses";

/// 分组走请求头，因为请求体必须原样是一份 Responses 载荷。
/// 同上：改它就要同步改 Go 那侧的 `PawGroupHeader`。
const GROUP_HEADER: &str = "X-Paw-Group-Id";

type ProxyBody = BoxBody<Bytes, std::io::Error>;

/// 一条 thread 绑到哪个分组。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ThreadRoute {
    pub group_id: i64,
}

struct RelayState {
    /// 交给 codex 的本地令牌。**不是**账号凭据。
    token: String,
    /// 中转站根地址，例如 `https://relay.example.com`。
    upstream_base: String,
    /// 当前账号会话。**前端是它唯一的持有者**，刷新之后推下来一次新的；
    /// 这里从不自己去刷新 —— 那会让刷新逻辑在两处各存一份，迟早对不上。
    session_token: RwLock<Option<String>>,
    /// 登录那次浏览器请求带的 `User-Agent`，原样转发给上游。
    ///
    /// **不是白捡的头，是拆出来的一个真故障**：账号会话的合法性绑着一个
    /// 「IP + UA」指纹（`enforceSessionBinding`），指纹在登录时随 access token
    /// 一起签发。登录走的是浏览器直连后端，UA 是浏览器的；这条转发走的是
    /// `reqwest`，默认 UA 是 `reqwest/x.y.z`——同一个 token，两种指纹，后端把
    /// 第二次判成会话被搬到了别的网络环境，直接 401 `SESSION_BINDING_MISMATCH`
    /// 并撤销整个 token family。IP 两边都是回环，对得上，UA 对不上就是全部原因。
    client_user_agent: String,
    routes: RwLock<HashMap<String, ThreadRoute>>,
    client: reqwest::Client,
    port: u16,
}

/// 转发层的句柄。丢掉它就等于关掉这个端口。
pub struct LocalRelay {
    addr: SocketAddr,
    state: Arc<RelayState>,
    shutdown: Option<oneshot::Sender<()>>,
}

impl LocalRelay {
    /// 在 `127.0.0.1` 的一个随机端口上起来。
    ///
    /// `client_user_agent` 必须是登录那次浏览器请求的 `User-Agent`——它决定了
    /// 转发出去的请求能不能通过后端的会话指纹校验，见 [`RelayState::client_user_agent`]。
    ///
    /// # Errors
    /// 绑不上端口、或生不出令牌时返回。
    pub async fn start(
        upstream_base: impl Into<String>,
        client_user_agent: impl Into<String>,
    ) -> Result<Self, HostError> {
        // 只听回环。写死而不是配置项：这个值一旦变成 0.0.0.0，
        // 局域网里任何人都能拿这台机器的额度跑 agent，而且从外面看不出来。
        let listener = TcpListener::bind(("127.0.0.1", 0))
            .await
            .map_err(|e| HostError::Proxy(format!("绑不上回环端口: {e}")))?;
        let addr = listener
            .local_addr()
            .map_err(|e| HostError::Proxy(format!("取不到本地端口: {e}")))?;

        let client = reqwest::Client::builder()
            // 转发层不该自作主张跟随跳转：那会把带着账号凭据的请求送到别处去。
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|e| HostError::Proxy(format!("建不出 HTTP 客户端: {e}")))?;

        let state = Arc::new(RelayState {
            token: new_token()?,
            upstream_base: upstream_base.into().trim_end_matches('/').to_owned(),
            session_token: RwLock::new(None),
            client_user_agent: client_user_agent.into(),
            routes: RwLock::new(HashMap::new()),
            client,
            port: addr.port(),
        });

        let (tx, mut rx) = oneshot::channel::<()>();
        let serve_state = Arc::clone(&state);
        tokio::spawn(async move {
            loop {
                let accepted = tokio::select! {
                    _ = &mut rx => break,
                    accepted = listener.accept() => accepted,
                };
                let Ok((stream, _peer)) = accepted else { continue };
                let conn_state = Arc::clone(&serve_state);
                tokio::spawn(async move {
                    let service = service_fn(move |req| handle(Arc::clone(&conn_state), req));
                    let _ = hyper::server::conn::http1::Builder::new()
                        .serve_connection(TokioIo::new(stream), service)
                        .await;
                });
            }
        });

        Ok(LocalRelay {
            addr,
            state,
            shutdown: Some(tx),
        })
    }

    /// 交给 codex 的 `base_url`。
    #[must_use]
    pub fn base_url(&self) -> String {
        format!("http://127.0.0.1:{}/v1", self.addr.port())
    }

    /// 交给 codex 的「API key」—— 一个只在这台机器上有意义的随机串。
    #[must_use]
    pub fn token(&self) -> &str {
        &self.state.token
    }

    #[must_use]
    pub fn port(&self) -> u16 {
        self.addr.port()
    }

    /// 前端在登录/刷新之后把当前账号会话推下来。传 `None` 表示已登出。
    pub async fn set_session_token(&self, jwt: Option<String>) {
        *self.state.session_token.write().await = jwt;
    }

    /// 告诉转发层「这条 thread 属于哪个分组」。
    pub async fn bind_thread(&self, thread_id: impl Into<String>, group_id: i64) {
        self.state
            .routes
            .write()
            .await
            .insert(thread_id.into(), ThreadRoute { group_id });
    }

    /// 会话结束时解绑，免得一个早就没了的 thread 还占着一个分组归属。
    pub async fn unbind_thread(&self, thread_id: &str) {
        self.state.routes.write().await.remove(thread_id);
    }

    /// 关掉端口。
    pub fn stop(&mut self) {
        if let Some(tx) = self.shutdown.take() {
            let _ = tx.send(());
        }
    }
}

impl Drop for LocalRelay {
    fn drop(&mut self) {
        self.stop();
    }
}

/// 令牌：32 字节 CSPRNG，十六进制。
///
/// **不能**照搬 `identity.rs` 里那个「纳秒 + 地址」的凑法 —— 那个值只需要独特，
/// 这个值需要猜不出来。生不出随机数就直接失败，绝不退化成一个可预测的串。
fn new_token() -> Result<String, HostError> {
    let mut raw = [0u8; 32];
    getrandom::fill(&mut raw).map_err(|e| HostError::Proxy(format!("取不到随机数: {e}")))?;
    let mut out = String::with_capacity(raw.len() * 2);
    for byte in raw {
        out.push_str(&format!("{byte:02x}"));
    }
    Ok(out)
}

/// 定长比较，不因为第几个字节不同而提前返回。
///
/// 长度本身不保密（令牌长度是固定且公开的），所以长度不等直接返回没有问题。
fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    let mut diff = 0u8;
    for (x, y) in a.iter().zip(b.iter()) {
        diff |= x ^ y;
    }
    diff == 0
}

/// 闸门判定的结果。分开成一个函数是为了能单独测 —— 这几条挡的都是
/// 「能跑通、但不该跑通」的走法，混在 IO 里就没法逐条验证了。
pub mod guard {
    use super::constant_time_eq;

    /// 挡下来的理由。
    #[derive(Debug, Clone, PartialEq, Eq)]
    pub enum Rejected {
        /// 路径/方法不对。转发层只有一条路，多一条就是多一个入口。
        NotFound,
        /// 带了 `Origin` —— 说明这是**网页**发起的。
        ///
        /// 实测 codex 从不发 `Origin`，所以这条可以设得很死。网页读不到我们的响应
        /// （没有 CORS 头），但它**发得出请求**，而发出去就已经在花用户的额度了。
        BrowserOrigin,
        /// `Host` 不是我们自己。挡的是 DNS 重绑定：一个域名先解析到真实 IP、
        /// 再改解析到 127.0.0.1，浏览器的同源策略就被绕过去了。
        ForeignHost,
        /// 令牌不对。
        BadToken,
    }

    /// # Errors
    /// 任何一条闸门没过都返回对应的 [`Rejected`]。
    pub fn check(
        method: &str,
        path: &str,
        host: Option<&str>,
        origin: Option<&str>,
        authorization: Option<&str>,
        expected_token: &str,
        port: u16,
    ) -> Result<(), Rejected> {
        if method != "POST" || path != super::CODEX_PATH {
            return Err(Rejected::NotFound);
        }
        if origin.is_some() {
            return Err(Rejected::BrowserOrigin);
        }
        let expected_host = format!("127.0.0.1:{port}");
        if host != Some(expected_host.as_str()) {
            return Err(Rejected::ForeignHost);
        }
        let presented = authorization
            .and_then(|value| value.strip_prefix("Bearer "))
            .unwrap_or_default();
        if !constant_time_eq(presented.as_bytes(), expected_token.as_bytes()) {
            return Err(Rejected::BadToken);
        }
        Ok(())
    }
}

/// 从已登记的路由里定出这一条该走哪个分组。
///
/// **宁可报错也不猜。** 猜错的后果是这一轮悄悄跑在用户没选的分组上 ——
/// 额度和账单都记到别处，而且两端都没有任何提示。
///
/// 只有一种情况可以不带 `thread-id`：所有已登记的路由本来就指向同一个分组。
/// 那时候「用哪个」没有歧义，不算猜。
fn resolve_group(routes: &HashMap<String, ThreadRoute>, thread_id: Option<&str>) -> Option<i64> {
    if let Some(id) = thread_id {
        if let Some(route) = routes.get(id) {
            return Some(route.group_id);
        }
        // 报了一个我们不认识的 thread：这条路由没登记过，不能拿别的顶上。
        return None;
    }
    let mut iter = routes.values();
    let first = iter.next()?.group_id;
    if iter.all(|route| route.group_id == first) {
        Some(first)
    } else {
        None
    }
}

fn error_response(status: StatusCode, message: &str) -> Response<ProxyBody> {
    // OpenAI 的错误形状 —— codex 认得这个，能把它归类成上游错误而不是协议漂移。
    let body = serde_json::json!({
        "error": { "message": message, "type": "cofly_local_relay" }
    })
    .to_string();
    let mut response = Response::new(
        Full::new(Bytes::from(body))
            .map_err(|never: Infallible| match never {})
            .boxed(),
    );
    *response.status_mut() = status;
    response
        .headers_mut()
        .insert("content-type", "application/json".parse().unwrap());
    response
}

async fn handle(
    state: Arc<RelayState>,
    req: Request<Incoming>,
) -> Result<Response<ProxyBody>, Infallible> {
    let header = |name: &str| {
        req.headers()
            .get(name)
            .and_then(|v| v.to_str().ok())
            .map(str::to_owned)
    };
    let method = req.method().clone();
    let path = req.uri().path().to_owned();

    if let Err(reason) = guard::check(
        method.as_str(),
        &path,
        header("host").as_deref(),
        header("origin").as_deref(),
        header("authorization").as_deref(),
        &state.token,
        state.port,
    ) {
        let (status, message) = match reason {
            guard::Rejected::NotFound => (StatusCode::NOT_FOUND, "no such endpoint"),
            guard::Rejected::BrowserOrigin => {
                (StatusCode::FORBIDDEN, "browser-originated request refused")
            }
            guard::Rejected::ForeignHost => (StatusCode::FORBIDDEN, "unexpected Host header"),
            guard::Rejected::BadToken => (StatusCode::UNAUTHORIZED, "invalid local relay token"),
        };
        return Ok(error_response(status, message));
    }

    let Some(session) = state.session_token.read().await.clone() else {
        return Ok(error_response(
            StatusCode::UNAUTHORIZED,
            "not signed in: no account session available",
        ));
    };

    let thread_id = header("thread-id");
    let group_id = {
        let routes = state.routes.read().await;
        resolve_group(&routes, thread_id.as_deref())
    };
    let Some(group_id) = group_id else {
        return Ok(error_response(
            StatusCode::BAD_REQUEST,
            "no group is bound to this thread",
        ));
    };

    let body = match req.into_body().collect().await {
        Ok(collected) => collected.to_bytes(),
        Err(e) => {
            return Ok(error_response(
                StatusCode::BAD_REQUEST,
                &format!("failed to read request body: {e}"),
            ))
        }
    };

    // **白名单，不是黑名单。** codex 的请求里带着 installation_id、window_id 之类
    // 只对它自己有意义的东西；一条都不往上游带，就不必逐个判断哪条能带。
    let upstream = format!("{}{UPSTREAM_PATH}", state.upstream_base);
    let sent = state
        .client
        .post(&upstream)
        // 换掉，不是追加 —— 往上游带 codex 那个本地令牌是这条路上最容易犯的错。
        .header("authorization", format!("Bearer {session}"))
        .header(GROUP_HEADER, group_id.to_string())
        .header("content-type", "application/json")
        .header("accept", "text/event-stream")
        // 必须和登录那次浏览器请求的 UA 一致，否则后端的会话指纹校验会把这次
        // 转发判成"换了网络环境"，401 掉并撤销整个 token family。
        // 见 RelayState::client_user_agent。
        .header("user-agent", &state.client_user_agent)
        .body(body)
        .send()
        .await;

    let sent = match sent {
        Ok(response) => response,
        Err(e) => {
            return Ok(error_response(
                StatusCode::BAD_GATEWAY,
                &format!("relay unreachable: {e}"),
            ))
        }
    };

    let status = StatusCode::from_u16(sent.status().as_u16()).unwrap_or(StatusCode::BAD_GATEWAY);
    let content_type = sent
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("text/event-stream")
        .to_owned();

    // **流式转发。** 攒完再吐会同时打坏两件事：正文不再是逐字出现的，
    // 而且「停止」按钮要等这一轮跑完才生效 —— 那时候停已经没有意义了。
    let stream = sent
        .bytes_stream()
        .map_ok(Frame::data)
        .map_err(std::io::Error::other);
    let mut response = Response::new(StreamBody::new(stream).boxed());
    *response.status_mut() = status;
    if let Ok(value) = content_type.parse() {
        response.headers_mut().insert("content-type", value);
    }
    Ok(response)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn token_is_not_predictable_and_is_long_enough() {
        let a = new_token().unwrap();
        let b = new_token().unwrap();
        assert_eq!(a.len(), 64, "32 字节十六进制");
        assert_ne!(a, b);
    }

    #[test]
    fn constant_time_eq_still_compares_correctly() {
        assert!(constant_time_eq(b"abc", b"abc"));
        assert!(!constant_time_eq(b"abc", b"abd"));
        assert!(!constant_time_eq(b"abc", b"ab"));
        assert!(constant_time_eq(b"", b""));
    }
}
