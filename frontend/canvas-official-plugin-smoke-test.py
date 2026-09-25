import base64
from io import BytesIO

from PIL import Image
import os

from playwright.sync_api import sync_playwright


APP_URL = os.getenv("CANVAS_APP_URL", "http://127.0.0.1:5173/playground/canvas")


def panorama_png() -> bytes:
    image = Image.new("RGB", (1024, 512))
    pixels = image.load()
    for y in range(image.height):
        for x in range(image.width):
            pixels[x, y] = (
                int(30 + 190 * x / image.width),
                int(40 + 170 * y / image.height),
                int(190 - 120 * x / image.width),
            )
    output = BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


def main() -> None:
    console_errors: list[str] = []
    image_requests: list[dict] = []
    encoded_panorama = base64.b64encode(panorama_png()).decode("ascii")
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=True,
            executable_path=r"C:\Program Files\Google\Chrome\Application\chrome.exe",
            args=["--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
        )
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        page.on("console", lambda message: console_errors.append(message.text) if message.type == "error" else None)
        page.on("requestfailed", lambda request: console_errors.append(f"REQUEST FAILED {request.url}: {request.failure}"))
        page.add_init_script(
            """
            localStorage.setItem('auth_token', 'official-plugin-smoke-token');
            localStorage.setItem('auth_user', JSON.stringify({
              id: 1, username: 'plugin-smoke', email: 'plugin@example.com', role: 'user',
              balance: 10, concurrency: 2, status: 'active', allowed_groups: null,
              balance_notify_enabled: false, balance_notify_threshold: null,
              balance_notify_extra_emails: []
            }));
            localStorage.removeItem('sub2api.playground.canvas.plugins.v1');
            localStorage.removeItem('sub2api.playground.canvas.v1.user.1');
            """
        )

        def fulfill(route):
            url = route.request.url.split("?", 1)[0]
            if url.endswith("/api/v1/auth/me"):
                route.fulfill(status=200, content_type="application/json", body='{"id":1,"username":"official-plugin","email":"official-plugin@example.com","role":"user","balance":10,"concurrency":2,"status":"active","allowed_groups":null,"balance_notify_enabled":false,"balance_notify_threshold":null,"balance_notify_extra_emails":[],"run_mode":"production"}')
            elif url.endswith("/api/v1/settings/public"):
                route.fulfill(status=200, content_type="application/json", body='{"playground_enabled":true,"playground_default_image_model":"gpt-image-1"}')
            elif url.endswith("/api/v1/keys/playground/ensure"):
                route.fulfill(status=200, content_type="application/json", body="{}")
            elif "/api/v1/keys" in url:
                route.fulfill(status=200, content_type="application/json", body='{"items":[{"id":1,"user_id":1,"key":"sk-smoke","name":"Smoke key","group_id":1,"auto_group":false,"auto_group_strategy":"price","status":"active","group":{"id":1,"name":"Smoke OpenAI","platform":"openai","status":"active"}}],"total":1,"page":1,"page_size":100,"pages":1}')
            elif url.endswith("/api/v1/playground/models"):
                route.fulfill(status=200, content_type="application/json", body='{"data":[{"id":"gpt-image-1","owned_by":"openai"},{"id":"gpt-4.1","owned_by":"openai"}]}')
            elif "official-plugins.json" in url:
                route.fulfill(status=200, content_type="application/json", body='{"version":1,"plugins":[{"id":"panorama","name":"3D Panorama","version":"1.1.0","description":"Three.js panorama viewer","icon":"P","entry":"panorama.js"}]}')
            elif "plugins-dist/panorama.js" in url:
                route.fulfill(status=200, content_type="text/javascript", body='import { getReact } from "@infinite-canvas/plugin-sdk"; const React = getReact(); function Content() { return React.createElement("div", { id: "official-panorama-content", style: { padding: "16px", color: "rgb(10,20,30)" } }, "Official panorama"); } export default { id: "panorama", name: "3D Panorama", version: "1.1.0", nodes: [{ type: "panorama:viewer", title: "3D Panorama", defaultSize: { width: 480, height: 300 }, Content }] };')
            elif "/images/generations" in url:
                image_requests.append(route.request.post_data_json)
                route.fulfill(status=200, content_type="application/json", body='{"data":[{"b64_json":"' + encoded_panorama + '"}]}')
            elif "/api/v1/" in url:
                route.fulfill(status=200, content_type="application/json", body="{}")
            else:
                route.continue_()

        page.route("**/*", fulfill)
        page.goto(APP_URL)
        page.wait_for_load_state("networkidle")

        plugin_button = page.get_by_title("Canvas plugins") if page.get_by_title("Canvas plugins").count() else page.get_by_title("画布插件")
        plugin_button.click()
        dialog = page.locator("[data-canvas-plugin-dialog]")
        dialog.wait_for(state="visible")
        official_card = dialog.locator('[data-plugin-catalog-id="panorama"]')
        official_card.wait_for(state="visible", timeout=30000)
        official_card.locator("button").click()
        page.wait_for_function(
            "() => localStorage.getItem('sub2api.playground.canvas.plugins.v1')?.includes('Official panorama')",
            timeout=60000,
        )
        installed_card = dialog.locator('[data-plugin-installed-id="panorama"]')
        installed_card.wait_for(state="visible", timeout=60000)
        assert "v1.1.0" in installed_card.inner_text()
        close_button = dialog.get_by_title("Close") if dialog.get_by_title("Close").count() else dialog.get_by_title("关闭")
        close_button.click()
        dialog.wait_for(state="detached")

        surface = page.locator("[data-canvas-surface]")
        surface.dispatch_event("dblclick", {"clientX": 480, "clientY": 260})
        page.locator('[data-node-create-type="image"]').click()
        source_node = page.locator('[data-node-id^="image-"]').last
        source_node.wait_for(state="visible")
        surface.dispatch_event("dblclick", {"clientX": 620, "clientY": 560})
        page.locator('[data-node-create-type="text"]').click()
        text_node = page.locator('[data-node-id^="text-"]').last
        text_node.wait_for(state="visible")
        text_node.locator("textarea").first.fill("Use a saturated ultramarine color treatment")
        surface.dispatch_event("dblclick", {"clientX": 880, "clientY": 390})
        page.locator('[data-node-create-type="panorama:viewer"]').click()
        node = page.locator('[data-node-id^="panorama:viewer-"]').last
        node.wait_for(state="visible")

        official_content = node.get_by_text("Official panorama")
        official_content.wait_for(state="visible", timeout=60000)
        rendered = Image.open(BytesIO(node.screenshot())).convert("RGB")
        extrema = rendered.getextrema()
        colors = rendered.resize((64, 32)).getcolors(maxcolors=64 * 32) or []
        assert "Official panorama" in node.inner_text()
        assert rendered.width > 100 and rendered.height > 100, f"Unexpected plugin screenshot size: {rendered.size}"
        assert any(high - low > 20 for low, high in extrema), f"Plugin screenshot is visually blank: {extrema}"
        assert len(colors) > 3, f"Plugin screenshot has too little visual variation: {len(colors)} colors"
        assert not image_requests, "The mocked official ESM plugin should render without invoking image generation"
        page.screenshot(path="canvas-official-panorama-smoke.png", full_page=True)
        relevant_errors = [message for message in console_errors if message.startswith("REQUEST FAILED") or any(token in message.lower() for token in ("canvas-plugin", "three.js", "webgl"))]
        assert not relevant_errors, "Browser console errors: " + " | ".join(relevant_errors)
        print(f"official panorama smoke: PASS size={rendered.size} colors={len(colors)} extrema={extrema}")
        browser.close()


if __name__ == "__main__":
    main()
