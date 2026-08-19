import json
import re
from pathlib import Path

from playwright.sync_api import sync_playwright


OUTPUT_DIR = Path(r"G:\154.9.26.202-枫迹云\sub2api\transit-hub\.artifacts\visual-priority-qa")
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)


def model_health(state, latency, weight=100):
    return [{
        "modelName": "gpt-5.6-sol",
        "providerFamily": "openai",
        "configured": True,
        "state": state,
        "currentWeight": weight,
        "consecutiveFailures": 0,
        "consecutiveSuccesses": 5,
        "lastProbeAt": "2026-08-06T10:20:00Z",
        "lastSuccessAt": "2026-08-06T10:20:00Z",
        "lastFailureAt": None,
        "lastLatencyMs": latency,
        "lastErrorKey": "",
        "lastErrorDetail": "",
        "lastRemoteAction": "",
        "updatedAt": "2026-08-06T10:20:00Z",
    }]


accounts = [
    {
        "id": "101", "name": "A - aicxxx.cn - 0.07x", "platform": "openai", "type": "oauth", "status": "active",
        "schedulable": True, "priority": 100, "concurrency": 20, "currentConcurrency": 4, "schedulerScore": 0.812,
        "usageP95FirstTokenMs": 920, "usageSampleCount": 20, "upstreamKeyGroupName": "ChatGPT-Plus",
        "upstreamKeyGroupMultiplier": 0.05, "upstreamKeyGroupMultiplierSource": "detected", "targetId": "sub2api:ws1:101",
        "probeAvailable": True, "modelHealth": model_health("healthy", 1100), "assignedPolicyIds": ["p1"],
        "assignedPolicies": [{"policyId": "p1", "policyName": "plus - 自动评分", "enabled": True, "priorityMode": "auto", "priorityStrategy": "balanced"}],
        "hasAssignedPolicy": True, "hasEnabledPolicy": True, "hasEnabledProbePolicy": True, "policyAssignmentSource": "group",
        "priorityManaged": True, "priorityConflict": False, "effectiveMultiplier": 0.05,
    },
    {
        "id": "102", "name": "A - api.icodexs.com - 0.06x", "platform": "openai", "type": "oauth", "status": "active",
        "schedulable": True, "priority": 200, "concurrency": 20, "currentConcurrency": 11, "schedulerScore": 0.664,
        "usageP95FirstTokenMs": 1380, "usageSampleCount": 17, "upstreamKeyGroupName": "plus-高并发",
        "upstreamKeyGroupMultiplier": 0.06, "upstreamKeyGroupMultiplierSource": "detected", "targetId": "sub2api:ws1:102",
        "probeAvailable": True, "modelHealth": model_health("degraded", 1600, 75), "assignedPolicyIds": ["p1"],
        "assignedPolicies": [{"policyId": "p1", "policyName": "plus - 自动评分", "enabled": True, "priorityMode": "auto", "priorityStrategy": "balanced"}],
        "hasAssignedPolicy": True, "hasEnabledPolicy": True, "hasEnabledProbePolicy": True, "policyAssignmentSource": "group",
        "priorityManaged": True, "priorityConflict": False, "effectiveMultiplier": 0.06,
    },
    {
        "id": "103", "name": "https://ai.youc.online-夜猫", "platform": "openai", "type": "oauth", "status": "active",
        "schedulable": True, "priority": 10000, "concurrency": 10, "currentConcurrency": 0, "schedulerScore": 0.921,
        "usageSampleCount": 2, "upstreamKeyGroupName": "夜间低价", "upstreamKeyGroupMultiplier": 0.04,
        "upstreamKeyGroupMultiplierSource": "detected", "targetId": "sub2api:ws1:103", "probeAvailable": True,
        "modelHealth": model_health("suspended", 400, 0), "assignedPolicyIds": ["p1"],
        "assignedPolicies": [{"policyId": "p1", "policyName": "plus - 自动评分", "enabled": True, "priorityMode": "auto", "priorityStrategy": "balanced"}],
        "hasAssignedPolicy": True, "hasEnabledPolicy": True, "hasEnabledProbePolicy": True, "policyAssignmentSource": "group",
        "priorityManaged": True, "priorityConflict": False, "effectiveMultiplier": 0.04,
    },
    {
        "id": "104", "name": "https://sub.hookai.shop", "platform": "openai", "type": "oauth", "status": "inactive",
        "schedulable": False, "priority": 10000, "concurrency": 8, "currentConcurrency": 0, "schedulerScore": 0.755,
        "usageP95FirstTokenMs": 700, "usageSampleCount": 20, "upstreamKeyGroupName": "专线", "upstreamKeyGroupMultiplier": 0.04,
        "upstreamKeyGroupMultiplierSource": "detected", "targetId": "sub2api:ws1:104", "probeAvailable": True,
        "modelHealth": model_health("healthy", 720), "assignedPolicyIds": ["p1"],
        "assignedPolicies": [{"policyId": "p1", "policyName": "plus - 自动评分", "enabled": True, "priorityMode": "auto", "priorityStrategy": "balanced"}],
        "hasAssignedPolicy": True, "hasEnabledPolicy": True, "hasEnabledProbePolicy": True, "policyAssignmentSource": "group",
        "priorityManaged": True, "priorityConflict": False, "effectiveMultiplier": 0.04,
    },
]

