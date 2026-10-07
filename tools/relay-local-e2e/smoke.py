"""WP16-19 后的本机冒烟：状态、节点健康、经从节点发请求、排空/取消排空、用户中转状态、按节点的使用记录、Key 地址。"""
import json
import sys
import time
import urllib.request

import os

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import drive  # noqa: E402

NODE = "http://127.0.0.1:18081"


def show(label, st, body, n=400):
    print("==", label, st, json.dumps(body, ensure_ascii=False)[:n] if not isinstance(body, str) else body[:n])


tok = drive.login()
s = drive.load()
st, r = drive.call("GET", "/api/v1/admin/relay/status", None, tok)
show("status", st, r["data"]["runtime"])
st, r = drive.call("GET", "/api/v1/admin/relay/nodes", None, tok)
nodes = r["data"]
show("nodes", st, [(n["id"], n["status"], n["public_domain"]) for n in nodes])
nid = next((n["id"] for n in nodes if n["status"] in ("active", "draining")), None)
if nid is None:
    drive.activate()
    st, r = drive.call("GET", "/api/v1/admin/relay/nodes", None, tok)
    nid = r["data"][0]["id"]
    time.sleep(8)

st, r = drive.call("GET", "/api/v1/admin/relay/nodes/health", None, tok)
show("health", st, r["data"], 700)

# 经从节点的一次请求
req = urllib.request.Request(NODE + "/v1/responses", method="POST",
                             data=json.dumps({"model": "gpt-5", "input": "hi"}).encode())
req.add_header("Content-Type", "application/json")
req.add_header("Authorization", "Bearer " + s["api_key"])
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
try:
    with opener.open(req, timeout=40) as resp:
        print("== via node:", resp.status, resp.read()[:160])
except Exception as e:  # noqa: BLE001
    print("== via node failed:", e, getattr(e, "read", lambda: b"")()[:300])

st, r = drive.call("GET", "/api/v1/admin/relay/api-keys/assignment", None, tok)
show("key assignment", st, r["data"])
st, r = drive.call("GET", "/api/v1/admin/relay/users/assignment", None, tok)
show("user assignment", st, r["data"])
st, r = drive.call("GET", "/api/v1/admin/relay/users/%d" % s["user_id"], None, tok)
show("user relay state", st, r["data"])
st, r = drive.call("GET", "/api/v1/keys?page=1&page_size=5", None, tok)
if st == 200:
    print("== key addresses:", [(k["id"], k.get("relay_node_id"), k.get("relay_base_url")) for k in r["data"]["items"]])

st, r = drive.call("POST", "/api/v1/admin/relay/nodes/%d/drain" % nid, None, tok)
show("drain", st, r)
time.sleep(7)
st, r = drive.call("GET", "/api/v1/admin/relay/nodes", None, tok)
show("after drain", st, [(n["id"], n["status"]) for n in r["data"]])
st, r = drive.call("POST", "/api/v1/admin/relay/nodes/%d/undrain" % nid, None, tok)
show("undrain", st, r)

for q in ("master", str(nid)):
    st, r = drive.call("GET", "/api/v1/admin/usage?page=1&page_size=3&node_id=" + q, None, tok)
    items = r["data"]["items"] if st == 200 else r
    show("usage node_id=" + q, st, [(i["id"], i.get("node_id")) for i in items] if st == 200 else items)
st, r = drive.call("GET", "/api/v1/admin/relay/logs?limit=3", None, tok)
show("logs", st, {"nodes": [(n["node_name"], n["status"], len(n["records"])) for n in r["data"]["nodes"]]} if st == 200 else r)
st, r = drive.call("GET", "/api/v1/admin/relay/notifications", None, tok)
show("notifications", st, {"events": len(r["data"]["events"])} if st == 200 else r)
