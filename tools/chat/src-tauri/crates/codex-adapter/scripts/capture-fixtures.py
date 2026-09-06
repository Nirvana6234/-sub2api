"""录制真 codex 进程的报文，供 tests/replay.rs 回放。

    python capture-fixtures.py --codex <bin> --version 0.153.0 [--scenario ...]

`<bin>` 可以是上游 app-server bundle 里的 `codex-app-server.exe`，也可以是官方
多合一的 `codex.exe`（后者要带 `app-server` 子命令，这里按文件名自动判断）。

# 为什么不再对着真中转站录

旧版要一把真 key、一个真中转站，而且**靠模型自己决定去做什么** —— 于是
「让 agent 创建一个文件」这种提问有时候根本不触发审批，测试就静默地什么都没验。
现在假中转站直接按剧本吐工具调用，模型那一半被完全替换掉：

  - **确定性**：同一个剧本每次录出同样的交互，fixture 可复现。
  - **不花钱、不要 key、不连网**，谁都能重录。
  - 想验哪条协议路径就写哪条剧本，不用哄模型。

这么做不会削弱 fixture 的价值：这些 fixture 验的是**协议形状**（审批请求长什么样、
拒绝之后 item 变成什么、上游错误怎么到达），而不是模型的判断力。协议那一半仍然
是真 codex 进程产生的。
"""
import argparse
import io
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURES = os.path.join(HERE, "..", "tests", "fixtures")

SECRET_KEYS = {"apiKey", "api_key", "OPENAI_API_KEY", "token", "access_token", "Authorization"}
REDACTED = "<redacted>"


def redact(node):
    """深拷贝 `node`，把任何看着像密钥的值换掉。"""
    if isinstance(node, dict):
        return {k: (REDACTED if k in SECRET_KEYS and isinstance(v, str) else redact(v))
                for k, v in node.items()}
    if isinstance(node, list):
        return [redact(v) for v in node]
    return node


# --------------------------------------------------------------------------
# 假中转站：按剧本吐 SSE
# --------------------------------------------------------------------------

def sse(name, payload):
    return ("event: %s\ndata: %s\n\n" % (name, json.dumps(payload))).encode("utf-8")


