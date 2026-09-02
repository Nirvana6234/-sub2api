import os
import time

from playwright.sync_api import sync_playwright


APP_URL = os.getenv("CANVAS_APP_URL", "http://127.0.0.1:5173/playground/canvas")


def wait_for_node_count(page, count, timeout=30000):
    deadline = time.monotonic() + timeout / 1000
    nodes = page.locator("[data-node-id]")
    while time.monotonic() < deadline:
        if nodes.count() == count:
            return
        page.wait_for_timeout(100)
    raise AssertionError(f"Expected {count} canvas nodes, found {nodes.count()}")


def main():
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=True,
            executable_path=r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        )
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        audio_requests = []
        page.on("console", lambda message: print(f"BROWSER_CONSOLE[{message.type}]: {message.text}"))
        page.on("pageerror", lambda error: print(f"BROWSER_PAGEERROR: {error}"))

        page.add_init_script(
            """
            localStorage.setItem('auth_token', 'smoke-token');
            localStorage.setItem('auth_user', JSON.stringify({
              id: 1, username: 'smoke', email: 'smoke@example.com', role: 'user',
              balance: 10, concurrency: 2, status: 'active', allowed_groups: null,
              balance_notify_enabled: false, balance_notify_threshold: null,
              balance_notify_extra_emails: []
            }));
            localStorage.setItem('sub2api.playground.canvas.v1.user.1', JSON.stringify({
              version: 1,
              activeProjectId: 'project-smoke',
              projects: [{
                id: 'project-smoke',
                title: 'Smoke canvas',
                nodes: [{
                  id: 'image-smoke', type: 'image', kind: 'result', x: 80, y: 80,
                  width: 320, height: 320, prompt: 'Smoke image', model: 'gpt-image-1',
                  imageUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAYCAIAAAAUMWhjAAAANElEQVR4nGOU2NLDQEvARFPTGUYtIAKMxgFBMBpEBMFoEBEEo0FEEIwGEUEwGkQEAc2DCAAZNwGIgi1JqgAAAABJRU5ErkJggg==',
                  imageUrls: ['data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAYCAIAAAAUMWhjAAAANElEQVR4nGOU2NLDQEvARFPTGUYtIAKMxgFBMBpEBMFoEBEEo0FEEIwGEUEwGkQEAc2DCAAZNwGIgi1JqgAAAABJRU5ErkJggg=='],
                  status: 'success', createdAt: Date.now(), updatedAt: Date.now()
                }],
                connections: [],
                viewport: {x: 0, y: 0, scale: 1},
                backgroundMode: 'dots',
                createdAt: Date.now(),
                updatedAt: Date.now()
              }]
            }));
            localStorage.removeItem('sub2api.playground.canvas.plugins.v1');
            """
        )

        def fulfill(route):
            url = route.request.url.split("?", 1)[0]
            if "/audio/" in url:
                audio_requests.append(url)
            if url.endswith("/api/v1/auth/me"):
                route.fulfill(status=200, content_type="application/json", body='{"id":1,"username":"smoke","email":"smoke@example.com","role":"user","balance":10,"concurrency":2,"status":"active","allowed_groups":null,"balance_notify_enabled":false,"balance_notify_threshold":null,"balance_notify_extra_emails":[],"run_mode":"production"}')
            elif url == "https://example.com/remote-plugin.js":
                route.fulfill(
                    status=200,
                    content_type="text/javascript",
                    body='import { getReact, useState } from "@infinite-canvas/plugin-sdk"; const React = getReact(); function Content({ ctx }) { const [n, setN] = useState(0); const [text, setText] = useState(""); const ask = () => ctx.ai.generateText("Smoke plugin prompt").then((result) => setText(result.text)); return React.createElement("div", null, React.createElement("button", { type: "button", onClick: () => setN((value) => value + 1), style: { padding: "12px", color: ctx.theme.node.text } }, `Remote click ${n}`), React.createElement("button", { type: "button", onClick: ask, title: "Run remote AI" }, "Run AI"), text ? React.createElement("span", null, text) : null); } function Panel({ onClose }) { return React.createElement("div", null, React.createElement("strong", null, "Remote panel"), React.createElement("button", { type: "button", onClick: onClose }, "Close panel")); } export default { id: "remote-smoke", name: "Remote smoke", version: "1.0.0", nodes: [{ type: "remote-smoke:counter", title: "Remote counter", defaultSize: { width: 280, height: 180 }, Content, Panel, toolbar: (ctx) => [{ id: "open", title: "Open remote panel", label: "Panel", icon: "P", onClick: () => ctx.openPanel() }] }] };',
                )
            elif "official-plugins.json" in url:
                route.fulfill(status=200, content_type="application/json", body='{"version":1,"plugins":[{"id":"panorama","name":"3D Panorama","version":"1.1.0","description":"Three.js panorama viewer","icon":"P","entry":"panorama.js"}]}')
            elif "plugins-dist/panorama.js" in url:
                route.fulfill(status=200, content_type="text/javascript", body='import { getReact } from "@infinite-canvas/plugin-sdk"; const React = getReact(); function Content() { return React.createElement("div", { style: { padding: "16px" } }, "Official panorama"); } export default { id: "panorama", name: "3D Panorama", version: "1.1.0", nodes: [{ type: "panorama:viewer", title: "3D Panorama", defaultSize: { width: 480, height: 300 }, Content }] };')
            elif url.endswith("/api/v1/settings/public"):
                route.fulfill(
                    status=200,
                    content_type="application/json",
                    body='{"playground_enabled":true,"playground_default_image_model":"gpt-image-1"}',
                )
            elif url.endswith("/api/v1/keys/playground/ensure"):
                route.fulfill(status=200, content_type="application/json", body="{}")
            elif "/api/v1/keys" in url:
                route.fulfill(
                    status=200,
                    content_type="application/json",
                    body='{"items":[{"id":1,"user_id":1,"key":"sk-smoke","name":"Smoke key","group_id":1,"auto_group":false,"auto_group_strategy":"price","auto_group_ids":[],"status":"active","ip_whitelist":[],"ip_blacklist":[],"last_used_at":null,"last_used_ip":null,"quota":0,"quota_used":0,"expires_at":null,"created_at":"","updated_at":"","current_concurrency":0,"group":{"id":1,"name":"Smoke OpenAI","platform":"openai","status":"active"},"rate_limit_5h":0,"rate_limit_1d":0,"rate_limit_7d":0,"usage_5h":0,"usage_1d":0,"usage_7d":0,"window_5h_start":null,"window_1d_start":null,"window_7d_start":null,"reset_5h_at":null,"reset_1d_at":null,"reset_7d_at":null}],"total":1,"page":1,"page_size":100,"pages":1}',
                )
            elif url.endswith("/api/v1/playground/models"):
                route.fulfill(
                    status=200,
                    content_type="application/json",
                    body='{"data":[{"id":"gpt-image-1","owned_by":"openai"},{"id":"gpt-4.1","owned_by":"openai"}]}',
                )
            elif url.endswith("/api/v1/playground/chat/completions"):
                route.fulfill(
                    status=200,
                    content_type="text/event-stream",
                    body='data: {"choices":[{"delta":{"content":"A refined lighthouse concept"}}]}\n\ndata: [DONE]\n\n',
                )
            elif url.endswith("/api/v1/playground/audio/speech"):
                route.fulfill(status=200, content_type="audio/mpeg", body=b"ID3-smoke-audio")
            elif url.endswith("/api/v1/playground/audio/transcriptions"):
                route.fulfill(status=200, content_type="application/json", body='{"text":"A calm ocean ambience, transcribed"}')
            elif "/api/v1/" in url:
                route.fulfill(status=200, content_type="application/json", body="{}")
            else:
                route.continue_()

        page.route("**/*", fulfill)
        page.goto(APP_URL)
        page.wait_for_load_state("networkidle")
        page.wait_for_timeout(1000)
        page.screenshot(path="canvas-smoke.png", full_page=True)

        assert page.get_by_text("Infinite canvas").count() + page.get_by_text("无限画布").count() > 0
        canvas = page.locator("[data-canvas-no-zoom]").first
        assert canvas.count() > 0
        page.locator('[data-node-id="image-smoke"]').click()
        crop_button = page.get_by_title("Crop image") if page.get_by_title("Crop image").count() else page.get_by_title("裁剪图片")
        crop_button.click()
        page.locator("[data-image-editor]").wait_for(state="visible")
        page.screenshot(path="canvas-image-editor-smoke.png", full_page=True)
        page.locator("[data-image-editor-apply]").click()
        page.locator("[data-image-editor]").wait_for(state="detached")
        view_button = page.get_by_title("View image") if page.get_by_title("View image").count() else page.get_by_title("查看原图")
        view_button.click()
        page.locator("[data-image-viewer]").wait_for(state="visible")
        page.locator("[data-image-viewer]").get_by_title("Close").click() if page.locator("[data-image-viewer]").get_by_title("Close").count() else page.locator("[data-image-viewer]").get_by_title("关闭").click()
        page.locator("[data-image-viewer]").wait_for(state="detached")
        mask_button = page.get_by_title("Mask edit") if page.get_by_title("Mask edit").count() else page.get_by_title("蒙版编辑")
        mask_button.click()
        page.locator("[data-image-mask-dialog]").wait_for(state="visible")
        page.locator("[data-image-mask-dialog]").get_by_title("Close").click() if page.locator("[data-image-mask-dialog]").get_by_title("Close").count() else page.locator("[data-image-mask-dialog]").get_by_title("关闭").click()
        page.locator("[data-image-mask-dialog]").wait_for(state="detached")
        angle_button = page.get_by_title("Angle transform") if page.get_by_title("Angle transform").count() else page.get_by_title("角度变换")
        angle_button.click()
        page.locator("[data-image-angle-dialog]").wait_for(state="visible")
        page.locator("[data-image-angle-apply]").click()
        page.locator("[data-image-angle-dialog]").wait_for(state="detached")
        wait_for_node_count(page, 2)
        page.keyboard.press("Control+Z")
        assert page.locator("[data-node-id]").count() == 1
        page.locator('[data-node-id="image-smoke"]').click()
        split_button = page.get_by_title("Split image") if page.get_by_title("Split image").count() else page.get_by_title("切分图片")
        split_button.click()
        page.locator("[data-image-split-dialog]").wait_for(state="visible")
        page.locator("[data-image-split-apply]").click()
        page.locator("[data-image-split-dialog]").wait_for(state="detached")
        wait_for_node_count(page, 5)
        page.keyboard.press("Control+Z")
        assert page.locator("[data-node-id]").count() == 1
        page.locator('[data-node-id="image-smoke"]').click()
        upscale_button = page.get_by_title("Upscale image") if page.get_by_title("Upscale image").count() else page.get_by_title("放大图片")
        upscale_button.click()
        page.locator("[data-image-upscale-dialog]").wait_for(state="visible")
        page.locator("[data-image-upscale-apply]").click()
        page.locator("[data-image-upscale-dialog]").wait_for(state="detached")
        wait_for_node_count(page, 2)
        page.keyboard.press("Control+Z")
        assert page.locator("[data-node-id]").count() == 1
        page.locator('[data-node-id="image-smoke"]').click()
        reverse_button = page.get_by_title("Reverse prompt") if page.get_by_title("Reverse prompt").count() else page.get_by_title("反推提示词")
        reverse_button.click()
        page.wait_for_timeout(300)
        assert page.locator("[data-node-id]").count() == 2
        page.keyboard.press("Delete")
        assert page.locator("[data-node-id]").count() == 1
        page.locator("[data-canvas-surface]").dblclick(position={"x": 600, "y": 300})
        assert page.get_by_text("Choose node type").count() + page.get_by_text("选择节点类型").count() == 1
        page.locator('[data-node-create-type="text"]').click()
        assert page.locator("[data-node-id]").count() == 2
        page.locator("[data-node-id] textarea").fill("A lighthouse on a quiet coast")
        rewrite_button = page.get_by_title("Rewrite with AI") if page.get_by_title("Rewrite with AI").count() else page.get_by_title("AI 改写")
        rewrite_button.click()
        page.wait_for_timeout(300)
        assert page.locator("[data-node-id]").count() == 3
        page.get_by_title("Canvas assistant").click() if page.get_by_title("Canvas assistant").count() else page.get_by_title("画布助手").click()
        assistant_prompt = page.get_by_placeholder("Ask about the selected canvas context…") if page.get_by_placeholder("Ask about the selected canvas context…").count() else page.get_by_placeholder("围绕选中的画布内容提问…")
        assistant_prompt.fill("Suggest a stronger visual direction")
        send_button = page.get_by_title("Send") if page.get_by_title("Send").count() else page.get_by_title("发送")
        send_button.click()
        page.wait_for_timeout(300)
        insert_button = page.get_by_text("Insert into canvas") if page.get_by_text("Insert into canvas").count() else page.get_by_text("插入画布")
        insert_button.click()
        assert page.locator("[data-node-id]").count() == 4
        page.get_by_title("Prompt library").click() if page.get_by_title("Prompt library").count() else page.get_by_title("提示词库").click()
        prompt_insert = page.get_by_text("Insert", exact=True) if page.get_by_text("Insert", exact=True).count() else page.get_by_text("插入", exact=True)
        prompt_insert.first.click()
        assert page.locator("[data-node-id]").count() == 5
        config_button = page.get_by_title("Add config node") if page.get_by_title("Add config node").count() else page.get_by_title("添加配置节点")
        config_button.click()
        assert page.locator("[data-node-id]").count() == 6
        page.keyboard.press("Control+A")
        page.keyboard.press("Control+C")
        page.keyboard.press("Control+V")
        assert page.locator("[data-node-id]").count() == 12
        page.keyboard.press("Escape")
        page.locator("[data-canvas-surface]").dispatch_event(
            "dblclick",
            {"clientX": 1020, "clientY": 680, "bubbles": True},
        )
        audio_button = page.locator('[data-node-create-type="audio"]')
        audio_button.click()
        assert page.locator("[data-node-id]").count() == 13
        audio_node = page.locator("[data-node-id]").last
        audio_node.locator("input").fill("A calm ocean ambience")
        audio_node.locator("input").press("Tab")
        generate_audio = page.get_by_title("Generate speech") if page.get_by_title("Generate speech").count() else page.get_by_title("生成语音")
        generate_audio.click()
        page.wait_for_timeout(300)
        assert audio_node.locator("audio").count() == 1
        transcribe_button = audio_node.get_by_title("Transcribe audio") if audio_node.get_by_title("Transcribe audio").count() else audio_node.get_by_title("转写音频")
        transcribe_button.click()
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            values = [page.locator("textarea").nth(index).input_value() for index in range(page.locator("textarea").count())]
            if "A calm ocean ambience, transcribed" in values:
                break
            page.wait_for_timeout(100)
        values = [page.locator("textarea").nth(index).input_value() for index in range(page.locator("textarea").count())]
        assert "A calm ocean ambience, transcribed" in values, {"values": values, "audio_requests": audio_requests}
        agent_button = page.get_by_title("Canvas Agent") if page.get_by_title("Canvas Agent").count() else page.get_by_title("画布 Agent")
        agent_button.click()
        page.locator("[data-canvas-agent-dialog]").wait_for(state="visible")
        assert page.locator("[data-canvas-agent-dialog] input").count() == 2
        page.locator("[data-canvas-agent-dialog]").get_by_title("Close").click() if page.locator("[data-canvas-agent-dialog]").get_by_title("Close").count() else page.locator("[data-canvas-agent-dialog]").get_by_title("关闭").click()
        page.locator("[data-canvas-agent-dialog]").wait_for(state="detached")

        plugin_button = page.get_by_title("Canvas plugins") if page.get_by_title("Canvas plugins").count() else page.get_by_title("画布插件")
        plugin_button.click()
        page.locator("[data-canvas-plugin-dialog]").wait_for(state="visible")
        page.locator('[data-plugin-installed-id="panorama"]').wait_for(state="visible")
        official_card = page.locator('[data-plugin-catalog-id="panorama"]')
        official_card.locator("button").click()
        page.locator('[data-plugin-installed-id="panorama"]').wait_for(state="visible")
        assert page.locator("[data-canvas-plugin-dialog] input:not([type='checkbox'])").count() == 1
        plugin_url = page.locator("[data-canvas-plugin-dialog] input:not([type='checkbox'])")
        plugin_url.fill("https://example.com/remote-plugin.js")
        install_button = page.get_by_text("Install manifest") if page.get_by_text("Install manifest").count() else page.get_by_text("安装清单")
        install_button.click()
        dialog = page.locator("[data-canvas-plugin-dialog]")
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if dialog.get_by_text("Remote smoke").count() and dialog.get_by_text("Remote smoke").first.is_visible():
                break
            if dialog.locator("p.text-red-700, p.text-red-300").count():
                break
            page.wait_for_timeout(100)
        if not (dialog.get_by_text("Remote smoke").count() and dialog.get_by_text("Remote smoke").first.is_visible()):
            raise AssertionError({"plugin_dialog": dialog.inner_text()})
        page.locator("[data-canvas-plugin-dialog]").get_by_title("Close").click() if page.locator("[data-canvas-plugin-dialog]").get_by_title("Close").count() else page.locator("[data-canvas-plugin-dialog]").get_by_title("关闭").click()
        page.locator("[data-canvas-plugin-dialog]").wait_for(state="detached")

        page.keyboard.press("Escape")
        page.locator("[data-canvas-surface]").dispatch_event("dblclick", {"clientX": 700, "clientY": 360})
        markdown_option = page.locator('[data-node-create-type="markdown:doc"]')
        markdown_option.wait_for(state="visible")
        markdown_option.click()
        markdown_node = page.locator('[data-node-id^="markdown:doc-"]').last
        markdown_node.wait_for(state="visible")
        edit_plugin = markdown_node.get_by_title("Edit plugin content") if markdown_node.get_by_title("Edit plugin content").count() else markdown_node.get_by_title("编辑插件内容")
        edit_plugin.click()
        markdown_node.locator("textarea").fill("# Smoke plugin")
        assert markdown_node.locator("textarea").input_value() == "# Smoke plugin"
        page.keyboard.press("Escape")
        page.locator("[data-canvas-surface]").dispatch_event("dblclick", {"clientX": 760, "clientY": 400})
        panorama_option = page.locator('[data-node-create-type="panorama:viewer"]')
        panorama_option.wait_for(state="visible")
        panorama_option.click()
        panorama_node = page.locator('[data-node-id^="panorama:viewer-"]').last
        panorama_node.wait_for(state="visible")
        panorama_node.get_by_text("Official panorama").wait_for(state="visible")
        page.keyboard.press("Escape")
        page.locator("[data-canvas-surface]").dispatch_event("dblclick", {"clientX": 860, "clientY": 430})
        remote_option = page.locator('[data-node-create-type="remote-smoke:counter"]')
        remote_option.wait_for(state="visible")
        remote_option.click()
        remote_node = page.locator('[data-node-id^="remote-smoke:counter-"]').last
        remote_node.get_by_text("Remote click 0").wait_for(state="visible")
        remote_node.get_by_text("Remote click 0").click()
        remote_node.get_by_text("Remote click 1").wait_for(state="visible")
        remote_node.get_by_title("Run remote AI").click()
        remote_node.get_by_text("A refined lighthouse concept").wait_for(state="visible")
        remote_node.get_by_title("Open remote panel").click()
        remote_node.get_by_text("Remote panel").wait_for(state="visible")
        remote_node.get_by_text("Close panel").click(force=True)
        remote_node.get_by_text("Remote panel").wait_for(state="detached")
        print("canvas smoke: PASS")
        browser.close()


if __name__ == "__main__":
    main()
