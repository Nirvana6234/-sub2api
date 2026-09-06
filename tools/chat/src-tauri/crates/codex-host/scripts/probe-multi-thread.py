"""Probe A (see 开发计划 A6 多会话改造): can ONE codex-app-server process run
TWO threads with genuinely CONCURRENT turns, or does it serialize them?

This is load-bearing for the multi-conversation redesign — if turns serialize,
the "one process, N threads" design is wrong and we fall back to a much
smaller "one live thread + explicit switch" change instead.

Method: start two threads with different cwds, fire `turn/start` on BOTH
without waiting for either to finish, and check two independent signals:

  1. Does the fake HTTP backend see two POST /responses requests
     OVERLAPPING in wall-clock time (not turn 2 starting only after turn 1's
     stream closes)? This is proof at the network boundary, before any of
     our own event-plumbing assumptions.
  2. On the JSON-RPC stdout stream, do item/agentMessage/delta notifications
     for the two threadIds INTERLEAVE, or does thread A's item/completed
     always precede thread B's first delta?

Each backend response is deliberately slow (chunked, paced) so that if codex
serializes turns internally, the two responses will visibly NOT overlap.
"""
import io
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DEFAULT_CODEX = "C:/Work/Git/codex/codex-main/codex-rs/target/release/codex-app-server.exe"
CODEX = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_CODEX
FAKE_KEY = "cofly-local-proxy-token-0123456789abcdef"

http_log = []
log_lock = threading.Lock()
START = time.time()


