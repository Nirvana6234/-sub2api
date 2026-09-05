from pathlib import Path
from playwright.sync_api import sync_playwright


BASE_URL = "http://127.0.0.1:5174"
USER_ID = 101
AUTH_INIT_SCRIPT = """
() => {
  const user = { id: 101, email: 'canvas-smoke@example.com', username: 'Canvas Smoke', role: 'user' };
  localStorage.setItem('auth_token', 'local-smoke-token');
  localStorage.setItem('refresh_token', '');
  localStorage.setItem('token_expires_at', String(Date.now() + 3600000));
  localStorage.setItem('auth_user', JSON.stringify(user));
}
"""


def seed_browser(page):
    page.goto(f"{BASE_URL}/login", wait_until="domcontentloaded")
    page.evaluate(
        """
        ({ userId }) => {
          const user = { id: userId, email: 'canvas-smoke@example.com', username: 'Canvas Smoke', role: 'user' };
          localStorage.setItem('auth_token', 'local-smoke-token');
          localStorage.setItem('refresh_token', 'local-smoke-refresh');
          localStorage.setItem('token_expires_at', String(Date.now() + 3600000));
          localStorage.setItem('auth_user', JSON.stringify(user));
          const now = Date.now();
          const project = {
            id: 'smoke-project',
            title: '本地验证样例',
            viewport: { x: 0, y: 0, scale: 1 },
            backgroundMode: 'dots',
            createdAt: now,
            updatedAt: now,
            connections: [{ id: 'smoke-connection', from: 'smoke-text', to: 'smoke-image' }],
            nodes: [
              {
                id: 'smoke-text', type: 'text', kind: 'generator', title: '文本提示词',
                x: 80, y: 80, width: 360, height: 240,
                prompt: '请写一个适合儿童绘本的太空探险场景。',
                textContent: '请写一个适合儿童绘本的太空探险场景。',
                model: 'gpt-5.6-terra', status: 'idle', textFontSize: 16, textCount: 1,
                createdAt: now, updatedAt: now
              },
              {
                id: 'smoke-image', type: 'image', kind: 'generator', title: '图片生成',
                x: 520, y: 80, width: 360, height: 420,
                prompt: '彩色儿童绘本风格的太空探险，柔和灯光。',
                model: 'gpt-image-2', status: 'success', imageCount: 1,
                imageUrl: 'data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 width=%22640%22 height=%22400%22%3E%3Crect width=%22640%22 height=%22400%22 fill=%22%23172f4d%22/%3E%3Ccircle cx=%22320%22 cy=%22200%22 r=%22120%22 fill=%22%23f5c04a%22/%3E%3C/svg%3E',
                imageUrls: ['data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 width=%22640%22 height=%22400%22%3E%3Crect width=%22640%22 height=%22400%22 fill=%22%23172f4d%22/%3E%3Ccircle cx=%22320%22 cy=%22200%22 r=%22120%22 fill=%22%23f5c04a%22/%3E%3C/svg%3E'],
                createdAt: now, updatedAt: now
              }
            ]
          };
          localStorage.setItem(`sub2api.playground.canvas.v1.user.${userId}`, JSON.stringify({ version: 1, activeProjectId: project.id, projects: [project] }));
        }
        """,
        {"userId": USER_ID},
    )
    page.evaluate(
        """
        () => new Promise((resolve, reject) => {
          const request = indexedDB.open('sub2api-playground-images', 2);
          request.onupgradeneeded = () => {
            const db = request.result;
            if (!db.objectStoreNames.contains('images')) {
              const store = db.createObjectStore('images', { keyPath: 'key' });
              store.createIndex('scope', 'scope', { unique: false });
              store.createIndex('createdAt', 'createdAt', { unique: false });
            }
          };
          request.onerror = () => reject(request.error);
          request.onsuccess = () => {
            const db = request.result;
            const tx = db.transaction('images', 'readwrite');
            tx.objectStore('images').put({
              key: '101:42:smoke-gallery:0', scope: '101:42', createdAt: Date.now(),
              blob: new Blob([`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400"><rect width="640" height="400" fill="#d9f4ef"/><circle cx="320" cy="200" r="110" fill="#0f9f91"/><text x="320" y="215" text-anchor="middle" font-size="32" fill="white">Canvas smoke</text></svg>`], { type: 'image/svg+xml' }),
              prompt: '本地图库验证：儿童太空探险', model: 'gpt-image-2', size: '1024x1024', quality: 'standard', outputFormat: 'png', sourceImageCount: 0
            });
            tx.oncomplete = () => { db.close(); resolve(true); };
            tx.onerror = () => reject(tx.error);
          };
        })
        """
    )


def mock_api(page):
    image_result = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='

    def handle(route):
        url = route.request.url
        if url.endswith('/auth/me'):
            route.fulfill(status=200, content_type='application/json', body='{"id":101,"email":"canvas-smoke@example.com","username":"Canvas Smoke","role":"user"}')
            return
        if '/keys/playground/ensure' in url:
            route.fulfill(status=200, content_type='application/json', body='{}')
            return
        if '/keys?' in url:
            route.fulfill(status=200, content_type='application/json', body='{"items":[{"id":42,"user_id":101,"key":"sk-smoke-key","name":"Playground Images","group_id":7,"auto_group":false,"auto_group_strategy":"price","auto_group_ids":[],"status":"active","group":{"id":7,"name":"Images","platform":"openai","status":"active"}}],"page":1,"pages":1,"page_size":100,"total":1}')
            return
        if '/playground/models' in url:
            route.fulfill(status=200, content_type='application/json', body='{"data":[{"id":"gpt-image-2"},{"id":"gpt-5.6-terra"}]}')
            return
        if '/playground/images/generations' in url or '/playground/images/edits' in url:
            route.fulfill(status=200, content_type='application/json', body=f'{{"data":[{{"b64_json":"{image_result}"}}]}}')
            return
        route.abort()

    page.route('**/api/v1/**', handle)


