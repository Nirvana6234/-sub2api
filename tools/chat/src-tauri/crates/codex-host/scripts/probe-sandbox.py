"""沙箱到底拦不拦得住 —— 用真进程量，不靠读文档。

跑法：
    python probe-sandbox.py <包目录>      # 目录里要有 bin/codex-app-server.exe
    python probe-sandbox.py <codex.exe>     # 官方多合一二进制也行

假中转站不返回正文，而是返回一次 exec_command 工具调用，让 agent 真去写文件，
然后看盘上有没有那个文件。**不花钱、不连中转站、不要 key。**

已经量出来的结论（记在这里免得下次又猜）：

  | sandbox | 决定 | 往**工作区外**写 |
  |---|---|---|
  | read-only | accept | **成功** |
  | workspace-write | accept | **成功** |
  | read-only | decline | 失败 |

**一旦批准，命令就完全脱离沙箱**（`core/src/tools/sandboxing.rs`：
requires_escalated_permissions -> BypassSandboxFirstAttempt）。所以：

  - **沙箱不是安全边界，审批才是。** sandbox 参数只约束不经审批就跑的命令。
  - **目录白名单没法靠 codex 的沙箱实现**（A8）。
  - **审批 UI 不能暗示「同意＝在沙箱里跑一下」**（A7）；同意就是交出整台机器。

另一个容易混的点：`approvalPolicy=never` 时，没被自动放行的命令会在**进沙箱之前**
就被 exec_policy 拒掉（"blocked by policy"）—— 看着像沙箱拦住了，其实沙箱没被调到。
"""
import io
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PKG = sys.argv[1]
if PKG.lower().endswith(".exe"):
    CODEX, SUB = PKG, ["app-server"]          # 直接指官方 codex.exe
else:
    CODEX, SUB = os.path.join(PKG, "bin", "codex-app-server.exe"), []
ROOT = "C:/Users/Borg/AppData/Local/cofly-sandbox-probe/" + ("official" if PKG.lower().endswith(".exe") else "slim")

state = {"count": 0, "cmd": ""}
lock = threading.Lock()


def sse(name, payload):
    return ("event: %s\ndata: %s\n\n" % (name, json.dumps(payload))).encode("utf-8")


