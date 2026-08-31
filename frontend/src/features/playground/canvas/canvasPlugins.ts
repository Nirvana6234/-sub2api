import type { CanvasNodeType } from './types'
import { evaluateCanvasPluginSource, registerCanvasPlugin, type CanvasPluginRuntimePlugin } from './canvasPluginRuntime'

export interface CanvasPluginNodeDefinition {
  name: string
  type: `${string}:${string}`
  title: string
  description?: string
  defaultSize?: { width?: number; height?: number }
  minimapColor?: string
  renderer?: 'generic' | 'markdown' | 'sticky-note' | 'svg' | 'html' | 'panorama'
  defaultMetadata?: Record<string, unknown>
  showInCreateMenu?: boolean
  hidePanel?: boolean
  autoOpenPanel?: boolean
  transparentBackground?: boolean
  interactionToggle?: boolean
  useBuiltinPanel?: { mode: 'image' | 'video' | 'text' | 'audio'; promptPrefix?: string; writeBackToSelf?: boolean }
}

export interface CanvasPluginManifest {
  id: string
  name: string
  version: string
  description?: string
  url: string
  enabled: boolean
  installedAt: number
  nodes: CanvasPluginNodeDefinition[]
  source?: string
}

export interface CanvasPluginCatalogEntry {
  id: string
  name: string
  version: string
  description?: string
  icon?: string
  url: string
}

export const OFFICIAL_PLUGIN_CATALOG_URL = 'https://cdn.jsdelivr.net/gh/basketikun/infinite-canvas@plugins-dist/official-plugins.json'

const STORAGE_KEY = 'sub2api.playground.canvas.plugins.v1'
const MAX_SOURCE_BYTES = 512 * 1024

const DEFAULT_PLUGINS: CanvasPluginManifest[] = [
  { id: 'markdown', name: 'Markdown 节点', version: '1.1.0', description: '在画布中编辑与渲染 Markdown', url: 'builtin://markdown', enabled: true, installedAt: 0, nodes: [{ name: 'doc', type: 'markdown:doc', title: 'Markdown', description: '编辑与渲染 Markdown', defaultSize: { width: 360, height: 300 }, renderer: 'markdown', hidePanel: true, defaultMetadata: { content: '# Markdown\\n\\n双击节点或使用右侧编辑器开始。' } }] },
  { id: 'sticky-note', name: '便利贴节点', version: '1.1.0', description: '可自选颜色、双击编辑、拖动即可移动的便利贴', url: 'builtin://sticky-note', enabled: true, installedAt: 0, nodes: [{ name: 'note', type: 'sticky-note:note', title: '便利贴', description: '彩色便利贴', defaultSize: { width: 240, height: 200 }, renderer: 'sticky-note', hidePanel: true, defaultMetadata: { content: '双击编辑便利贴', pluginColor: '#fde68a' } }] },
  { id: 'svg', name: 'SVG 节点', version: '1.1.0', description: '编辑与渲染 SVG', url: 'builtin://svg', enabled: true, installedAt: 0, nodes: [{ name: 'render', type: 'svg:render', title: 'SVG', description: '安全渲染 SVG 源码', defaultSize: { width: 420, height: 300 }, renderer: 'svg', hidePanel: true, defaultMetadata: { content: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 240"><rect width="400" height="240" rx="24" fill="#0f766e"/><circle cx="200" cy="120" r="70" fill="#99f6e4"/></svg>' } }] },
  { id: 'html', name: 'HTML 节点', version: '1.2.0', description: '沙箱 iframe 渲染 HTML', url: 'builtin://html', enabled: true, installedAt: 0, nodes: [{ name: 'render', type: 'html:render', title: 'HTML', description: '沙箱渲染 HTML', defaultSize: { width: 420, height: 320 }, renderer: 'html', hidePanel: true, defaultMetadata: { content: '<!doctype html><html><body style="font-family:system-ui;padding:24px"><h2>HTML 节点</h2><p>双击编辑 HTML 源码。</p></body></html>' } }] },
  { id: 'panorama', name: '3D 全景节点', version: '1.0.0', description: '拖拽浏览等距柱状 360° 全景图', url: 'builtin://panorama', enabled: true, installedAt: 0, nodes: [{ name: 'viewer', type: 'panorama:viewer', title: '3D 全景', description: '拖拽旋转、滚轮缩放全景图', defaultSize: { width: 480, height: 300 }, renderer: 'panorama', hidePanel: true, defaultMetadata: { content: '' } }] },
]

export function loadCanvasPlugins(): CanvasPluginManifest[] {
  if (typeof localStorage === 'undefined') return []
  try {
    const value = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]') as unknown
    if (!Array.isArray(value)) return []
    const stored = value.flatMap(normalizeManifest)
    return [...stored, ...DEFAULT_PLUGINS.filter((plugin) => !stored.some((item) => item.id === plugin.id))].slice(0, 50)
  } catch {
    return DEFAULT_PLUGINS.map((plugin) => ({ ...plugin, nodes: plugin.nodes.map((node) => ({ ...node, defaultMetadata: node.defaultMetadata ? { ...node.defaultMetadata } : undefined })) }))
  }
}

export function saveCanvasPlugins(plugins: CanvasPluginManifest[]): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(STORAGE_KEY, JSON.stringify(plugins.slice(0, 50)))
}

export async function fetchCanvasPluginCatalog(): Promise<CanvasPluginCatalogEntry[]> {
  const response = await fetch(OFFICIAL_PLUGIN_CATALOG_URL, { headers: { Accept: 'application/json' } })
  if (!response.ok) throw new Error(`官方插件目录下载失败（HTTP ${response.status}）。`)
  const payload = JSON.parse(await response.text()) as { plugins?: unknown }
  if (!Array.isArray(payload.plugins)) return []
  return payload.plugins.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return []
    const value = entry as Record<string, unknown>
    const id = typeof value.id === 'string' ? value.id.trim() : ''
    const entryFile = typeof value.entry === 'string' ? value.entry.trim() : ''
    if (!id || !entryFile || !/^[a-z0-9][a-z0-9._-]{0,100}$/i.test(entryFile)) return []
    return [{ id, name: typeof value.name === 'string' && value.name.trim() ? value.name.trim() : id, version: typeof value.version === 'string' ? value.version : '', description: typeof value.description === 'string' ? value.description : undefined, icon: typeof value.icon === 'string' ? value.icon : undefined, url: `https://cdn.jsdelivr.net/gh/basketikun/infinite-canvas@plugins-dist/${encodeURIComponent(entryFile)}` }]
  }).slice(0, 50)
}

