#!/usr/bin/env python3
"""Read-only CPA deployment checks; never print secrets or generate model output."""

import argparse
import hashlib
import http.client
import json
from pathlib import Path
import re
import socket
import ssl
import subprocess
import sys
from urllib.request import ProxyHandler, Request, build_opener


class CheckFailed(Exception):
    pass


def require(condition, message):
    if not condition:
        raise CheckFailed(message)


def docker_inspect(name):
    result = subprocess.run(
        ["docker", "inspect", name], capture_output=True, text=True, timeout=15
    )
    require(result.returncode == 0, "Container inspection failed")
    value = json.loads(result.stdout)[0]
    require(value["State"]["Running"], "Required container is not running")
    return value


def local_get(path, key=None, return_headers=False):
    headers = {"Authorization": "Bearer " + key} if key else {}
    request = Request("http://127.0.0.1:8317" + path, headers=headers)
    with build_opener(ProxyHandler({})).open(request, timeout=15) as response:
        require(response.status == 200, "Local HTTP check failed")
        body = response.read()
        if return_headers:
            return body, {name.lower(): value for name, value in response.headers.items()}
        return body


class LocalTLSConnection(http.client.HTTPSConnection):
    def connect(self):
        # Preserve certificate verification and SNI without public-IP hairpin routing.
        connection = socket.create_connection(("127.0.0.1", 443), self.timeout)
        self.sock = self._context.wrap_socket(connection, server_hostname=self.host)


def tls_get(domain, path, key=None):
    connection = LocalTLSConnection(domain, timeout=15, context=ssl.create_default_context())
    try:
        headers = {"Authorization": "Bearer " + key} if key else {}
        connection.request("GET", path, headers=headers)
        response = connection.getresponse()
        require(response.status == 200, "TLS domain HTTP check failed")
        return response.read()
    finally:
        connection.close()


def groups_from_account(account):
    values = []
    for name in ("credential_group", "credential-group", "credential_groups", "credential-groups"):
        value = account.get(name)
        if isinstance(value, str):
            values.extend(value.split(","))
        elif isinstance(value, list):
            for item in value:
                require(isinstance(item, str), "Malformed account pool membership")
                values.extend(item.split(","))
    return {value.strip() for value in values if value.strip()}


