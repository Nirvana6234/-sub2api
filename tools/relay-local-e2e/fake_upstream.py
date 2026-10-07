"""本机主从联调用的假上游（OpenAI Responses / Chat Completions），监听 127.0.0.1:18090。"""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

COUNTER = {"n": 0}


def response_obj(rid, stream_text="hello from fake upstream"):
    return {
        "id": rid, "object": "response", "status": "completed", "model": "gpt-5",
        "output": [{"type": "message", "role": "assistant",
                    "content": [{"type": "output_text", "text": stream_text}]}],
        "usage": {"input_tokens": 1200, "output_tokens": 300, "total_tokens": 1500},
    }


class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(n) or b"{}")
        COUNTER["n"] += 1
        rid = "resp_fake%04d" % COUNTER["n"]
        auth = self.headers.get("Authorization", "")
        print("POST", self.path, "auth=", auth[:20], "stream=", body.get("stream"), flush=True)
        if "cyber-trigger" in json.dumps(body):
            raw = json.dumps({"error": {"code": "cyber_policy", "message": "blocked by fake cyber policy",
                                        "type": "invalid_request_error"}}).encode()
            self.send_response(400)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        if self.path.endswith("/chat/completions"):
            out = {"id": "chatcmpl-%d" % COUNTER["n"], "object": "chat.completion", "model": "gpt-5",
                   "choices": [{"index": 0, "message": {"role": "assistant", "content": "hello"}, "finish_reason": "stop"}],
                   "usage": {"prompt_tokens": 50, "completion_tokens": 10, "total_tokens": 60}}
            return self.send_json(out)
        if body.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            events = [
                ("response.created", {"type": "response.created", "response": {"id": rid, "status": "in_progress"}}),
                ("response.output_text.delta", {"type": "response.output_text.delta", "delta": "hello"}),
                ("response.completed", {"type": "response.completed", "response": response_obj(rid)}),
            ]
            for name, data in events:
                self.wfile.write(("event: %s\ndata: %s\n\n" % (name, json.dumps(data))).encode())
                self.wfile.flush()
            return
        self.send_json(response_obj(rid))

    def send_json(self, obj):
        raw = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 18090), H).serve_forever()