export async function installCanvasPluginFromUrl(url: string): Promise<CanvasPluginManifest> {
  const normalizedUrl = url.trim()
  if (!/^https?:\/\//i.test(normalizedUrl)) throw new Error('插件地址必须使用 HTTP 或 HTTPS。')
  const response = await fetch(normalizedUrl, { headers: { Accept: 'application/json' } })
  if (!response.ok) throw new Error(`插件清单下载失败（HTTP ${response.status}）。`)
  const source = await response.text()
  if (new TextEncoder().encode(source).byteLength > MAX_SOURCE_BYTES) throw new Error('插件清单超过大小限制。')
  let manifest = normalizeManifest(parseJson(source))[0]
  let runtimeSource: string | undefined
  if (!manifest) {
    let plugin: CanvasPluginRuntimePlugin
    try { plugin = await evaluateCanvasPluginSource(source) } catch { throw new Error('插件清单不是有效 JSON，也不是可加载的 ESM 插件。') }
    manifest = runtimePluginToManifest(plugin, normalizedUrl)
    runtimeSource = source
    registerCanvasPlugin(plugin)
  }
  const next = { ...manifest, url: normalizedUrl, enabled: true, installedAt: Date.now(), ...(runtimeSource ? { source: runtimeSource } : {}) }
  const plugins = loadCanvasPlugins().filter((item) => item.id !== next.id)
  saveCanvasPlugins([next, ...plugins])
  return next
}

export function setCanvasPluginEnabled(id: string, enabled: boolean): CanvasPluginManifest[] {
  const next = loadCanvasPlugins().map((plugin) => plugin.id === id ? { ...plugin, enabled } : plugin)
  saveCanvasPlugins(next)
  return next
}

export function uninstallCanvasPlugin(id: string): CanvasPluginManifest[] {
  const next = loadCanvasPlugins().filter((plugin) => plugin.id !== id)
  saveCanvasPlugins(next)
  return next
}

export function pluginNodeType(pluginId: string, name: string): `${string}:${string}` {
  return `${pluginId}:${name}` as `${string}:${string}`
}

function normalizeManifest(value: unknown): CanvasPluginManifest[] {
  if (!value || typeof value !== 'object') return []
  const source = value as Record<string, unknown>
  const id = typeof source.id === 'string' && /^[a-z0-9][a-z0-9_-]{1,63}$/i.test(source.id) ? source.id : ''
  const name = typeof source.name === 'string' && source.name.trim() ? source.name.trim().slice(0, 120) : id
  const version = typeof source.version === 'string' && source.version.trim() ? source.version.trim().slice(0, 40) : ''
  const rawNodes = Array.isArray(source.nodes) ? source.nodes : []
  if (!id || !version || !rawNodes.length) return []
  const nodes = rawNodes.flatMap((item) => {
    if (!item || typeof item !== 'object') return []
    const node = item as Record<string, unknown>
    const nodeName = typeof node.name === 'string' && /^[a-z0-9][a-z0-9_-]{0,63}$/i.test(node.name) ? node.name : ''
    if (!nodeName) return []
    const size = node.defaultSize && typeof node.defaultSize === 'object' ? node.defaultSize as Record<string, unknown> : undefined
    return [{
      name: nodeName,
      type: pluginNodeType(id, nodeName),
      title: typeof node.title === 'string' && node.title.trim() ? node.title.trim().slice(0, 120) : nodeName,
      description: typeof node.description === 'string' ? node.description.slice(0, 500) : undefined,
      defaultSize: size ? { width: finite(size.width, 360), height: finite(size.height, 300) } : undefined,
      minimapColor: typeof node.minimapColor === 'string' ? node.minimapColor.slice(0, 30) : undefined,
      renderer: (node.renderer === 'markdown' || node.renderer === 'sticky-note' || node.renderer === 'svg' || node.renderer === 'html' || node.renderer === 'panorama' ? node.renderer : 'generic') as CanvasPluginNodeDefinition['renderer'],
      defaultMetadata: node.defaultMetadata && typeof node.defaultMetadata === 'object' && !Array.isArray(node.defaultMetadata) ? Object.fromEntries(Object.entries(node.defaultMetadata).slice(0, 30).filter(([, value]) => typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean')) : undefined,
      showInCreateMenu: node.showInCreateMenu !== false,
      hidePanel: node.hidePanel === true,
      autoOpenPanel: node.autoOpenPanel === true,
      transparentBackground: node.transparentBackground === true,
      interactionToggle: node.interactionToggle === true,
      useBuiltinPanel: normalizeBuiltinPanel(node.useBuiltinPanel),
    }]
  }).slice(0, 50)
  if (!nodes.length) return []
  return [{ id, name, version, description: typeof source.description === 'string' ? source.description.slice(0, 500) : undefined, url: typeof source.url === 'string' ? source.url : '', enabled: source.enabled !== false, installedAt: finite(source.installedAt, Date.now()), nodes, source: typeof source.source === 'string' ? source.source.slice(0, MAX_SOURCE_BYTES) : undefined }]
}

function runtimePluginToManifest(plugin: CanvasPluginRuntimePlugin, url: string): CanvasPluginManifest {
  const nodes = plugin.nodes.flatMap((node) => {
    const type = typeof node.type === 'string' ? node.type : ''
    const name = type.split(':')[1] || type
    if (!/^[a-z0-9][a-z0-9_-]{0,63}$/i.test(name)) return []
    return [{ name, type: pluginNodeType(plugin.id, name), title: node.title || name, description: node.description, defaultSize: node.defaultSize, renderer: 'generic' as const, defaultMetadata: node.defaultMetadata, showInCreateMenu: node.showInCreateMenu !== false, hidePanel: node.hidePanel === true, autoOpenPanel: node.autoOpenPanel === true, transparentBackground: node.transparentBackground === true, interactionToggle: node.interactionToggle === true, useBuiltinPanel: node.useBuiltinPanel }]
  })
  if (!nodes.length) throw new Error('插件没有有效节点定义。')
  return { id: plugin.id, name: plugin.name || plugin.id, version: plugin.version || '0.0.0', description: plugin.description, url, enabled: true, installedAt: Date.now(), nodes }
}

function parseJson(value: string): unknown {
  try { return JSON.parse(value) as unknown } catch { return null }
}

function finite(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(80, Math.min(1200, value)) : fallback
}

function normalizeBuiltinPanel(value: unknown): CanvasPluginNodeDefinition['useBuiltinPanel'] {
  if (!value || typeof value !== 'object') return undefined
  const source = value as Record<string, unknown>
  if (source.mode !== 'image' && source.mode !== 'video' && source.mode !== 'text' && source.mode !== 'audio') return undefined
  return { mode: source.mode, promptPrefix: typeof source.promptPrefix === 'string' ? source.promptPrefix.slice(0, 500) : undefined, writeBackToSelf: source.writeBackToSelf === true }
}

export function isPluginNodeType(type: CanvasNodeType): boolean {
  return /^[a-z0-9][a-z0-9_-]{1,63}:[a-z0-9][a-z0-9_-]{0,63}$/i.test(type)
}