def sse_payload(tag):
    return [
        ("response.created", {"type": "response.created", "response": {
            "id": "resp_" + tag, "object": "response", "created_at": 0,
            "status": "in_progress", "model": "gpt-5", "output": []}}),
        ("response.output_item.added", {"type": "response.output_item.added", "output_index": 0,
            "item": {"id": "msg_" + tag, "type": "message", "role": "assistant",
                     "status": "in_progress", "content": []}}),
        ("response.content_part.added", {"type": "response.content_part.added",
            "item_id": "msg_" + tag, "output_index": 0, "content_index": 0,
            "part": {"type": "output_text", "text": "", "annotations": []}}),
        ("response.output_text.delta", {"type": "response.output_text.delta",
            "item_id": "msg_" + tag, "output_index": 0, "content_index": 0,
            "delta": "FROM-" + tag}),
        ("response.output_text.done", {"type": "response.output_text.done",
            "item_id": "msg_" + tag, "output_index": 0, "content_index": 0, "text": "FROM-" + tag}),
        ("response.content_part.done", {"type": "response.content_part.done",
            "item_id": "msg_" + tag, "output_index": 0, "content_index": 0,
            "part": {"type": "output_text", "text": "FROM-" + tag, "annotations": []}}),
        ("response.output_item.done", {"type": "response.output_item.done", "output_index": 0,
            "item": {"id": "msg_" + tag, "type": "message", "role": "assistant", "status": "completed",
                     "content": [{"type": "output_text", "text": "FROM-" + tag, "annotations": []}]}}),
        ("response.completed", {"type": "response.completed", "response": {
            "id": "resp_" + tag, "object": "response", "created_at": 0, "status": "completed",
            "model": "gpt-5",
            "output": [{"id": "msg_" + tag, "type": "message", "role": "assistant",
                        "status": "completed",
                        "content": [{"type": "output_text", "text": "FROM-" + tag,
                                     "annotations": []}]}],
            "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}),
    ]


class Recorder(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _read_body(self):
        n = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(n) if n else b""
        try:
            return json.loads(raw.decode("utf-8"))
        except Exception:
            return raw.decode("utf-8", "replace")

    def do_POST(self):
        body = self._read_body()
        thread_id = self.headers.get("thread-id") or self.headers.get("Thread-Id") or "?"
        tag = "T1" if thread_id.endswith("1") or "1" in thread_id[-4:] else thread_id[-6:]
        started_ms = int((time.time() - START) * 1000)
        with log_lock:
            http_log.append({"t_ms_start": started_ms, "thread_id": thread_id, "path": self.path})

        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-store")
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()
        # 故意放慢、分片发——如果 codex 内部把两轮串行化，第二个请求要等
        # 第一个流完全关闭（含这几次 sleep）才会打到这台假服务器上。
        for name, payload in sse_payload(thread_id[-6:] if thread_id != "?" else tag):
            chunk = ("event: %s\ndata: %s\n\n" % (name, json.dumps(payload))).encode("utf-8")
            self.wfile.write(("%x\r\n" % len(chunk)).encode("ascii") + chunk + b"\r\n")
            self.wfile.flush()
            time.sleep(0.4)
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()
        with log_lock:
            http_log.append({
                "t_ms_end": int((time.time() - START) * 1000),
                "thread_id": thread_id,
                "path": self.path,
            })

    def do_GET(self):
        body = json.dumps({"object": "list",
                           "data": [{"id": "gpt-5", "object": "model"}]}).encode("utf-8")
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


server = ThreadingHTTPServer(("127.0.0.1", 0), Recorder)
PORT = server.server_address[1]
threading.Thread(target=server.serve_forever, daemon=True).start()
BASE_URL = "http://127.0.0.1:%d/v1" % PORT
print("recording server on " + BASE_URL, flush=True)

root = tempfile.mkdtemp(prefix="probe-multi-thread-")
codex_home = os.path.join(root, "home")
work_a = os.path.join(root, "work-a")
work_b = os.path.join(root, "work-b")
os.makedirs(codex_home)
os.makedirs(work_a)
os.makedirs(work_b)
io.open(os.path.join(work_a, "README.md"), "w", encoding="utf-8").write("a\n")
io.open(os.path.join(work_b, "README.md"), "w", encoding="utf-8").write("b\n")

env = dict(os.environ)
env["CODEX_HOME"] = codex_home
env.pop("OPENAI_API_KEY", None)
env.pop("CODEX_API_KEY", None)

_stem = os.path.splitext(os.path.basename(CODEX))[0].lower()
SUBCOMMAND = [] if _stem == "codex-app-server" else ["app-server"]
print("binary=%s subcommand=%r" % (CODEX, SUBCOMMAND), flush=True)

proc = subprocess.Popen(
    [CODEX] + SUBCOMMAND +
    ["-c", 'model_provider="custom"',
     "-c", 'model_providers.custom.name="custom"',
     "-c", 'model_providers.custom.wire_api="responses"',
     "-c", "model_providers.custom.requires_openai_auth=true",
     "-c", 'model_providers.custom.base_url="%s"' % BASE_URL,
     "-c", 'model="gpt-5"'],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    env=env, cwd=root, bufsize=0)

rpc = []
pending = {}
lock = threading.Lock()


def reader():
    for line in proc.stdout:
        line = line.decode("utf-8", "replace").strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except Exception:
            continue
        recv_ms = int((time.time() - START) * 1000)
        with lock:
            rpc.append({"t_ms": recv_ms, "msg": msg})
            if "id" in msg and ("result" in msg or "error" in msg):
                pending[msg["id"]] = msg


def errreader():
    for line in proc.stderr:
        sys.stderr.write("[codex] " + line.decode("utf-8", "replace"))


threading.Thread(target=reader, daemon=True).start()
threading.Thread(target=errreader, daemon=True).start()

_next = [0]


def request(method, params, timeout=60, wait=True):
    _next[0] += 1
    rid = _next[0]
    proc.stdin.write((json.dumps({"jsonrpc": "2.0", "id": rid, "method": method,
                                  "params": params}) + "\n").encode("utf-8"))
    proc.stdin.flush()
    if not wait:
        return rid
    deadline = time.time() + timeout
    while time.time() < deadline:
        with lock:
            if rid in pending:
                return pending.pop(rid)
        time.sleep(0.05)
    return None


def notify(method, params):
    proc.stdin.write((json.dumps({"jsonrpc": "2.0", "method": method,
                                  "params": params}) + "\n").encode("utf-8"))
    proc.stdin.flush()


out = {}
out["initialize"] = request("initialize", {
    "clientInfo": {"name": "probe", "title": None, "version": "0.1.0"},
    "capabilities": None}, 30)
notify("initialized", {})
out["login"] = request("account/login/start", {"type": "apiKey", "apiKey": FAKE_KEY}, 30)
print("login -> " + json.dumps(out["login"])[:200], flush=True)


def thread_id_of(resp):
    result = (resp or {}).get("result") or {}
    return (result.get("thread") or {}).get("id") or result.get("threadId") or result.get("id")


out["threadA"] = request("thread/start", {"cwd": work_a, "sandbox": "read-only",
                                          "approvalPolicy": "never"}, 60)
out["threadB"] = request("thread/start", {"cwd": work_b, "sandbox": "read-only",
                                          "approvalPolicy": "never"}, 60)
tid_a = thread_id_of(out["threadA"])
tid_b = thread_id_of(out["threadB"])
print("threadA -> %s   threadB -> %s" % (tid_a, tid_b), flush=True)

if not tid_a or not tid_b:
    print("FAIL: could not start two threads on one process", flush=True)
    proc.kill()
    server.shutdown()
    sys.exit(1)

# 关键动作：两个 turn 几乎同时发出去，都不等对方结束。
request("turn/start", {"threadId": tid_a, "input": [{"type": "text", "text": "say A",
                                                      "text_elements": []}]}, wait=False)
request("turn/start", {"threadId": tid_b, "input": [{"type": "text", "text": "say B",
                                                      "text_elements": []}]}, wait=False)

time.sleep(6)

with lock:
    out["rpc"] = rpc[:]
with log_lock:
    out["http"] = http_log[:]

proc.kill()
server.shutdown()

dest = os.environ.get("PROBE_OUT") or os.path.join(tempfile.gettempdir(),
                                                    "probe-multi-thread.json")
io.open(dest, "w", encoding="utf-8").write(json.dumps(out, indent=2, ensure_ascii=False))

print("\n=== HTTP requests (overlap check) ===", flush=True)
for entry in out["http"]:
    print("  " + json.dumps(entry))

print("\n=== item/agentMessage/delta interleaving on stdout ===", flush=True)
deltas = [e for e in out["rpc"]
          if e["msg"].get("method") == "item/agentMessage/delta"]
for e in deltas:
    p = e["msg"]["params"]
    print("  t=%dms thread=%s delta=%r" % (e["t_ms"], p.get("threadId"), p.get("delta")))

seen_threads = []
for e in deltas:
    tid = e["msg"]["params"].get("threadId")
    if not seen_threads or seen_threads[-1] != tid:
        seen_threads.append(tid)
distinct_runs = len(seen_threads)
print("\ndistinct contiguous thread runs among deltas: %d (2 == pure interleave/overlap possible, "
      ">2 == actually interleaved, 1 might mean serialized or only one thread produced deltas)"
      % distinct_runs, flush=True)

print("wrote " + dest, flush=True)