def check_sub2api_network(sub2api, cpa, keys):
    source = sub2api["NetworkSettings"]["Networks"]
    target = cpa["NetworkSettings"]["Networks"]
    shared = sorted(set(source) & set(target))
    require(shared, "CPA and Sub2API do not share a Docker network")
    address = target[shared[0]]["IPAddress"]
    require(address, "CPA has no address on the shared Docker network")
    # Enter only the network namespace, retaining host Python and filesystem.
    # Secrets enter over private stdin, never argv or a temporary file.
    child = """
import json, sys
from urllib.request import ProxyHandler, Request, build_opener
try:
    data = json.load(sys.stdin)
    opener = build_opener(ProxyHandler({}))
    for key in data['keys']:
        request = Request(data['url'], headers={'Authorization': 'Bearer ' + key})
        with opener.open(request, timeout=15) as response:
            if response.status != 200 or not isinstance(json.load(response).get('data'), list):
                raise ValueError()
except Exception:
    sys.exit(1)
"""
    result = subprocess.run(
        ["nsenter", "--target", str(sub2api["State"]["Pid"]), "--net", sys.executable, "-c", child],
        input=json.dumps({"url": "http://" + address + ":8317/v1/models", "keys": keys}),
        capture_output=True, text=True, timeout=15 * len(keys) + 10,
    )
    require(result.returncode == 0, "Sub2API network namespace authentication/reachability failed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("/opt/cli-proxy-api"))
    parser.add_argument("--domain", default="icode-xtu.cc.cd")
    args = parser.parse_args()
    try:
        import yaml
    except ImportError:
        raise CheckFailed("Python3 PyYAML is required on the production host") from None

    config = yaml.safe_load((args.root / "config.yaml").read_text(encoding="utf-8"))
    management_key = (args.root / "management.key").read_text(encoding="utf-8").strip()
    keys = config.get("api-keys", [])
    require(management_key and isinstance(keys, list) and keys, "CPA keys are missing")
    require(all(isinstance(key, str) and key.strip() for key in keys), "Malformed client key configuration")
    require(len(keys) == len(set(keys)), "Duplicate client keys in configuration")
    remote = config.get("remote-management", {})
    require(remote.get("disable-auto-update-panel") is True, "Custom panel auto-update must be disabled")
    require(not remote.get("disable-control-panel", False), "Management panel is disabled")
    bindings = config.get("api-key-credential-groups", {})
    require(isinstance(bindings, dict), "Invalid key pool bindings")

    def management(path):
        return json.loads(local_get("/v0/management" + path, management_key))

    _, live_headers = local_get("/v0/management/config", management_key, return_headers=True)
    access = management("/api-key-access")["items"]
    live_bindings = {entry["key"]: entry["groups"] for entry in access}
    require(set(live_bindings) == set(keys), "Running client keys differ from disk configuration")
    pools = {entry["name"] for entry in management("/credential-pools")["items"]}
    allowed = set()
    denied = 0
    for key in keys:
        groups = bindings.get(key)
        require(isinstance(groups, list), "Every key must be explicitly bound or denied with []")
        require(all(isinstance(group, str) and group.strip() for group in groups), "Malformed key pool binding")
        require(set(live_bindings[key]) == set(groups), "Running key permissions differ from disk")
        require(set(groups) <= pools, "Key references a missing pool")
        allowed.update(groups)
        denied += not groups

    accounts = management("/auth-files")["files"]
    active = [account for account in accounts if not account.get("disabled", False)]
    reachable = [account for account in active if groups_from_account(account) & allowed]
    require(reachable, "No enabled account belongs to an allowed pool")
    unpooled = sum(not groups_from_account(account) for account in accounts)
    plugin_data = management("/plugins")
    plugins = plugin_data.get("plugins", [])
    plugin_config = config.get("plugins", {})
    plugins_enabled = plugin_config.get("enabled", False)
    require(plugin_data.get("plugins_enabled") == plugins_enabled, "Running plugin setting differs from disk")
    expected_plugins = {
        name for name, item in plugin_config.get("configs", {}).items()
        if plugins_enabled and item.get("enabled", False)
    }
    registered = {plugin["id"]: plugin for plugin in plugins}
    require(expected_plugins <= set(registered), "A configured enabled plugin is missing")
    enabled = [registered[name] for name in expected_plugins]
    require(all(plugin.get("registered") and plugin.get("effective_enabled") for plugin in enabled),
            "A configured enabled plugin did not register")

    panel = local_get("/management.html")
    domain_panel = tls_get(args.domain, "/management.html")
    tls_get(args.domain, "/v0/management/config", management_key)
    require(panel == domain_panel, "TLS and internal management panels differ")
    marker = args.root / "DEPLOYED_RELEASE"
    manifest_check = "absent"
    if marker.exists():
        release = marker.read_text(encoding="utf-8").strip()
        require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", release), "Invalid release marker")
        manifest = json.loads((args.root / "releases" / release / "manifest.json").read_text(encoding="utf-8"))
        require(live_headers.get("x-cpa-commit") == manifest["backend_commit"],
                "Running backend commit differs from release manifest")
        expected = manifest["sha256"]["management.html"]
        require(isinstance(expected, str) and re.fullmatch(r"[a-fA-F0-9]{64}", expected), "Invalid panel manifest checksum")
        require(hashlib.sha256(panel).hexdigest() == expected.lower(), "Served panel differs from release manifest")
        manifest_check = "ok"

    check_sub2api_network(docker_inspect("sub2api"), docker_inspect("cli-proxy-api"), keys)
    print("management=ok tls=ok panel_manifest=" + manifest_check + " sub2api_network=ok")
    print("keys=%d bound=%d denied=%d accounts=%d reachable=%d unpooled=%d plugins=%d" % (
        len(keys), len(keys) - denied, denied, len(accounts), len(reachable), unpooled, len(enabled)))
    print("No generation performed; model listing does not prove upstream generation health.")


if __name__ == "__main__":
    try:
        main()
    except CheckFailed as error:
        print("CPA verification failed: " + str(error), file=sys.stderr)
        sys.exit(1)
    except Exception as error:
        # YAML errors, URLs and HTTP bodies may contain secrets. Print only type.
        print("CPA verification failed: " + type(error).__name__, file=sys.stderr)
        sys.exit(1)
