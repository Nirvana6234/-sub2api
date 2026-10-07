"""本机主从联调驱动：登录后台、打开主从分流、建分组/账号/Key、激活从节点。状态存 state.json。"""
import json
import re
import sys
import urllib.request

MASTER = "http://127.0.0.1:18080"
import os

# 状态和凭据都放在仓库根目录下被忽略的 .local/relay-e2e（见 docs/RELAY_LOCAL_TESTING.md），脚本本身可以提交。
ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
HERE = os.path.join(ROOT, ".local", "relay-e2e")
STATE = HERE + "/state.json"


def secret(name):
    text = open(HERE + "/secrets.ps1", encoding="utf-8").read()
    return re.search(r"\$env:%s = '([^']*)'" % name, text).group(1)


def call(method, path, body=None, token=None, base=MASTER):
    req = urllib.request.Request(base + path, method=method,
                                 data=None if body is None else json.dumps(body).encode())
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, raw.decode(errors="replace")


def load():
    try:
        return json.load(open(STATE))
    except Exception:
        return {}


def save(s):
    json.dump(s, open(STATE, "w"), indent=2)


def login():
    st, r = call("POST", "/api/v1/auth/login", {"email": secret("ADMIN_EMAIL"), "password": secret("ADMIN_PASSWORD")})
    assert st == 200, (st, r)
    tok = r["data"]["access_token"]
    # 新库第一次登录要先确认管理员合规声明（只对这个本机临时管理员）。
    accept_compliance(tok)
    return tok


def accept_compliance(tok):
    st, r = call("GET", "/api/v1/admin/compliance", None, tok)
    assert st == 200, (st, r)
    if r["data"].get("acknowledged"):
        return
    st, r2 = call("POST", "/api/v1/admin/compliance/accept", {"phrase": r["data"]["ack_phrase_zh"], "language": "zh"}, tok)
    print("compliance accept (temp e2e admin):", st)


def setup():
    s = load()
    tok = login()
    accept_compliance(tok)
    st, r = call("PUT", "/api/v1/admin/relay/enabled", {"enabled": True}, tok)
    print("enable relay:", st, r if st != 200 else "ok")
    if "group_id" not in s:
        st, r = call("POST", "/api/v1/admin/groups", {"name": "relay-e2e", "platform": "openai", "rate_multiplier": 1,
                                                      "subscription_type": "standard"}, tok)
        assert st == 200, (st, r)
        s["group_id"] = r["data"]["id"]
    if "account_id" not in s:
        st, r = call("POST", "/api/v1/admin/accounts", {
            "name": "fake-upstream", "platform": "openai", "type": "apikey", "concurrency": 5,
            "credentials": {"api_key": "sk-fake-upstream", "base_url": "http://127.0.0.1:18090"},
            "group_ids": [s["group_id"]]}, tok)
        assert st == 200, (st, r)
        s["account_id"] = r["data"]["id"]
    st, me = call("GET", "/api/v1/auth/me", None, tok)
    s["user_id"] = me["data"]["id"]
    if "api_key" not in s:
        st, r = call("POST", "/api/v1/admin/users/%d/balance" % s["user_id"], {"balance": 5, "operation": "set"}, tok)
        assert st == 200, (st, r)
        st, r = call("POST", "/api/v1/keys", {"name": "relay-e2e", "group_id": s["group_id"]}, tok)
        assert st == 200, (st, r)
        s["api_key"] = r["data"]["key"]
    save(s)
    print("setup done: group", s["group_id"], "account", s["account_id"], "user", s["user_id"])


def activate():
    tok = login()
    st, r = call("GET", "/api/v1/admin/relay/nodes", None, tok)
    assert st == 200, (st, r)
    for n in r["data"]:
        print("node", n.get("id"), n.get("status"), n.get("identity_fingerprint", "")[:16])
        if n.get("status") == "pending":
            st2, r2 = call("POST", "/api/v1/admin/relay/nodes/%d/activate" % n["id"], {
                "fingerprint": n["identity_fingerprint"], "name": "local-node", "public_domain": "relay1.localhost",
                # 本机没有真实域名：解析检查不过，明确选择"仍然激活"。
                "ignore_dns_mismatch": True}, tok)
            print("activate:", st2, r2 if st2 != 200 else "ok")


def status():
    tok = login()
    print(json.dumps(call("GET", "/api/v1/admin/relay/status", None, tok)[1], indent=1, ensure_ascii=False)[:3000])
    st, u = call("GET", "/api/v1/auth/me", None, tok)
    print("balance:", u["data"].get("balance"))


def fingerprint():
    """把主节点的主从通信根证书指纹写到 .local/relay-e2e/root-fingerprint.txt（start-node.ps1 读它）。"""
    tok = login()
    st, r = call("GET", "/api/v1/admin/relay/status", None, tok)
    assert st == 200, (st, r)
    fps = r["data"]["runtime"].get("root_fingerprints") or []
    if not fps:
        raise SystemExit("主节点还没有根证书指纹：运行状态 = %s，原因 = %s" % (r["data"]["runtime"].get("state"), r["data"]["runtime"].get("reason")))
    open(HERE + "/root-fingerprint.txt", "w").write(",".join(fps))
    print("root fingerprint:", ",".join(fps))


def route():
    """本机联调的分配设置：关掉外部探测（本机没有公网 HTTPS）、主节点比例 0，再把还没分配的 Key 分给从节点。"""
    tok = login()
    st, c = call("GET", "/api/v1/admin/relay/general-config", None, tok)
    assert st == 200, (st, c)
    cfg = c["data"]
    cfg["probe_enabled"] = False
    cfg["master_ratio_percent"] = 0
    st, r = call("PUT", "/api/v1/admin/relay/general-config", cfg, tok)
    print("general config:", st, "ok" if st == 200 else r)
    st, r = call("POST", "/api/v1/admin/relay/api-keys/assign-unassigned", None, tok)
    print("assign unassigned keys:", st, r.get("data") if isinstance(r, dict) else r)
    # setup 建 Key 时从节点还没激活，Key 会落在主节点上：把测试 Key 明确移到第一台在服务的从节点。
    st, nodes = call("GET", "/api/v1/admin/relay/nodes", None, tok)
    active = [n["id"] for n in nodes.get("data", []) if n.get("status") == "active"] if isinstance(nodes, dict) else []
    state = load()
    if active and state.get("api_key"):
        st, keys = call("GET", "/api/v1/keys?page=1&page_size=100", None, tok)
        ids = [k["id"] for k in keys["data"]["items"] if k.get("key") == state["api_key"]] if st == 200 else []
        if ids:
            st, r = call("POST", "/api/v1/admin/relay/api-keys/move", {"key_ids": ids, "node_id": active[0]}, tok)
            print("move key to node", active[0], ":", st, r.get("data") if isinstance(r, dict) else r)
    else:
        print("没有在服务的从节点，先运行 activate")
    st, r = call("GET", "/api/v1/admin/relay/api-keys/assignment", None, tok)
    print("key assignment:", json.dumps(r.get("data") if isinstance(r, dict) else r))


if __name__ == "__main__":
    {"setup": setup, "activate": activate, "status": status, "fingerprint": fingerprint, "route": route}[sys.argv[1]]()
