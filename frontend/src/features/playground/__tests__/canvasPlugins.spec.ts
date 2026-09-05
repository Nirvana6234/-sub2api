import { beforeEach, describe, expect, it, vi } from 'vitest'
import { installCanvasPluginFromUrl, loadCanvasPlugins, setCanvasPluginEnabled, uninstallCanvasPlugin } from '../canvas/canvasPlugins'

describe('canvas plugin manifests', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('installs and normalizes a declarative manifest', async () => {
    expect(loadCanvasPlugins().some((plugin) => plugin.id === 'markdown')).toBe(true)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      id: 'demo-plugin',
      name: 'Demo Plugin',
      version: '1.0.0',
      nodes: [{ name: 'note', title: 'Plugin note', defaultSize: { width: 480, height: 320 } }],
    }), { status: 200, headers: { 'content-type': 'application/json' } })))

    const installed = await installCanvasPluginFromUrl('https://example.com/demo.json')
    expect(installed.nodes[0].type).toBe('demo-plugin:note')
    expect(loadCanvasPlugins().find((plugin) => plugin.id === 'demo-plugin')?.nodes[0].type).toBe('demo-plugin:note')
  })

  it('rejects non-http URLs and supports enable/disable/uninstall', async () => {
    await expect(installCanvasPluginFromUrl('file:///tmp/plugin.json')).rejects.toThrow('HTTP')
    localStorage.setItem('sub2api.playground.canvas.plugins.v1', JSON.stringify([{
      id: 'demo-plugin', name: 'Demo', version: '1', url: 'https://example.com', enabled: true, installedAt: 1, nodes: [{ name: 'note', type: 'demo-plugin:note', title: 'Note' }],
    }]))
    expect(setCanvasPluginEnabled('demo-plugin', false)[0].enabled).toBe(false)
    expect(uninstallCanvasPlugin('demo-plugin').some((plugin) => plugin.id === 'demo-plugin')).toBe(false)
  })
})
