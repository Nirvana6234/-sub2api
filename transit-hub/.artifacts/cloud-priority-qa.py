import json
import os
import re
from pathlib import Path

from playwright.sync_api import sync_playwright


base_url = os.environ["QA_BASE_URL"].rstrip("/")
email = os.environ["QA_EMAIL"]
password = os.environ["QA_PASSWORD"]
output_dir = Path(".artifacts/visual-priority-qa")
output_dir.mkdir(parents=True, exist_ok=True)

console_errors = []
page_errors = []

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1600, "height": 1000})
    page.on("console", lambda message: console_errors.append(message.text) if message.type == "error" else None)
    page.on("pageerror", lambda error: page_errors.append(str(error)))

    page.goto(f"{base_url}/login", wait_until="networkidle", timeout=120_000)
    page.locator("#login-email").fill(email)
    page.locator("#login-password").fill(password)
    page.locator("form button[type=submit]").click()
    page.wait_for_url(re.compile(r"/admin(?:/)?$"), timeout=30_000)

    page.goto(f"{base_url}/admin/connection-health", wait_until="networkidle", timeout=120_000)
    page.wait_for_selector("main", timeout=30_000)
    auto_group_name = page.evaluate(
        """async () => {
          const token = localStorage.getItem('transithub.auth.accessToken');
          const response = await fetch('/api/connection-health/admin-groups', {
            headers: { Authorization: `Bearer ${token}` },
          });
          const groups = await response.json();
          const group = groups.find((item) => item.priorityMode === 'auto');
          return group ? group.name : '';
        }"""
    )
    if not auto_group_name:
        raise AssertionError("No cloud group is configured for auto priority")

    group_button = page.locator("nav button").filter(has_text=auto_group_name).first
    group_button.click()
    page.wait_for_timeout(500)
    body_text = page.locator("body").inner_text()
    if "\u81ea\u52a8\u8bc4\u5206" not in body_text:
        raise AssertionError("Auto priority badge is not visible")
    if "TTFT P95" not in body_text:
        raise AssertionError("TTFT P95 scoring signal is not visible")

    setup_button = page.get_by_role(
        "button",
        name=re.compile(
            "\u7ba1\u7406\u5206\u7ec4\u7b56\u7565|\u914d\u7f6e\u5206\u7ec4\u7b56\u7565"
        ),
    ).first
    setup_button.click()
    page.wait_for_timeout(500)
    page.get_by_role(
        "button",
        name=re.compile("\u4e0b\u4e00\u6b65|Next"),
    ).click()
    page.wait_for_timeout(500)
    page.get_by_role(
        "button",
        name=re.compile("\u81ea\u52a8\u8bc4\u5206|Auto Score"),
    ).first.click()
    page.wait_for_timeout(300)
    drawer_text = page.locator("body").inner_text()
    for label in ("\u4f4e\u4ef7\u4f18\u5148", "\u517c\u5bb9", "\u901f\u5ea6\u4f18\u5148"):
        if label not in drawer_text:
            raise AssertionError(f"Missing scoring mode label: {label}")

    screenshot = output_dir / "cloud-r3.png"
    page.screenshot(path=str(screenshot), full_page=True)
    result = {
        "autoGroup": auto_group_name,
        "autoBadge": True,
        "ttftP95": True,
        "strategyModes": 3,
        "consoleErrors": console_errors,
        "pageErrors": page_errors,
        "screenshot": str(screenshot),
    }
    (output_dir / "cloud-r3.json").write_text(json.dumps(result, ensure_ascii=True, indent=2), encoding="utf-8")
    print(json.dumps(result, ensure_ascii=True, sort_keys=True))
    browser.close()
