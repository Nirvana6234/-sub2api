from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from playwright.sync_api import Browser, BrowserContext, Page, sync_playwright


ROOT = Path(__file__).resolve().parents[2]
WORKSPACE_ROOT = ROOT.parent
ENV_PATH = ROOT / "deploy" / ".env"
PSQL = WORKSPACE_ROOT / ".local" / "pgsql" / "pgsql" / "bin" / "psql.exe"
FRONTEND_URL = "http://127.0.0.1:3000"
SCREENSHOT_DIR = ROOT / "deploy" / "tests" / "shared-audit"
EDGE = Path(r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe")
FORBIDDEN_RESPONSE_KEYS = {"credentials", "api_key", "access_token", "refresh_token", "id_token"}


@dataclass
class TestUser:
    user_id: int
    email: str
    username: str
    password_hash: str
    role: str
    status: str
    balance: float
    concurrency: int
    created_at: str
    updated_at: str


def read_env() -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in ENV_PATH.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def query_rows(sql: str, password: str) -> list[list[str]]:
    env = os.environ.copy()
    env["PGPASSWORD"] = password
    result = subprocess.run(
        [
            str(PSQL),
            "-h",
            "127.0.0.1",
            "-p",
            "5433",
            "-U",
            "postgres",
            "-d",
            "sub2api",
            "-A",
            "-t",
            "-F",
            "\t",
            "-v",
            "ON_ERROR_STOP=1",
            "-c",
            sql,
        ],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        env=env,
    )
    return [line.split("\t") for line in result.stdout.splitlines() if line.strip()]


def load_user(where_clause: str, password: str) -> TestUser:
    rows = query_rows(
        "SELECT id,email,COALESCE(username,''),password_hash,role,status,balance,"
        "concurrency,created_at,updated_at FROM users WHERE "
        + where_clause
        + " AND deleted_at IS NULL ORDER BY id LIMIT 1;",
        password,
    )
    if not rows:
        raise RuntimeError(f"No test user found for: {where_clause}")
    row = rows[0]
    return TestUser(
        user_id=int(row[0]),
        email=row[1],
        username=row[2],
        password_hash=row[3],
        role=row[4],
        status=row[5],
        balance=float(row[6]),
        concurrency=int(row[7]),
        created_at=row[8],
        updated_at=row[9],
    )


def base64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def token_version(user: TestUser) -> int:
    material = f"{user.email.strip().lower()}\n{user.password_hash}".encode("utf-8")
    fingerprint = int.from_bytes(hashlib.sha256(material).digest()[:8], "big")
    return fingerprint & 0x7FFFFFFFFFFFFFFF


def make_jwt(user: TestUser, secret: str) -> str:
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {
        "user_id": user.user_id,
        "email": user.email,
        "role": user.role,
        "token_version": token_version(user),
        "iat": now,
        "nbf": now,
        "exp": now + 3600,
    }
    signing_input = (
        base64url(json.dumps(header, separators=(",", ":")).encode("utf-8"))
        + "."
        + base64url(json.dumps(payload, separators=(",", ":")).encode("utf-8"))
    )
    signature = hmac.new(secret.encode("utf-8"), signing_input.encode("ascii"), hashlib.sha256).digest()
    return signing_input + "." + base64url(signature)


def frontend_user(user: TestUser) -> dict[str, Any]:
    return {
        "id": user.user_id,
        "username": user.username,
        "email": user.email,
        "role": user.role,
        "balance": user.balance,
        "concurrency": user.concurrency,
        "status": user.status,
        "allowed_groups": None,
        "balance_notify_enabled": False,
        "balance_notify_threshold": None,
        "balance_notify_extra_emails": [],
        "created_at": user.created_at,
        "updated_at": user.updated_at,
    }


def api_request(
    user: TestUser,
    token: str,
    path: str,
    method: str = "GET",
    body: dict[str, Any] | None = None,
) -> tuple[int, dict[str, Any]]:
    data = None if body is None else json.dumps(body).encode("utf-8")
    request = urllib.request.Request(
        "http://127.0.0.1:8080/api/v1" + path,
        data=data,
        method=method,
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
            "X-Shared-Audit-User": str(user.user_id),
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read())


def response_data(payload: dict[str, Any]) -> Any:
    return payload.get("data", payload)


def forbidden_key_paths(value: Any, prefix: str = "$") -> list[str]:
    found: list[str] = []
    if isinstance(value, dict):
        for key, nested in value.items():
            path = f"{prefix}.{key}"
            if key.lower() in FORBIDDEN_RESPONSE_KEYS:
                found.append(path)
            found.extend(forbidden_key_paths(nested, path))
    elif isinstance(value, list):
        for index, nested in enumerate(value):
            found.extend(forbidden_key_paths(nested, f"{prefix}[{index}]"))
    return found


def api_contract_audit(
    admin: TestUser,
    contributor: TestUser,
    consumer: TestUser,
    admin_token: str,
    contributor_token: str,
    consumer_token: str,
    known_secrets: list[str],
) -> dict[str, Any]:
    checks: list[dict[str, Any]] = []

    def record(
        name: str,
        status: int,
        payload: dict[str, Any],
        expected_status: int,
        assertion: bool,
    ) -> None:
        serialized = json.dumps(payload, ensure_ascii=False)
        checks.append(
            {
                "name": name,
                "status": status,
                "expected_status": expected_status,
                "assertion_passed": status == expected_status and assertion,
                "forbidden_key_paths": forbidden_key_paths(payload),
                "secret_leak_count": sum(1 for secret in known_secrets if secret and secret in serialized),
            }
        )

    status, payload = api_request(admin, admin_token, "/admin/contributions?page=1&page_size=100")
    data = response_data(payload)
    ids = {int(item["id"]) for item in data.get("items", [])} if isinstance(data, dict) else set()
    record("admin_can_list_contributions", status, payload, 200, {48, 55}.issubset(ids))

    status, payload = api_request(admin, admin_token, "/admin/contribution-rooms")
    data = response_data(payload)
    room_ids = {int(item["id"]) for item in data.get("items", [])} if isinstance(data, dict) else set()
    record("admin_can_list_rooms", status, payload, 200, 1 in room_ids)

    status, payload = api_request(consumer, consumer_token, "/contribution-rooms?page=1&limit=20")
    data = response_data(payload)
    preference = data.get("preference", {}) if isinstance(data, dict) else {}
    record(
        "consumer_catalog_and_preference",
        status,
        payload,
        200,
        1 in preference.get("room_ids", []) and preference.get("allow_pool_fallback") is True,
    )

    status, payload = api_request(
        contributor,
        contributor_token,
        "/contribution-rooms/preference",
        method="PUT",
        body={"room_ids": [1], "allow_pool_fallback": False},
    )
    record("contributor_cannot_join_own_room", status, payload, 400, True)

    status, payload = api_request(consumer, consumer_token, "/admin/contributions?page=1&page_size=20")
    record("consumer_cannot_use_admin_governance", status, payload, 403, True)

    status, payload = api_request(consumer, consumer_token, "/account-contributions/48/usage-summary")
    record("consumer_cannot_read_foreign_contribution", status, payload, 403, True)

    return {
        "checks": checks,
        "all_passed": all(
            item["assertion_passed"]
            and not item["forbidden_key_paths"]
            and item["secret_leak_count"] == 0
            for item in checks
        ),
    }


def authenticated_context(browser: Browser, user: TestUser, token: str) -> BrowserContext:
    context = browser.new_context(viewport={"width": 1440, "height": 1000})
    auth_user = json.dumps(frontend_user(user), separators=(",", ":"), ensure_ascii=False)
    auth = json.dumps({"token": token, "user": auth_user}, ensure_ascii=False)
    context.add_init_script(
        f"""
        (() => {{
          if (sessionStorage.getItem('shared_audit_auth_initialized') === '1') return;
          const auth = {auth};
          localStorage.setItem('auth_token', auth.token);
          localStorage.setItem('auth_user', auth.user);
          localStorage.removeItem('refresh_token');
          localStorage.removeItem('token_expires_at');
          sessionStorage.setItem('shared_audit_auth_initialized', '1');
        }})();
        """
    )
    return context


def audit_page(
    context: BrowserContext,
    path: str,
    screenshot_name: str,
    expected_text: str,
    known_secrets: list[str],
) -> dict[str, Any]:
    page: Page = context.new_page()
    console_errors: list[str] = []
    page_errors: list[str] = []
    failed_requests: list[str] = []
    bad_responses: list[str] = []
    leaked_response_secrets: set[int] = set()

    def record(target: list[str], value: str) -> None:
        if value not in target and len(target) < 20:
            target.append(value)

    page.on(
        "console",
        lambda message: record(console_errors, message.text) if message.type == "error" else None,
    )
    page.on("pageerror", lambda error: record(page_errors, str(error)))
    page.on("requestfailed", lambda request: record(failed_requests, f"{request.method} {request.url}"))

    def inspect_response(response: Any) -> None:
        if response.status >= 400:
            record(bad_responses, f"{response.status} {response.url}")
        if "/api/v1/" not in response.url:
            return
        content_type = (response.headers.get("content-type") or "").lower()
        if "json" not in content_type:
            return
        try:
            body = response.text()
        except Exception:
            return
        for index, secret in enumerate(known_secrets):
            if secret and secret in body:
                leaked_response_secrets.add(index)

    page.on("response", inspect_response)
    page.goto(FRONTEND_URL + path, wait_until="domcontentloaded", timeout=45_000)
    networkidle_reached = True
    try:
        page.wait_for_load_state("networkidle", timeout=15_000)
    except Exception:
        # Vite keeps a development connection open; the rendered DOM and API
        # responses below are the authoritative readiness checks in that case.
        networkidle_reached = False
    page.wait_for_timeout(1_500)
    body_text = page.locator("body").inner_text()
    body_html = page.locator("body").inner_html()
    SCREENSHOT_DIR.mkdir(parents=True, exist_ok=True)
    page.screenshot(path=str(SCREENSHOT_DIR / screenshot_name), full_page=True)

    leaked_dom_secrets = [index for index, secret in enumerate(known_secrets) if secret and secret in body_html]
    result = {
        "path": path,
        "final_url": page.url,
        "title": page.title(),
        "expected_text_found": expected_text in body_text,
        "redirected_to_login": "/login" in page.url,
        "body_length": len(body_text),
        "networkidle_reached": networkidle_reached,
        "console_errors": console_errors,
        "page_errors": page_errors,
        "failed_requests": failed_requests,
        "bad_responses": bad_responses,
        "dom_secret_leak_count": len(leaked_dom_secrets),
        "api_secret_leak_count": len(leaked_response_secrets),
    }
    page.close()
    return result


def main() -> None:
    env = read_env()
    db_password = env["POSTGRES_PASSWORD"]
    jwt_secret = query_rows(
        "SELECT value FROM security_secrets WHERE key='jwt_secret' LIMIT 1;",
        db_password,
    )[0][0]
    admin = load_user("role='admin'", db_password)
    contributor = load_user("id=2", db_password)
    consumer = load_user("id=3", db_password)

    secret_rows = query_rows(
        "SELECT COALESCE(credentials->>'api_key',''),"
        "COALESCE(credentials->>'access_token',''),"
        "COALESCE(credentials->>'refresh_token','') "
        "FROM accounts WHERE id IN (48,55);",
        db_password,
    )
    known_secrets = [value for row in secret_rows for value in row if len(value) >= 8]

    admin_token = make_jwt(admin, jwt_secret)
    contributor_token = make_jwt(contributor, jwt_secret)
    consumer_token = make_jwt(consumer, jwt_secret)
    api_checks = api_contract_audit(
        admin,
        contributor,
        consumer,
        admin_token,
        contributor_token,
        consumer_token,
        known_secrets,
    )

    results: list[dict[str, Any]] = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True, executable_path=str(EDGE))
        admin_context = authenticated_context(browser, admin, admin_token)
        contributor_context = authenticated_context(browser, contributor, contributor_token)
        consumer_context = authenticated_context(browser, consumer, consumer_token)
        try:
            results.append(
                audit_page(
                    admin_context,
                    "/admin/contributions",
                    "admin-contributions.png",
                    "822830019@qq.com",
                    known_secrets,
                )
            )
            results.append(
                audit_page(
                    admin_context,
                    "/admin/contribution-rooms",
                    "admin-contribution-rooms.png",
                    "nirvana_share",
                    known_secrets,
                )
            )
            results.append(
                audit_page(
                    contributor_context,
                    "/account-contributions",
                    "contributor-accounts.png",
                    "nirvana_share",
                    known_secrets,
                )
            )
            results.append(
                audit_page(
                    consumer_context,
                    "/shared-rooms",
                    "consumer-shared-rooms.png",
                    "nirvana_share",
                    known_secrets,
                )
            )
        finally:
            admin_context.close()
            contributor_context.close()
            consumer_context.close()
            browser.close()

    print(json.dumps({"api_checks": api_checks, "results": results}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