def _envelope(rid, output):
    return {"id": rid, "object": "response", "created_at": 0, "status": "completed",
            "model": "gpt-5", "output": output,
            "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}


REASONING = "Checking the workspace before touching anything."


def _reasoning_events(index):
    """推理 item 的一整套事件。

    必须有 —— UI 要把推理过程画出来，而 `replay.rs` 直接断言了
    `reasoning` 这个 item 类型。假中转站如果只吐工具调用，录出来的 fixture
    就比真实情况**穷**，于是测试静默地少验一大块。
    """
    item = {"id": "rs_1", "type": "reasoning", "summary": []}
    done = {"id": "rs_1", "type": "reasoning",
            "summary": [{"type": "summary_text", "text": REASONING}]}
    return [
        sse("response.output_item.added",
            {"type": "response.output_item.added", "output_index": index, "item": item}),
        sse("response.reasoning_summary_part.added",
            {"type": "response.reasoning_summary_part.added", "item_id": "rs_1",
             "output_index": index, "summary_index": 0,
             "part": {"type": "summary_text", "text": ""}}),
        sse("response.reasoning_summary_text.delta",
            {"type": "response.reasoning_summary_text.delta", "item_id": "rs_1",
             "output_index": index, "summary_index": 0, "delta": REASONING}),
        sse("response.reasoning_summary_text.done",
            {"type": "response.reasoning_summary_text.done", "item_id": "rs_1",
             "output_index": index, "summary_index": 0, "text": REASONING}),
        sse("response.reasoning_summary_part.done",
            {"type": "response.reasoning_summary_part.done", "item_id": "rs_1",
             "output_index": index, "summary_index": 0,
             "part": {"type": "summary_text", "text": REASONING}}),
        sse("response.output_item.done",
            {"type": "response.output_item.done", "output_index": index, "item": done}),
    ], done


def tool_call(cmd, justification="This command writes to the workspace."):
    """一轮：先推理，再调 exec_command 执行 cmd。

    这里刻意录**提权**那条路（`sandbox_permissions: "require_escalated"`），两个原因：

    1. `justification` 就是审批请求里给人看的 `reason`，而 codex 要求它和
       `sandbox_permissions` **成对出现** —— 只给 justification 会被顶回来：
       "`justification` requires an explicit `sandbox_permissions`"。没有 reason
       的审批弹窗上只剩一条光秃的命令，用户无从判断该不该同意。
    2. 提权这条正是**最危险**的一条：批准之后命令完全脱离沙箱（实测能写到工作区外）。
       A7 的审批界面首先要把这条渲染对，所以 fixture 就该录它。
    """
    args = json.dumps({"cmd": cmd, "justification": justification,
                       "sandbox_permissions": "require_escalated"})
    item = {"id": "fc_1", "type": "function_call", "status": "completed",
            "arguments": args, "call_id": "call_1", "name": "exec_command"}
    reasoning_events, reasoning_done = _reasoning_events(0)
    return [
        sse("response.created", {"type": "response.created",
                                 "response": {**_envelope("r1", []), "status": "in_progress"}}),
    ] + reasoning_events + [
        sse("response.output_item.added", {"type": "response.output_item.added",
                                           "output_index": 1,
                                           "item": {**item, "status": "in_progress",
                                                    "arguments": ""}}),
        sse("response.function_call_arguments.done",
            {"type": "response.function_call_arguments.done",
             "item_id": "fc_1", "output_index": 1, "arguments": args}),
        sse("response.output_item.done", {"type": "response.output_item.done",
                                          "output_index": 1, "item": item}),
        sse("response.completed", {"type": "response.completed",
                                   "response": _envelope("r1", [reasoning_done, item])}),
    ]


def text(msg):
    """一轮：只说一句话收工。"""
    item = {"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
            "content": [{"type": "output_text", "text": msg, "annotations": []}]}
    return [
        sse("response.created", {"type": "response.created",
                                 "response": {**_envelope("r2", []), "status": "in_progress"}}),
        sse("response.output_item.added", {"type": "response.output_item.added",
                                           "output_index": 0,
                                           "item": {**item, "status": "in_progress",
                                                    "content": []}}),
        sse("response.output_text.delta", {"type": "response.output_text.delta",
                                           "item_id": "msg_1", "output_index": 0,
                                           "content_index": 0, "delta": msg}),
        sse("response.output_item.done", {"type": "response.output_item.done",
                                          "output_index": 0, "item": item}),
        sse("response.completed", {"type": "response.completed",
                                   "response": _envelope("r2", [item])}),
    ]


class FakeRelay:
    """按剧本回应；剧本用完之后一律收工。`status` 非 200 时直接回错误。"""

    def __init__(self, script, status=200, error_body=None):
        self.script = list(script)
        self.status = status
        self.error_body = error_body
        self.seen = 0
        self.lock = threading.Lock()
        relay = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_POST(self):
                n = int(self.headers.get("content-length") or 0)
                if n:
                    self.rfile.read(n)
                with relay.lock:
                    relay.seen += 1
                    idx = relay.seen - 1
                    chunks = relay.script[idx] if idx < len(relay.script) else text("done")

                if relay.status != 200:
                    body = json.dumps(relay.error_body).encode("utf-8")
                    BaseHTTPRequestHandler.send_response(self, relay.status)
                    self.send_header("content-type", "application/json")
                    self.send_header("content-length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    return

                BaseHTTPRequestHandler.send_response(self, 200)
                self.send_header("content-type", "text/event-stream")
                self.send_header("transfer-encoding", "chunked")
                self.end_headers()
                for chunk in chunks:
                    self.wfile.write(("%x\r\n" % len(chunk)).encode("ascii") + chunk + b"\r\n")
                    self.wfile.flush()
                self.wfile.write(b"0\r\n\r\n")
                self.wfile.flush()

            def log_message(self, *a):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.port = self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    @property
    def base_url(self):
        return "http://127.0.0.1:%d/v1" % self.port

    def close(self):
        self.server.shutdown()


# --------------------------------------------------------------------------
# 会话：起进程 + 录下 stdio 上的每一条
# --------------------------------------------------------------------------

class Session:
    def __init__(self, codex, codex_home, workdir, base_url, model):
        self.records = []
        self.lock = threading.Lock()
        self.pending = {}
        self.next_id = 0
        self.approvals = []
        self.auto_answer = None
        self.started_at = time.time()

        env = dict(os.environ)
        env["CODEX_HOME"] = codex_home
        env.pop("OPENAI_API_KEY", None)
        env.pop("CODEX_API_KEY", None)  # 对 app-server 本来就无效；显式清掉以证明这一点

        # 官方多合一 codex(.exe) 要带 app-server 子命令；上游 app-server bundle 里的
        # codex-app-server(.exe) 自己就是 app-server，多传会被 clap 当未知参数拒掉。
        stem = os.path.splitext(os.path.basename(codex))[0].lower()
        sub = [] if stem == "codex-app-server" else ["app-server"]

        self.proc = subprocess.Popen(
            [codex] + sub + [
                "-c", 'model_provider="custom"',
                "-c", 'model_providers.custom.name="custom"',
                "-c", 'model_providers.custom.wire_api="responses"',
                "-c", "model_providers.custom.requires_openai_auth=true",
                "-c", 'model_providers.custom.base_url="%s"' % base_url,
                "-c", 'model="%s"' % model,
            ],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, cwd=workdir, bufsize=0)
        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()

    def _record(self, direction, msg):
        self.records.append({"t_ms": int((time.time() - self.started_at) * 1000),
                             "dir": direction, "msg": redact(msg)})

    def _read_stdout(self):
        for line in self.proc.stdout:
            line = line.decode("utf-8", "replace").strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except Exception:
                continue
            with self.lock:
                self._record("server->client", msg)
                if "id" in msg and ("result" in msg or "error" in msg):
                    self.pending[msg["id"]] = msg
            method = msg.get("method") or ""
            if "id" in msg and method.endswith("requestApproval"):
                with self.lock:
                    self.approvals.append(method)
                if self.auto_answer is not None:
                    self.send({"jsonrpc": "2.0", "id": msg["id"], "result": self.auto_answer})

    def _read_stderr(self):
        for line in self.proc.stderr:
            self.records.append({"t_ms": int((time.time() - self.started_at) * 1000),
                                 "dir": "stderr",
                                 "text": line.decode("utf-8", "replace").rstrip()})

    def send(self, msg):
        line = json.dumps(msg, ensure_ascii=False) + "\n"
        with self.lock:
            self.proc.stdin.write(line.encode("utf-8"))
            self.proc.stdin.flush()
            self._record("client->server", msg)

    def request(self, method, params):
        self.next_id += 1
        rid = self.next_id
        self.send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        return rid

    def wait_for(self, rid, timeout):
        end = time.time() + timeout
        while time.time() < end:
            with self.lock:
                if rid in self.pending:
                    return self.pending.pop(rid)
            time.sleep(0.05)
        return None

    def wait_notification(self, method, timeout):
        end = time.time() + timeout
        while time.time() < end:
            with self.lock:
                for r in self.records:
                    if r.get("dir") == "server->client" and (r.get("msg") or {}).get("method") == method:
                        return r["msg"]
            time.sleep(0.1)
        return None

    def close(self):
        try:
            self.proc.kill()
        except Exception:
            pass


def write_fixture(scenario, records, summary):
    os.makedirs(FIXTURES, exist_ok=True)
    path = os.path.join(FIXTURES, "%s.jsonl" % scenario)
    with io.open(path, "w", encoding="utf-8", newline="\n") as fh:
        for rec in records:
            fh.write(json.dumps(rec, ensure_ascii=False) + "\n")

    manifest_path = os.path.join(FIXTURES, "manifest.json")
    manifest = {}
    if os.path.exists(manifest_path):
        manifest = json.load(io.open(manifest_path, encoding="utf-8"))
    manifest[scenario] = summary
    with io.open(manifest_path, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n")

    print(json.dumps(summary, ensure_ascii=False, indent=2))
    print("transcript -> %s" % path)


def make_dirs(tag):
    root = tempfile.mkdtemp(prefix="codex-fixture-%s-" % tag)
    home = os.path.join(root, "home")
    work = os.path.join(root, "work")
    os.makedirs(home)
    os.makedirs(work)
    io.open(os.path.join(work, "README.md"), "w", encoding="utf-8").write("fixture workspace\n")
    return root, home, work


def handshake(s, timeout=30):
    rid = s.request("initialize", {"clientInfo": {"name": "cofly-workbench", "title": None,
                                                  "version": "0.1.0"},
                                   "capabilities": None})
    if not (s.wait_for(rid, timeout) or {}).get("result"):
        raise SystemExit("initialize 失败")
    s.send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
    rid = s.request("account/login/start", {"type": "apiKey", "apiKey": "fixture-local-token"})
    if not (s.wait_for(rid, timeout) or {}).get("result"):
        raise SystemExit("account/login/start 失败")


def start_thread(s, work, sandbox, approval, timeout=60):
    rid = s.request("thread/start", {"cwd": work, "sandbox": sandbox,
                                     "approvalPolicy": approval})
    resp = s.wait_for(rid, timeout) or {}
    result = resp.get("result") or {}
    return ((result.get("thread") or {}).get("id") or result.get("threadId")
            or result.get("id")), resp


# --------------------------------------------------------------------------
# 各个场景
# --------------------------------------------------------------------------

def capture_decline_command(args):
    """审批一条命令然后**拒绝** —— 验「拒绝确实生效，且本轮继续」。"""
    marker = "SHOULD_NOT_EXIST.txt"
    relay = FakeRelay([tool_call('cmd /c echo escaped > %s' % marker)])
    root, home, work = make_dirs("decline-cmd")
    s = Session(args.codex, home, work, relay.base_url, args.model)
    s.auto_answer = {"decision": "decline"}
    try:
        handshake(s)
        thread_id, resp = start_thread(s, work, "read-only", "on-request")
        if not thread_id:
            raise SystemExit("thread/start 没给 thread id: %s" % json.dumps(resp)[:300])
        s.request("turn/start", {"threadId": thread_id,
                                 "input": [{"type": "text", "text": "run it",
                                            "text_elements": []}]})
        s.wait_notification("turn/completed", args.turn_timeout)
        time.sleep(1.0)
    finally:
        s.close()
        relay.close()

    declined = [r for r in s.records
                if (r.get("msg") or {}).get("method") == "item/completed"
                and (((r["msg"].get("params") or {}).get("item") or {}).get("status")
                     == "declined")]
    write_fixture("decline-command-approval", s.records, {
        "codexVersion": args.version,
        "scenario": "decline-command-approval",
        "recordedAgainst": "假中转站（按剧本吐 exec_command 调用），不连真中转站",
        "approvalMethods": sorted(set(s.approvals)),
        "approvalRequestsSeen": len(s.approvals),
        "declinedItems": len(declined),
        "declinedCommandDidNotRun": not os.path.exists(os.path.join(work, marker)),
        "records": len(s.records),
    })


def capture_bad_credentials(args):
    """上游回 401 —— 验「上游错误走 error 通知，不是 JSON-RPC 错误响应」。"""
    # 照抄我们后端真实返回的形状（`middleware.NewErrorResponse`）——
    # 是扁平的 code/message，**不是** OpenAI 那种嵌套在 `error` 下面的。
    # 拿错形状会让 additionalDetails 里拿不到真正的原因。
    relay = FakeRelay([], status=401, error_body={
        "code": "INVALID_API_KEY", "message": "Invalid API key"})
    root, home, work = make_dirs("bad-creds")
    s = Session(args.codex, home, work, relay.base_url, args.model)
    try:
        handshake(s)
        thread_id, resp = start_thread(s, work, "read-only", "never")
        if thread_id:
            s.request("turn/start", {"threadId": thread_id,
                                     "input": [{"type": "text", "text": "say hi",
                                                "text_elements": []}]})
            s.wait_notification("error", 60)
            time.sleep(2.0)
    finally:
        s.close()
        relay.close()

    error_responses = [r for r in s.records
                       if r.get("dir") == "server->client" and "error" in (r.get("msg") or {})]
    failure_notes = sorted({(r.get("msg") or {}).get("method") for r in s.records
                            if r.get("dir") == "server->client"
                            and (r.get("msg") or {}).get("method")
                            in ("error", "turn/failed", "turn/completed")})
    write_fixture("bad-credentials", s.records, {
        "codexVersion": args.version,
        "scenario": "bad-credentials",
        "recordedAgainst": "假中转站直接回 401",
        "threadStarted": bool(thread_id),
        "jsonRpcErrorResponses": len(error_responses),
        "failureNotifications": [m for m in failure_notes if m],
        "records": len(s.records),
    })


def capture_invalid_cwd(args):
    """给一个不存在的 cwd —— 验「codex 不校验，目录合法性只能宿主自己管」。"""
    relay = FakeRelay([])
    root, home, work = make_dirs("invalid-cwd")
    missing = os.path.join(root, "definitely-not-here", "nested")
    s = Session(args.codex, home, work, relay.base_url, args.model)
    try:
        handshake(s)
        thread_id, resp = start_thread(s, missing, "read-only", "never")
        time.sleep(1.0)
    finally:
        s.close()
        relay.close()

    write_fixture("invalid-cwd", s.records, {
        "codexVersion": args.version,
        "scenario": "invalid-cwd",
        "recordedAgainst": "假中转站（这条根本不发请求）",
        "cwdExists": os.path.exists(missing),
        "threadStartedAnyway": bool(thread_id),
        "records": len(s.records),
    })


def capture_decline_file_change(args):
    """走 apply_patch 触发 fileChange 审批然后拒绝。

    0.153.0 的工具集里没有独立的 patch 工具，改文件是把 apply_patch 当命令跑，
    codex 会把它识别成 fileChange。**这条是不是还触发得出来，以实测为准** ——
    触发不出来就如实记进 manifest，不假装录到了。
    """
    patch = (
        "apply_patch <<'PATCH'\n"
        "*** Begin Patch\n"
        "*** Add File: fixture-added.txt\n"
        "+hello from fixture\n"
        "*** End Patch\n"
        "PATCH\n"
    )
    relay = FakeRelay([tool_call(patch)])
    root, home, work = make_dirs("decline-patch")
    s = Session(args.codex, home, work, relay.base_url, args.model)
    s.auto_answer = {"decision": "decline"}
    try:
        handshake(s)
        thread_id, resp = start_thread(s, work, "read-only", "on-request")
        if not thread_id:
            raise SystemExit("thread/start 没给 thread id")
        s.request("turn/start", {"threadId": thread_id,
                                 "input": [{"type": "text", "text": "apply it",
                                            "text_elements": []}]})
        s.wait_notification("turn/completed", args.turn_timeout)
        time.sleep(1.0)
    finally:
        s.close()
        relay.close()

    item_types = sorted({((r.get("msg") or {}).get("params") or {}).get("item", {}).get("type")
                         for r in s.records
                         if (r.get("msg") or {}).get("method") in
                         ("item/started", "item/completed")} - {None})
    write_fixture("decline-file-change", s.records, {
        "codexVersion": args.version,
        "scenario": "decline-file-change",
        "recordedAgainst": "假中转站（剧本里是一条 apply_patch 命令）",
        "approvalMethods": sorted(set(s.approvals)),
        "itemTypesSeen": item_types,
        "fileWasNotCreated": not os.path.exists(os.path.join(work, "fixture-added.txt")),
        "records": len(s.records),
    })


SCENARIOS = {
    "decline-command-approval": capture_decline_command,
    "decline-file-change": capture_decline_file_change,
    "bad-credentials": capture_bad_credentials,
    "invalid-cwd": capture_invalid_cwd,
}


def main():
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--codex", required=True,
                   help="codex-app-server(.exe) 或官方 codex(.exe)")
    p.add_argument("--version", required=True, help="那个二进制的版本，例如 0.153.0")
    p.add_argument("--model", default="gpt-5")
    p.add_argument("--scenario", default="all", choices=["all"] + sorted(SCENARIOS))
    p.add_argument("--turn-timeout", type=int, default=90)
    args = p.parse_args()

    todo = sorted(SCENARIOS) if args.scenario == "all" else [args.scenario]
    for name in todo:
        print("\n=== %s ===" % name, flush=True)
        SCENARIOS[name](args)


if __name__ == "__main__":
    main()