group = {
    "id": "42", "name": "plus", "platform": "openai", "status": "active", "statusMutable": True,
    "type": "public", "isExclusive": False, "subscriptionType": "standard", "multiplier": 0.075,
    "multiplierDisplay": "0.075x", "accountCount": 4, "monitoredAccountCount": 4, "excludedAccountCount": 0,
    "assignedPolicyIds": ["p1"], "assignedPolicies": [{"policyId": "p1", "policyName": "plus - 自动评分", "enabled": True, "priorityMode": "auto", "priorityStrategy": "balanced"}],
    "hasAssignedPolicy": True, "hasEnabledPolicy": True, "hasEnabledProbePolicy": True, "priorityMode": "auto",
    "priorityStrategy": "balanced", "priorityConflictCount": 0,
    "healthSummary": {"totalAccounts": 4, "probeableAccounts": 4, "unprobeableAccounts": 0, "healthyModels": 2,
                      "degradedModels": 1, "observingModels": 0, "recoveringModels": 0, "suspendedModels": 1,
                      "disabledModels": 0, "unconfiguredModels": 0, "lastProbeAt": "2026-08-06T10:20:00Z"},
    "accounts": accounts,
}

policy = {
    "id": "p1", "name": "plus - 自动评分", "enabled": True, "ownGroupId": "", "ownGroupName": "",
    "modelPattern": "", "probeMode": "chat", "probeIntervalSeconds": 60, "failureThreshold": 3,
    "successThreshold": 2, "cooldownSeconds": 300, "observationSeconds": 300, "recoveryStepPercent": 25,
    "autoDegradeEnabled": True, "autoRemoteActionEnabled": True, "priorityMode": "auto", "priorityStrategy": "balanced",
    "strategyMode": "health_probe", "dailyProbeBudget": 1000, "createdAt": "2026-08-06T00:00:00Z",
    "updatedAt": "2026-08-06T00:00:00Z", "modelTargets": [],
}


def response_for(path):
    if path == "/api/admin-accounts/current":
        return {"id": "ws1", "platform": "sub2api", "baseUrl": "https://example.test", "identity": "admin@example.test", "displayName": "演示工作区", "authMethod": "admin_key", "current": True, "lastUsedAt": None, "createdAt": "2026-08-01T00:00:00Z", "updatedAt": "2026-08-01T00:00:00Z"}
    if path == "/api/system/version":
        return {"version": "local-preview"}
    if path == "/api/connection-health/admin-groups":
        return [group]
    if path == "/api/connection-health/groups":
        return []
    if path.startswith("/api/connection-health/events"):
        return []
    if path == "/api/connection-health/policies":
        return [policy]
    if path == "/api/connection-health/admin-groups/42/policy-configuration":
        return {"adminGroupId": "42", "adminGroupName": "plus", "policyIds": [], "policies": [], "excludedTargetIds": []}
    if path == "/api/upstream-sites":
        return []
    return []


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    context = browser.new_context(viewport={"width": 1600, "height": 1000}, device_scale_factor=1)
    context.add_init_script("localStorage.setItem('transithub.auth.accessToken', 'visual-test-token')")
    page = context.new_page()
    console_errors = []
    page.on("console", lambda message: console_errors.append(message.text) if message.type == "error" else None)

    def intercept(route):
        payload = response_for(route.request.url.split("http://127.0.0.1:4173", 1)[-1])
        route.fulfill(status=200, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))

    page.route("**/api/**", intercept)
    page.goto("http://127.0.0.1:4173/admin/connection-health")
    page.wait_for_load_state("networkidle")
    page.screenshot(path=str(OUTPUT_DIR / "desktop.png"), full_page=True)

    page.get_by_role("button", name="管理分组策略").click()
    page.get_by_role("dialog", name="配置分组自动化").wait_for()
    page.get_by_role("button", name="下一步").click()
    page.get_by_role("button", name=re.compile(r"^自动评分")).click()
    page.screenshot(path=str(OUTPUT_DIR / "strategy-drawer.png"), full_page=True)

    page.goto("http://127.0.0.1:4173/admin/connection-health")
    page.wait_for_load_state("networkidle")
    page.set_viewport_size({"width": 390, "height": 844})
    page.wait_for_timeout(300)
    page.screenshot(path=str(OUTPUT_DIR / "mobile.png"), full_page=True)

    result = {
        "url": page.url,
        "desktop_size": page.locator("body").evaluate("el => ({width: el.scrollWidth, height: el.scrollHeight})"),
        "mobile_viewport": page.viewport_size,
        "console_errors": console_errors,
        "auto_score_badge": page.get_by_text("自动评分 · 兼容模式", exact=True).count(),
        "ttft_rows": page.get_by_text("TTFT P95", exact=False).count(),
    }
    (OUTPUT_DIR / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))
    browser.close()
