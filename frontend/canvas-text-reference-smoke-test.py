import os

from playwright.sync_api import sync_playwright


APP_URL = os.getenv("CANVAS_APP_URL", "http://127.0.0.1:5173/playground/canvas")
IMAGE_DATA_URL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACAAAAAYCAIAAAAUMWhjAAAANElEQVR4nGOU2NLDQEvARFPTGUYtIAKMxgFBMBpEBMFoEBEEo0FEEIwGEUEwGkQEAc2DCAAZNwGIgi1JqgAAAABJRU5ErkJggg=="


def main() -> None:
    chat_requests: list[dict] = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=True,
            executable_path=r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        )
        context = browser.new_context(viewport={"width": 1440, "height": 900})
        page = context.new_page()
        state_seed = """
            localStorage.setItem('auth_token', 'text-reference-smoke-token');
            localStorage.setItem('auth_user', JSON.stringify({
              id: 1, username: 'text-reference', email: 'text-reference@example.com', role: 'user',
              balance: 10, concurrency: 2, status: 'active', allowed_groups: null,
              balance_notify_enabled: false, balance_notify_threshold: null, balance_notify_extra_emails: []
            }));
            localStorage.setItem('sub2api.playground.canvas.v1.user.1', JSON.stringify({
              version: 1, activeProjectId: 'reference-project', projects: [{
                id: 'reference-project', title: 'Reference canvas',
                nodes: [
                  { id: 'reference-image', type: 'image', kind: 'result', x: 80, y: 80, width: 260, height: 320,
                    prompt: 'Reference image', model: 'gpt-image-1', imageUrl: 'DATA_URL', imageUrls: ['DATA_URL'],
                    status: 'success', createdAt: 1, updatedAt: 1 },
                  { id: 'target-text', type: 'text', kind: 'generator', x: 480, y: 80, width: 340, height: 300,
                    prompt: 'Describe the composition', textContent: 'Describe the composition', textFontSize: 16,
                    model: 'gpt-4.1', status: 'idle', createdAt: 1, updatedAt: 1 }
                ],
                connections: [{ id: 'reference-link', from: 'reference-image', to: 'target-text', kind: 'reference' }],
                viewport: { x: 0, y: 0, scale: 1 }, backgroundMode: 'dots', createdAt: 1, updatedAt: 1
              }]
            }).replaceAll('DATA_URL', 'IMAGE_DATA_URL'));
            """.replace("IMAGE_DATA_URL", IMAGE_DATA_URL)
        page.add_init_script("if (!localStorage.getItem('sub2api.playground.canvas.v1.user.1')) {" + state_seed + "}")

        def fulfill(route):
            url = route.request.url.split("?", 1)[0]
            if url.endswith("/api/v1/auth/me"):
                route.fulfill(status=200, content_type="application/json", body='{"id":1,"username":"text-reference","email":"text-reference@example.com","role":"user","balance":10,"concurrency":2,"status":"active","allowed_groups":null,"balance_notify_enabled":false,"balance_notify_threshold":null,"balance_notify_extra_emails":[],"run_mode":"production"}')
            elif url.endswith("/api/v1/settings/public"):
                route.fulfill(status=200, content_type="application/json", body='{"playground_enabled":true,"playground_default_image_model":"gpt-image-1"}')
            elif url.endswith("/api/v1/keys/playground/ensure"):
                route.fulfill(status=200, content_type="application/json", body="{}")
            elif "/api/v1/keys" in url:
                route.fulfill(status=200, content_type="application/json", body='{"items":[{"id":1,"user_id":1,"key":"sk-smoke","name":"Smoke key","group_id":1,"auto_group":false,"auto_group_strategy":"price","status":"active","group":{"id":1,"name":"Smoke OpenAI","platform":"openai","status":"active"}}],"total":1,"page":1,"page_size":100,"pages":1}')
            elif url.endswith("/api/v1/playground/models"):
                route.fulfill(status=200, content_type="application/json", body='{"data":[{"id":"gpt-image-1","owned_by":"openai"},{"id":"gpt-4.1","owned_by":"openai"}]}')
            elif url.endswith("/api/v1/playground/chat/completions"):
                chat_requests.append(route.request.post_data_json)
                route.fulfill(status=200, content_type="text/event-stream", body='data: {"choices":[{"delta":{"content":"Referenced image summary"}}]}\n\ndata: [DONE]\n\n')
            elif "/api/v1/" in url:
                route.fulfill(status=200, content_type="application/json", body="{}")
            else:
                route.continue_()

        page.route("**/*", fulfill)
        page.goto(APP_URL)
        page.wait_for_load_state("networkidle")
        target = page.locator('[data-node-id="target-text"]')
        target.click()
        page.locator('[data-canvas-text-reference="reference-image"]').click()
        assert "@[node:reference-image]" in target.locator("textarea").input_value()
        page.locator("#canvas-text-size").select_option("28")
        assert page.locator("#canvas-text-size").input_value() == "28"
        page.wait_for_timeout(250)
        page.get_by_title("Rewrite with AI").click() if page.get_by_title("Rewrite with AI").count() else page.get_by_title("AI 改写").click()
        page.wait_for_function("() => document.querySelectorAll('[data-node-id]').length === 3")
        assert chat_requests, "Text generation did not reach the chat endpoint"
        content = chat_requests[0]["messages"][1]["content"]
        assert isinstance(content, list), chat_requests[0]
        assert any(part.get("type") == "image_url" and part.get("image_url", {}).get("url", "").startswith("data:image/") for part in content), content
        page.reload()
        page.wait_for_load_state("networkidle")
        page.locator('[data-node-id="target-text"]').click()
        assert page.locator("[data-canvas-text-size]").input_value() == "28"
        print("canvas text reference smoke: PASS")
        browser.close()


if __name__ == "__main__":
    main()
