"""Record exactly what codex sends when base_url points at a loopback HTTP server.

Answers, in one run:
  1. is plain http:// on loopback accepted at all?
  2. does account/login/start take an arbitrary opaque string as the apiKey?
  3. what path does codex append to base_url?
  4. what headers does it send?
  5. does it call anything besides the turn (a startup GET /models, say)?
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

DEFAULT_CODEX = "C:/Users/Borg/AppData/Local/OpenAI/Codex/bin/3135b80b111fd431/codex.exe"
CODEX = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_CODEX
FAKE_KEY = "cofly-local-proxy-token-0123456789abcdef"

http_log = []
log_lock = threading.Lock()

SSE = [
    ("response.created", {"type": "response.created", "response": {
        "id": "resp_probe", "object": "response", "created_at": 0,
        "status": "in_progress", "model": "gpt-5", "output": []}}),
    ("response.output_item.added", {"type": "response.output_item.added", "output_index": 0,
        "item": {"id": "msg_probe", "type": "message", "role": "assistant",
                 "status": "in_progress", "content": []}}),
    ("response.content_part.added", {"type": "response.content_part.added",
        "item_id": "msg_probe", "output_index": 0, "content_index": 0,
        "part": {"type": "output_text", "text": "", "annotations": []}}),
    ("response.output_text.delta", {"type": "response.output_text.delta",
        "item_id": "msg_probe", "output_index": 0, "content_index": 0, "delta": "PROBE-OK"}),
    ("response.output_text.done", {"type": "response.output_text.done",
        "item_id": "msg_probe", "output_index": 0, "content_index": 0, "text": "PROBE-OK"}),
    ("response.content_part.done", {"type": "response.content_part.done",
        "item_id": "msg_probe", "output_index": 0, "content_index": 0,
        "part": {"type": "output_text", "text": "PROBE-OK", "annotations": []}}),
    ("response.output_item.done", {"type": "response.output_item.done", "output_index": 0,
        "item": {"id": "msg_probe", "type": "message", "role": "assistant", "status": "completed",
                 "content": [{"type": "output_text", "text": "PROBE-OK", "annotations": []}]}}),
    ("response.completed", {"type": "response.completed", "response": {
        "id": "resp_probe", "object": "response", "created_at": 0, "status": "completed",
        "model": "gpt-5",
        "output": [{"id": "msg_probe", "type": "message", "role": "assistant",
                    "status": "completed",
                    "content": [{"type": "output_text", "text": "PROBE-OK",
                                 "annotations": []}]}],
        "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}),
]


class Recorder(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _record(self, body):
        with log_lock:
            http_log.append({
                "t_ms": int((time.time() - START) * 1000),
                "method": self.command,
                "path": self.path,
                "headers": {k.lower(): v for k, v in self.headers.items()},
                "body": body,
            })

    def _read_body(self):
        n = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(n) if n else b""
        try:
            return json.loads(raw.decode("utf-8"))
        except Exception:
            return raw.decode("utf-8", "replace")

    def do_POST(self):
        self._record(self._read_body())
        BaseHTTPRequestHandler.send_response(self, 200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-store")
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()
        for name, payload in SSE:
            chunk = ("event: %s\ndata: %s\n\n" % (name, json.dumps(payload))).encode("utf-8")
            self.wfile.write(("%x\r\n" % len(chunk)).encode("ascii") + chunk + b"\r\n")
            self.wfile.flush()
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

    def do_GET(self):
        self._record(None)
        body = json.dumps({"object": "list",
                           "data": [{"id": "gpt-5", "object": "model"}]}).encode("utf-8")
        BaseHTTPRequestHandler.send_response(self, 200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


START = time.time()
server = ThreadingHTTPServer(("127.0.0.1", 0), Recorder)
PORT = server.server_address[1]
threading.Thread(target=server.serve_forever, daemon=True).start()
BASE_URL = "http://127.0.0.1:%d/v1" % PORT
print("recording server on " + BASE_URL, flush=True)

root = tempfile.mkdtemp(prefix="probe-proxy-")
codex_home = os.path.join(root, "home")
workdir = os.path.join(root, "work")
os.makedirs(codex_home)
os.makedirs(workdir)
io.open(os.path.join(workdir, "README.md"), "w", encoding="utf-8").write("probe\n")

env = dict(os.environ)
env["CODEX_HOME"] = codex_home
env.pop("OPENAI_API_KEY", None)
env.pop("CODEX_API_KEY", None)

proc = subprocess.Popen(
    [CODEX, "app-server",
     "-c", 'model_provider="custom"',
     "-c", 'model_providers.custom.name="custom"',
     "-c", 'model_providers.custom.wire_api="responses"',
     "-c", "model_providers.custom.requires_openai_auth=true",
     "-c", 'model_providers.custom.base_url="%s"' % BASE_URL,
     "-c", 'model="gpt-5"'],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    env=env, cwd=workdir, bufsize=0)

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
        with lock:
            rpc.append(msg)
            if "id" in msg and ("result" in msg or "error" in msg):
                pending[msg["id"]] = msg


def errreader():
    for line in proc.stderr:
        sys.stderr.write("[codex] " + line.decode("utf-8", "replace"))


threading.Thread(target=reader, daemon=True).start()
threading.Thread(target=errreader, daemon=True).start()

_next = [0]


def request(method, params, timeout=60):
    _next[0] += 1
    rid = _next[0]
    proc.stdin.write((json.dumps({"jsonrpc": "2.0", "id": rid, "method": method,
                                  "params": params}) + "\n").encode("utf-8"))
    proc.stdin.flush()
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
print("login -> " + json.dumps(out["login"])[:400], flush=True)

auth_path = os.path.join(codex_home, "auth.json")
out["authFileAfterLogin"] = (io.open(auth_path, encoding="utf-8").read()
                             if os.path.exists(auth_path) else None)

out["threadStart"] = request("thread/start", {"cwd": workdir, "sandbox": "read-only",
                                              "approvalPolicy": "never"}, 60)
result = (out["threadStart"] or {}).get("result") or {}
thread_id = ((result.get("thread") or {}).get("id") or result.get("threadId")
             or result.get("id"))
print("thread -> " + str(thread_id), flush=True)

if thread_id:
    request("turn/start", {"threadId": thread_id,
                           "input": [{"type": "text", "text": "say hello",
                                      "text_elements": []}]}, 30)
    time.sleep(8)

with lock:
    out["rpc"] = rpc[:]
with log_lock:
    out["http"] = http_log[:]

proc.kill()
server.shutdown()

dest = os.path.join(os.path.dirname(os.path.abspath(__file__)), "probe-local-proxy.json")
io.open(dest, "w", encoding="utf-8").write(json.dumps(out, indent=2, ensure_ascii=False))
print("\n=== HTTP requests codex made: %d ===" % len(out["http"]), flush=True)
for entry in out["http"]:
    print("  %s %s" % (entry["method"], entry["path"]))
print("wrote " + dest, flush=True)
