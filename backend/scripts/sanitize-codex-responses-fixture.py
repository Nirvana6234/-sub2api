#!/usr/bin/env python3
"""把 probe-local-proxy.py 的原始转储脱敏成可入库的 Codex /v1/responses 请求 fixture。

为什么需要这一步：原始转储里有真实的 installation_id、会话/线程/回合 UUID、本机
工作目录路径，以及 17KB 的官方 system instructions。前三类是标识，不能进仓库；
最后一类是上游的散文，我们的代码一个字节都不读它，留着只会让人以为可以改。

用法：
    python probe-local-proxy.py <codex-app-server.exe>          # PROBE_OUT=dump.json
    python backend/scripts/sanitize-codex-responses-fixture.py \
        dump.json backend/internal/service/testdata/codex_responses_request.json

**保真边界（改这个脚本时务必同步更新 fixture 里的 _notes）：**

  逐字节保留 —— tools（含 namespace 型工具与 parameters schema）、字段顺序、
                 store/stream/include/reasoning/tool_choice/parallel_tool_calls、
                 client_metadata 的键集合与嵌套 turn-metadata 的键集合。
  替换       —— 所有标识 UUID、prompt_cache_key、input 里的 message id、
                 environment_context 里的本机路径。
  截断       —— instructions，以及 developer 那条 skills 长文；都留下显式标记。

这条边界就是这份 fixture 能证明什么的边界。拿它断言「未截断字段逐字段不变」是成立
的；拿它断言「请求体逐字节等于真实 Codex 报文」不成立。
"""
import io
import json
import re
import sys

# 固定占位符：形状必须仍是合法 UUID（指纹收敛那条链路会 uuid.Parse 它们）。
INSTALLATION_ID = "11111111-1111-4111-8111-111111111111"
SESSION_ID = "22222222-2222-4222-8222-222222222222"
TURN_ID = "33333333-3333-4333-8333-333333333333"
CONTEXT_WINDOW_ID = "44444444-4444-4444-8444-444444444444"
PROMPT_CACHE_KEY = "55555555-5555-4555-8555-555555555555"
# 兜底占位符：没被语义映射认领的 UUID 都换成它。出现在 fixture 里就说明有一类标识
# build_id_map 还没覆盖——升级 codex 后如果它变多了，先查是不是新增了标识字段。
UNKNOWN_ID = "99999999-9999-4999-8999-999999999999"
FIXTURE_CWD = "C:\\\\workspace\\\\fixture"

INSTRUCTIONS_KEEP = 240
DEVELOPER_TEXT_KEEP = 240
TRUNCATION_MARK = "\n\n[fixture: truncated, see backend/scripts/sanitize-codex-responses-fixture.py]"

UUID_RE = re.compile(
    r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
)


def build_id_map(body):
    """从真实报文里收集标识值，映射到固定占位符。

    按语义分组而不是逐个替换：session 与 thread 在真实报文里是同一个值，turn 与
    root_turn 也是——把它们映射成同一个占位符，才能保住「哪些字段本来相等」这条
    信息。丢了它，按 thread-id 选分组之类的逻辑就没法用这份 fixture 验。
    """
    metadata = body.get("client_metadata") or {}
    raw_turn_metadata = metadata.get("x-codex-turn-metadata") or "{}"
    try:
        turn_metadata = json.loads(raw_turn_metadata)
    except ValueError:
        turn_metadata = {}

    mapping = {}

    def add(value, placeholder):
        # 先写为准。真实报文里 prompt_cache_key == session_id == thread_id（实测
        # 0.153.0），后写覆盖会让这个值被标成最后登记的那个语义——全部 session/thread
        # 出现处都变成 prompt-cache 占位符，读 fixture 的人会以为 Codex 在到处塞
        # 缓存键。先写为准则保住「这三个字段本来就是同一个值」这条事实。
        if isinstance(value, str) and value and value not in mapping:
            mapping[value] = placeholder

    add(metadata.get("x-codex-installation-id"), INSTALLATION_ID)
    add(turn_metadata.get("installation_id"), INSTALLATION_ID)
    add(metadata.get("session_id"), SESSION_ID)
    add(metadata.get("thread_id"), SESSION_ID)
    add(turn_metadata.get("session_id"), SESSION_ID)
    add(turn_metadata.get("thread_id"), SESSION_ID)
    add(metadata.get("turn_id"), TURN_ID)
    add(metadata.get("root_turn_id"), TURN_ID)
    add(turn_metadata.get("turn_id"), TURN_ID)
    add(turn_metadata.get("root_turn_id"), TURN_ID)
    add(turn_metadata.get("context_window_id"), CONTEXT_WINDOW_ID)
    add(body.get("prompt_cache_key"), PROMPT_CACHE_KEY)
    return mapping