def tool_call_stream(cmd):
    args = json.dumps({"cmd": cmd})
    item = {"id": "fc_1", "type": "function_call", "status": "completed",
            "arguments": args, "call_id": "call_1", "name": "exec_command"}
    return [
        sse("response.created", {"type": "response.created", "response": {
            "id": "r1", "object": "response", "created_at": 0,
            "status": "in_progress", "model": "gpt-5", "output": []}}),
        sse("response.output_item.added", {
            "type": "response.output_item.added", "output_index": 0,
            "item": {**item, "status": "in_progress", "arguments": ""}}),
        sse("response.function_call_arguments.done", {
            "type": "response.function_call_arguments.done",
            "item_id": "fc_1", "output_index": 0, "arguments": args}),
        sse("response.output_item.done", {
            "type": "response.output_item.done", "output_index": 0, "item": item}),
        sse("response.completed", {"type": "response.completed", "response": {
            "id": "r1", "object": "response", "created_at": 0, "status": "completed",
            "model": "gpt-5", "output": [item],
            "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}),
    ]


def text_stream(text):
    item = {"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
            "content": [{"type": "output_text", "text": text, "annotations": []}]}
    return [
        sse("response.created", {"type": "response.created", "response": {
            "id": "r2", "object": "response", "created_at": 0,
            "status": "in_progress", "model": "gpt-5", "output": []}}),
        sse("response.output_item.added", {
            "type": "response.output_item.added", "output_index": 0,
            "item": {**item, "status": "in_progress", "content": []}}),
        sse("response.output_text.delta", {
            "type": "response.output_text.delta", "item_id": "msg_1",
            "output_index": 0, "content_index": 0, "delta": text}),
        sse("response.output_item.done", {
            "type": "response.output_item.done", "output_index": 0, "item": item}),
        sse("response.completed", {"type": "response.completed", "response": {
            "id": "r2", "object": "response", "created_at": 0, "status": "completed",
            "model": "gpt-5", "output": [item],
            "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}),
    ]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_POST(self):
        n = int(self.headers.get("content-length") or 0)
        if n:
            self.rfile.read(n)
        with lock:
            state["count"] += 1
            first = state["count"] == 1
            cmd = state["cmd"]
        chunks = tool_call_stream(cmd) if first else text_stream("done")
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


DECLINE = [False]


def run_case(tag, sandbox, approval, cmd, marker=None):
    DECLINE[0] = tag.endswith('declined')
    with lock:
        state["count"] = 0
        state["cmd"] = cmd
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    port = server.server_address[1]
    threading.Thread(target=server.serve_forever, daemon=True).start()

    case_root = os.path.join(ROOT, tag)
    shutil.rmtree(case_root, ignore_errors=True)
    home, work = os.path.join(case_root, "home"), os.path.join(case_root, "work")
    os.makedirs(home, exist_ok=True)
    os.makedirs(work, exist_ok=True)
    io.open(os.path.join(work, "README.md"), "w", encoding="utf-8").write("probe\n")
    marker_path = os.path.join(work, marker) if marker else None

    env = dict(os.environ)
    env["CODEX_HOME"] = home
    env.pop("OPENAI_API_KEY", None)
    env.pop("CODEX_API_KEY", None)

    proc = subprocess.Popen(
        [CODEX] + SUB + [
         "-c", 'model_provider="custom"',
         "-c", 'model_providers.custom.name="custom"',
         "-c", 'model_providers.custom.wire_api="responses"',
         "-c", "model_providers.custom.requires_openai_auth=true",
         "-c", 'model_providers.custom.base_url="http://127.0.0.1:%d/v1"' % port,
         "-c", 'model="gpt-5"'],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        env=env, cwd=work, bufsize=0)

    pending, rpc, approvals = {}, [], []
    rlock = threading.Lock()
    stderr_lines = []

    def send(obj):
        proc.stdin.write((json.dumps(obj) + "\n").encode("utf-8"))
        proc.stdin.flush()

    def reader():
        for line in proc.stdout:
            try:
                msg = json.loads(line.decode("utf-8", "replace").strip())
            except Exception:
                continue
            with rlock:
                rpc.append(msg)
                if "id" in msg and ("result" in msg or "error" in msg):
                    pending[msg["id"]] = msg
            # 服务端请求：审批一律同意，好让命令真的跑到沙箱那一层
            method = msg.get("method") or ""
            if "id" in msg and method.endswith("requestApproval"):
                with rlock:
                    approvals.append(method)
                decision = "decline" if DECLINE[0] else "accept"
                send({"jsonrpc": "2.0", "id": msg["id"],
                      "result": {"decision": decision}})

    threading.Thread(target=reader, daemon=True).start()
    threading.Thread(
        target=lambda: [stderr_lines.append(l.decode("utf-8", "replace"))
                        for l in proc.stderr], daemon=True).start()

    nxt = [0]

    def req(method, params, timeout=60):
        nxt[0] += 1
        rid = nxt[0]
        send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        end = time.time() + timeout
        while time.time() < end:
            with rlock:
                if rid in pending:
                    return pending.pop(rid)
            time.sleep(0.05)
        return None

    req("initialize", {"clientInfo": {"name": "probe", "title": None, "version": "0.1.0"},
                       "capabilities": None}, 30)
    send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
    req("account/login/start", {"type": "apiKey", "apiKey": "local-token"}, 30)

    resp = req("thread/start", {"cwd": work, "sandbox": sandbox,
                                "approvalPolicy": approval}, 60) or {}
    result = resp.get("result") or {}
    thread_id = ((result.get("thread") or {}).get("id") or result.get("threadId")
                 or result.get("id"))
    if not thread_id:
        proc.kill(); server.shutdown()
        return {"tag": tag, "error": "thread/start 失败"}

    req("turn/start", {"threadId": thread_id,
                       "input": [{"type": "text", "text": "run it", "text_elements": []}]}, 30)
    deadline = time.time() + 90
    while time.time() < deadline:
        with rlock:
            if any(m.get("method") == "turn/completed" for m in rpc):
                break
        time.sleep(0.2)
    time.sleep(1.0)

    with rlock:
        snapshot = list(rpc)
        seen_approvals = list(approvals)
    proc.kill()
    server.shutdown()

    items = []
    for m in snapshot:
        if m.get("method") == "item/completed":
            it = (m.get("params") or {}).get("item") or {}
            items.append({
                "type": it.get("type"),
                "status": it.get("status"),
                "exitCode": it.get("exitCode"),
                "out": str(it.get("aggregatedOutput") or it.get("output") or "")[:200],
            })
    errs = [l for l in stderr_lines if "ERROR" in l]
    return {
        "tag": tag, "sandbox": sandbox, "approvalPolicy": approval, "cmd": cmd,
        "markerWritten": (os.path.exists(marker_path) if marker_path else None),
        "approvalsSeen": seen_approvals,
        "items": [i for i in items if i["type"] != "userMessage"],
        "stderrErrors": [e.split("error=")[-1][:220] for e in errs][:3],
    }


OUT = "C:/Users/Borg/AppData/Local/cofly-outside-workspace.txt"
import os as _os
if _os.path.exists(OUT):
    _os.remove(OUT)

CASES = [
    # 只读沙箱 + 批准：往**工作目录外**写。成功＝批准等于完全脱离沙箱。
    ("ro-outside", "read-only", "on-request",
     'cmd /c echo escaped > "%s"' % OUT.replace("/", "\\"), None),
    # 可写沙箱 + 批准：同样往工作目录外写。workspace-write 的字面含义是
    # "只能写工作区"，这里若也成功，说明沙箱边界在批准面前不存在。
    ("ws-outside", "workspace-write", "on-request",
     'cmd /c echo escaped > "%s"' % OUT.replace("/", "\\"), None),
    # 对照：不批准（拒绝），必须写不进去。
    ("ro-declined", "read-only", "on-request",
     'cmd /c echo escaped > "%s"' % OUT.replace("/", "\\"), None),
]

print("package: %s\n" % PKG, flush=True)
results = {}
for tag, sandbox, approval, cmd, marker in CASES:
    print("--- %s ---" % tag, flush=True)
    if os.path.exists(OUT):
        os.remove(OUT)
    results[tag] = run_case(tag, sandbox, approval, cmd, marker)
    results[tag]["outsideFileWritten"] = os.path.exists(OUT)
    print(json.dumps(results[tag], ensure_ascii=False, indent=2), flush=True)
    print(flush=True)

io.open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "sandbox-results.json"),
        "w", encoding="utf-8").write(json.dumps(results, ensure_ascii=False, indent=2))