def main():
    screenshot_dir = Path('test-results')
    screenshot_dir.mkdir(exist_ok=True)
    edit_requests = []
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(
            headless=True,
            executable_path=r'C:\Program Files\Google\Chrome\Application\chrome.exe',
        )
        context = browser.new_context(viewport={"width": 1440, "height": 900}, device_scale_factor=1)
        context.add_init_script(AUTH_INIT_SCRIPT)
        page = context.new_page()
        page.on('console', lambda message: print('Console:', message.type, message.text))
        page.on('pageerror', lambda error: print('Page error:', error))
        page.on('requestfailed', lambda request: print('Request failed:', request.url, request.failure))
        page.on('request', lambda request: edit_requests.append(request.post_data_buffer or b'') if '/playground/images/edits' in request.url else None)
        mock_api(page)
        seed_browser(page)

        page.goto(f"{BASE_URL}/playground/gallery", wait_until="networkidle")
        print('Gallery URL:', page.url)
        print('Gallery body:', page.locator('body').inner_text()[:1000])
        page.screenshot(path=str(screenshot_dir / 'local-gallery-debug.png'), full_page=True)
        page.get_by_role('link', name='无限画布').wait_for(state='visible')
        assert page.locator('article').count() >= 1
        page.screenshot(path=str(screenshot_dir / 'local-gallery-nav.png'), full_page=True)

        page.get_by_role('link', name='无限画布').click()
        page.wait_for_url('**/playground/images?view=canvas')
        page.locator('[data-canvas-header]').wait_for(state='visible')
        page.locator('[data-canvas-workspace-tabs]').get_by_role('link', name='图库').wait_for(state='visible')
        text_node = page.locator('[data-node-id="smoke-text"]')
        text_node.wait_for(state='visible')
        assert text_node.locator('[data-canvas-text-preview]').inner_text() == '请写一个适合儿童绘本的太空探险场景。'
        assert '等待生成结果' not in text_node.inner_text()
        image_node = page.locator('[data-node-id="smoke-image"]')
        image_node.locator('img').click(position={"x": 180, "y": 180})
        print('Image toolbar count:', page.locator('[data-canvas-hover-toolbar]').count())
        print('Image toolbar titles:', page.locator('[data-canvas-hover-toolbar] button').evaluate_all("buttons => buttons.map(button => button.title)"))
        mask_button = page.locator('[data-canvas-hover-toolbar] button[title*="蒙版"]')
        mask_button.wait_for(state='visible')
        mask_button.click()
        dialog = page.locator('[data-image-mask-dialog]')
        dialog.wait_for(state='visible')
        assert dialog.locator('[data-image-mask-erase]').is_visible()
        assert dialog.locator('[data-image-mask-paint]').is_visible()
        dialog.locator('[data-image-mask-prompt]').fill('把圆形太阳改成一颗蓝色星球')
        mask_canvas = dialog.locator('canvas').last
        box = mask_canvas.bounding_box()
        assert box
        page.mouse.move(box['x'] + box['width'] * 0.5, box['y'] + box['height'] * 0.5)
        page.mouse.down()
        page.mouse.move(box['x'] + box['width'] * 0.7, box['y'] + box['height'] * 0.5, steps=4)
        page.mouse.up()
        assert dialog.locator('[data-image-mask-apply]').is_enabled()
        page.screenshot(path=str(screenshot_dir / 'local-mask-dialog.png'), full_page=True)
        dialog.locator('[data-image-mask-undo]').click()
        assert dialog.locator('[data-image-mask-redo]').is_enabled()
        dialog.locator('[data-image-mask-redo]').click()
        dialog.locator('[data-image-mask-apply]').click()
        page.locator('[data-node-id^="mask-smoke-image-"]').wait_for(state='visible')
        result_node = page.locator('[data-node-id^="mask-smoke-image-"]')
        prompt_bytes = '把圆形太阳改成一颗蓝色星球'.encode('utf-8')
        assert any(prompt_bytes in body for body in edit_requests)
        page.screenshot(path=str(screenshot_dir / 'local-mask-edit.png'), full_page=True)
        page.screenshot(path=str(screenshot_dir / 'local-canvas-nav.png'), full_page=True)

        page.get_by_role('link', name='图库').click()
        page.wait_for_url('**/playground/gallery')
        page.get_by_role('link', name='无限画布').wait_for(state='visible')
        assert page.locator('article').count() >= 1
        print('PASS: gallery fixture, canvas fixture, gallery -> canvas -> gallery navigation, and node layout checks')
        print(f'Gallery screenshot: {screenshot_dir / "local-gallery-nav.png"}')
        print(f'Canvas screenshot: {screenshot_dir / "local-canvas-nav.png"}')
        context.close()
        browser.close()


if __name__ == '__main__':
    main()