def replace_ids(text, mapping):
    """单遍替换：认识的 UUID 换成语义占位符，不认识的一律抹成 UNKNOWN_ID。

    必须是**一遍**。先逐个 str.replace 再跑兜底正则会把刚插进去的占位符自己也当成
    「漏网 UUID」抹掉——脱敏检查照样全绿（确实没有真实标识了），但 installation /
    turn / context_window 全部塌成同一个值，「哪些字段本来相等」这条信息静默丢失。
    """
    return UUID_RE.sub(lambda m: mapping.get(m.group(0), UNKNOWN_ID), text)


def scrub_local_paths(text):
    """抹掉本机绝对路径。environment_context 里的 cwd 会带出用户名与临时目录名。"""
    text = re.sub(r"C:\\\\Users\\\\[^\\\\\"<\s]+(?:\\\\[^\"<\s]*)?", FIXTURE_CWD, text)
    text = re.sub(r"C:\\Users\\[^\\\"<\s]+(?:\\[^\"<\s]*)?", "C:\\\\workspace\\\\fixture", text)
    text = re.sub(r"/(?:home|Users)/[^\"<\s]+", "/workspace/fixture", text)
    return text


def truncate(text, keep):
    if len(text) <= keep:
        return text, False
    return text[:keep].rstrip() + TRUNCATION_MARK, True


def sanitize(dump):
    posts = [e for e in dump.get("http", []) if e.get("method") == "POST"]
    if not posts:
        raise SystemExit("转储里没有 POST 请求——codex 那一轮没发出去")
    request = posts[0]
    body = request["body"]
    if not isinstance(body, dict):
        raise SystemExit("请求体不是 JSON 对象，无法脱敏")

    mapping = build_id_map(body)
    truncated = []

    def walk(value):
        if isinstance(value, dict):
            return {k: walk(v) for k, v in value.items()}
        if isinstance(value, list):
            return [walk(v) for v in value]
        if isinstance(value, str):
            return scrub_local_paths(replace_ids(value, mapping))
        return value

    clean = walk(body)

    original_instructions = len(body.get("instructions") or "")
    if isinstance(clean.get("instructions"), str):
        clean["instructions"], cut = truncate(clean["instructions"], INSTRUCTIONS_KEEP)
        if cut:
            truncated.append("instructions (原 %d 字符)" % original_instructions)

    for index, item in enumerate(clean.get("input") or []):
        if not isinstance(item, dict):
            continue
        item["id"] = "msg_%s-%04d" % (SESSION_ID, index)
        for part in item.get("content") or []:
            if not (isinstance(part, dict) and isinstance(part.get("text"), str)):
                continue
            if item.get("role") != "developer":
                continue
            before = len(part["text"])
            part["text"], cut = truncate(part["text"], DEVELOPER_TEXT_KEEP)
            if cut:
                truncated.append("input[%d].content[].text (原 %d 字符)" % (index, before))

    headers = {
        k: replace_ids(scrub_local_paths(v), mapping)
        for k, v in (request.get("headers") or {}).items()
        if k not in ("authorization", "cookie", "host", "content-length")
    }

    return {
        "_notes": (
            "probe-local-proxy.py 录的真实 Codex /v1/responses 报文，经 "
            "backend/scripts/sanitize-codex-responses-fixture.py 脱敏。"
            "保真边界见该脚本头部：tools、字段顺序、store/stream/include/reasoning "
            "与 client_metadata 的键集合逐字节保留；标识 UUID 与本机路径被替换成固定"
            "占位符；instructions 与 developer 长文被截断。"
            "**能用它断言「未截断字段逐字段不变」，不能用它断言「等于真实报文的字节」。**"
            "升级 codex 必须重录。"
        ),
        "_truncated": truncated,
        "_originalBodyBytes": request["headers"].get("content-length"),
        "method": request["method"],
        "path": request["path"],
        "headers": headers,
        "body": clean,
    }


def main():
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    dump = json.load(io.open(sys.argv[1], encoding="utf-8"))
    result = sanitize(dump)
    with io.open(sys.argv[2], "w", encoding="utf-8", newline="\n") as handle:
        json.dump(result, handle, indent=2, ensure_ascii=False)
        handle.write("\n")
    print("wrote %s (%d truncated fields)" % (sys.argv[2], len(result["_truncated"])))


if __name__ == "__main__":
    main()
