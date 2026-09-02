# -*- coding: utf-8 -*-
"""Record a real codex app-server conversation as test fixtures.

The adapter's unit tests replay these transcripts instead of spawning a process,
so they must come off the wire — not be written by hand from the schema, which
would only test our own assumptions.

Run this when bumping the pinned codex version:

    python capture-fixtures.py --codex <path to codex.exe> --base-url <relay>

It writes, next to the crate:

    protocol/<version>/            JSON Schema exported from that exact binary
    tests/fixtures/<scenario>.jsonl   raw framing, both directions
    tests/fixtures/manifest.json      what was captured, with which binary

The API key is read from the user's auth.json, passed to the server in-band via
`account/login/start`, and REDACTED from the transcript before it is written.
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

HERE = os.path.dirname(os.path.abspath(__file__))
CRATE = os.path.dirname(HERE)
FIXTURES = os.path.join(CRATE, "tests", "fixtures")

# Values that must never reach a committed fixture.
REDACTED = "<redacted>"
SECRET_KEYS = {"apiKey", "api_key", "token", "accessToken", "refreshToken", "idToken"}


def redact(node):
    """Deep-copy `node`, replacing any secret-looking value with a placeholder."""
    if isinstance(node, dict):
        return {
            k: (REDACTED if k in SECRET_KEYS and isinstance(v, str) else redact(v))
            for k, v in node.items()
        }
    if isinstance(node, list):
        return [redact(v) for v in node]
    return node


class Session:
    """A live app-server process plus a recording of everything crossing stdio."""

    def __init__(self, codex, codex_home, workdir, base_url, model):
        self.records = []
        self.lock = threading.Lock()
        self.pending = {}
        self.next_id = 0
        self.approvals = []
        self.turn_done = threading.Event()
        self.started_at = time.time()

        env = dict(os.environ)
        env["CODEX_HOME"] = codex_home
        env.pop("OPENAI_API_KEY", None)
        env.pop("CODEX_API_KEY", None)  # ignored by app-server anyway; drop it to prove that

        args = [
            codex, "app-server",
            "-c", 'model_provider="custom"',
            "-c", 'model_providers.custom.name="custom"',
            "-c", 'model_providers.custom.wire_api="responses"',
            "-c", "model_providers.custom.requires_openai_auth=true",
            "-c", 'model_providers.custom.base_url="%s"' % base_url,
            "-c", 'model="%s"' % model,
        ]
        self.proc = subprocess.Popen(
            args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, cwd=workdir, bufsize=0,
        )
        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()

    def _record(self, direction, msg):
        self.records.append({
            "t_ms": int((time.time() - self.started_at) * 1000),
            "dir": direction,
            "msg": redact(msg),
        })

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

    def wait_for(self, rid, timeout=60):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if rid in self.pending:
                return self.pending.pop(rid)
            time.sleep(0.05)
        return None

    def _read_stdout(self):
        for raw in iter(self.proc.stdout.readline, b""):
            try:
                msg = json.loads(raw.decode("utf-8", "replace"))
            except ValueError:
                self.records.append({"dir": "server->client", "unparsed": raw.decode("utf-8", "replace")})
                continue
            self._record("server->client", msg)
            method = msg.get("method")

            if method and "id" in msg:
                # A ServerRequest: it must be answered or the turn stalls.
                if method.endswith("/requestApproval") or method.endswith("Approval"):
                    self.approvals.append(msg)
                    self.send({"jsonrpc": "2.0", "id": msg["id"], "result": {"decision": "decline"}})
                else:
                    self.send({"jsonrpc": "2.0", "id": msg["id"], "result": {}})
            elif "id" in msg:
                self.pending[msg["id"]] = msg

            if method in ("turn/completed", "turn/failed", "error"):
                self.turn_done.set()

    def _read_stderr(self):
        for raw in iter(self.proc.stderr.readline, b""):
            text = raw.decode("utf-8", "replace").rstrip()
            if text:
                self.records.append({"dir": "stderr", "text": text})

    def close(self):
        try:
            self.proc.terminate()
        except OSError:
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


def make_dirs():
    root = tempfile.mkdtemp(prefix="codex-fixture-")
    codex_home = os.path.join(root, "home")   # private: the user's ~/.codex is never touched
    workdir = os.path.join(root, "work")
    os.makedirs(codex_home)
    os.makedirs(workdir)
    io.open(os.path.join(workdir, "README.md"), "w", encoding="utf-8").write("fixture workspace\n")
    return codex_home, workdir


def handshake(s, api_key):
    """initialize + initialized + login. Returns False if any step failed."""
    rid = s.request("initialize", {
        "clientInfo": {"name": "cofly-workbench", "title": None, "version": "0.1.0"},
        "capabilities": None,
    })
    if not (s.wait_for(rid, 30) or {}).get("result"):
        return False
    s.send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
    rid = s.request("account/login/start", {"type": "apiKey", "apiKey": api_key})
    return bool((s.wait_for(rid, 30) or {}).get("result"))


def capture_invalid_cwd(args, api_key):
    """thread/start against a path that does not exist.

    Answers: does a bad cwd come back as a JSON-RPC error response (which our
    classifier can see), or as something else entirely?
    """
    codex_home, _ = make_dirs()
    s = Session(args.codex, codex_home, os.path.dirname(codex_home), args.base_url, args.model)
    if not handshake(s, api_key):
        s.close()
        sys.exit("handshake failed")

    bogus = os.path.join(codex_home, "no", "such", "directory")
    rid = s.request("thread/start", {
        "cwd": bogus, "sandbox": "read-only", "approvalPolicy": "on-request",
    })
    resp = s.wait_for(rid, 60)
    time.sleep(1)
    s.close()

    write_fixture("invalid-cwd", s.records, {
        "codexVersion": args.version,
        "scenario": "invalid-cwd",
        "gotErrorResponse": bool(resp and "error" in resp),
        "errorCode": ((resp or {}).get("error") or {}).get("code"),
        "errorMessage": ((resp or {}).get("error") or {}).get("message"),
        "records": len(s.records),
    })


def capture_bad_credentials(args):
    """Run a turn with a deliberately wrong API key.

    Answers the question our error classifier depends on: does an upstream 401
    arrive as a JSON-RPC error response, or as a notification (turn/failed,
    error) that never reaches the classifier at all?
    """
    codex_home, workdir = make_dirs()
    s = Session(args.codex, codex_home, workdir, args.base_url, args.model)
    if not handshake(s, "sk-deliberately-invalid-key-for-fixtures"):
        s.close()
        sys.exit("handshake failed")

    rid = s.request("thread/start", {
        "cwd": workdir, "sandbox": "read-only", "approvalPolicy": "never",
    })
    resp = s.wait_for(rid, 60) or {}
    result = resp.get("result") or {}
    thread_id = (result.get("thread") or {}).get("id") or result.get("threadId")

    turn_error = None
    if thread_id:
        rid = s.request("turn/start", {
            "threadId": thread_id,
            "input": [{"type": "text", "text": "say hi", "text_elements": []}],
        })
        turn_error = s.wait_for(rid, 60)
        s.turn_done.wait(90)
        time.sleep(1)

    s.close()

    # How did the failure actually surface?
    error_responses = [r for r in s.records
                       if r.get("dir") == "server->client" and "error" in (r.get("msg") or {})]
    failure_notes = sorted({
        (r.get("msg") or {}).get("method") for r in s.records
        if r.get("dir") == "server->client"
        and (r.get("msg") or {}).get("method") in ("error", "turn/failed", "turn/completed")
    })

    write_fixture("bad-credentials", s.records, {
        "codexVersion": args.version,
        "scenario": "bad-credentials",
        "threadStarted": bool(thread_id),
        "jsonRpcErrorResponses": len(error_responses),
        "turnStartWasError": bool(turn_error and "error" in turn_error),
        "failureNotifications": [m for m in failure_notes if m],
        "records": len(s.records),
    })


def capture(args):
    api_key = json.load(io.open(args.auth, encoding="utf-8")).get("OPENAI_API_KEY", "")
    if not api_key:
        sys.exit("no OPENAI_API_KEY in %s" % args.auth)

    if args.scenario == "invalid-cwd":
        return capture_invalid_cwd(args, api_key)
    if args.scenario == "bad-credentials":
        return capture_bad_credentials(args)

    root = tempfile.mkdtemp(prefix="codex-fixture-")
    codex_home = os.path.join(root, "home")   # private: the user's ~/.codex is never touched
    workdir = os.path.join(root, "work")
    os.makedirs(codex_home)
    os.makedirs(workdir)
    io.open(os.path.join(workdir, "README.md"), "w", encoding="utf-8").write("fixture workspace\n")

    s = Session(args.codex, codex_home, workdir, args.base_url, args.model)

    rid = s.request("initialize", {
        "clientInfo": {"name": "cofly-workbench", "title": None, "version": "0.1.0"},
        "capabilities": None,
    })
    if not (s.wait_for(rid, 30) or {}).get("result"):
        s.close()
        sys.exit("initialize failed")
    s.send({"jsonrpc": "2.0", "method": "initialized", "params": {}})

    rid = s.request("account/login/start", {"type": "apiKey", "apiKey": api_key})
    if not (s.wait_for(rid, 30) or {}).get("result"):
        s.close()
        sys.exit("account/login/start failed")

    # read-only + on-request is the combination that forces an approval round-trip.
    rid = s.request("thread/start", {
        "cwd": workdir, "sandbox": "read-only", "approvalPolicy": "on-request",
    })
    resp = s.wait_for(rid, 60) or {}
    result = resp.get("result") or {}
    thread_id = (result.get("thread") or {}).get("id") or result.get("threadId") or result.get("id")
    if not thread_id:
        s.close()
        sys.exit("thread/start returned no thread id: %s" % json.dumps(resp)[:300])

    marker = os.path.join(workdir, args.marker)
    s.request("turn/start", {
        "threadId": thread_id,
        "input": [{"type": "text", "text": args.prompt, "text_elements": []}],
    })
    s.turn_done.wait(args.turn_timeout)
    time.sleep(2)  # let trailing notifications land

    summary = {
        "codexVersion": args.version,
        "codexBinary": args.codex,
        "scenario": args.scenario,
        "approvalRequestsSeen": len(s.approvals),
        "approvalMethods": sorted({a.get("method") for a in s.approvals}),
        "declinedCommandDidNotRun": not os.path.exists(marker),
        "records": len(s.records),
    }
    s.close()
    write_fixture(args.scenario, s.records, summary)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--codex", required=True, help="path to codex.exe")
    p.add_argument("--version", required=True, help="version string of that binary, e.g. 0.144.2")
    p.add_argument("--base-url", required=True)
    p.add_argument("--model", default="gpt-5.5")
    p.add_argument("--auth", default=os.path.expanduser("~/.codex/auth.json"))
    p.add_argument("--scenario", default="decline-command-approval")
    # 提问决定 agent 会走哪条工具路径，从而决定触发哪一类审批。
    p.add_argument("--prompt",
                   default="Run this exact shell command and nothing else: "
                           "cmd /c echo pwned > SHOULD_NOT_EXIST.txt")
    # 拒绝之后必须确认「这个文件没被建出来」——审批不生效的话它会存在。
    p.add_argument("--marker", default="SHOULD_NOT_EXIST.txt")
    p.add_argument("--turn-timeout", type=int, default=180)
    capture(p.parse_args())


if __name__ == "__main__":
    main()
