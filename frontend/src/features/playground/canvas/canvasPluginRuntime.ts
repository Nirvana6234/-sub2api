import React, { type ComponentType, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import type { CanvasNode } from './types'

export interface CanvasPluginRuntimeContext {
  node: { id: string; type: string; title: string; metadata: Record<string, unknown> }
  theme: { node: { fill: string; text: string; stroke: string; placeholder: string }; toolbar: { panel: string; activeBg: string; activeText: string } }
  scale: number
  isSelected: boolean
  updateMetadata: (patch: Record<string, unknown>) => void
  updateNode: (patch: { title?: string; width?: number; height?: number }) => void
  getNode: (id: string) => CanvasNode | null
  getNodes: () => CanvasNode[]
  getConnections: () => Array<{ id: string; from: string; to: string; kind?: string }>
  getUpstream: () => CanvasNode[]
  getDownstream: () => CanvasNode[]
  applyOps: (ops: Record<string, unknown>[]) => void
  emit: (event: string, payload?: unknown) => void
  on: (event: string, handler: (payload: unknown) => void) => () => void
  ai: {
    generateImage: (prompt: string, options?: { signal?: AbortSignal; references?: string[]; model?: string; count?: number; size?: string; quality?: string; background?: string }) => Promise<{ images: string[] }>
    generateVideo: (prompt: string, options?: { signal?: AbortSignal; references?: string[]; model?: string; size?: string; seconds?: string; aspectRatio?: string }) => Promise<{ url: string; mimeType: string; width?: number; height?: number; durationMs?: number }>
    generateText: (prompt: string, options?: { signal?: AbortSignal; model?: string; system?: string; onDelta?: (text: string) => void }) => Promise<{ text: string }>
    generateAudio: (prompt: string, options?: { signal?: AbortSignal; model?: string; voice?: string; language?: string }) => Promise<{ url: string; mimeType: string }>
    listModels: (capability?: 'image' | 'video' | 'text' | 'audio') => Array<{ value: string; label: string }>
    defaultModel: (capability?: 'image' | 'video' | 'text' | 'audio') => string
  }
  openPanel: () => void
  closePanel: () => void
  storage: { get: <T = unknown>(key: string) => Promise<T | null>; set: (key: string, value: unknown) => Promise<void>; remove: (key: string) => Promise<void> }
}

export interface CanvasPluginRuntimeNodeDefinition {
  type: string
  title: string
  icon?: ReactNode
  description?: string
  defaultSize?: { width: number; height: number }
  defaultMetadata?: Record<string, unknown>
  showInCreateMenu?: boolean
  hidePanel?: boolean
  autoOpenPanel?: boolean
  useBuiltinPanel?: { mode: 'image' | 'video' | 'text' | 'audio'; promptPrefix?: string; writeBackToSelf?: boolean }
  interactionToggle?: boolean
  forceInteractive?: (node: CanvasNode) => boolean
  keepAspectRatio?: (node: CanvasNode) => boolean
  resource?: (node: CanvasNode) => { kind: string; text?: string; url?: string } | null
  onDoubleClick?: (ctx: CanvasPluginRuntimeContext) => boolean
  transparentBackground?: boolean
  Content?: ComponentType<{ ctx: CanvasPluginRuntimeContext }>
  Panel?: ComponentType<{ ctx: CanvasPluginRuntimeContext; onClose: () => void }>
  toolbar?: (ctx: CanvasPluginRuntimeContext) => Array<{ id: string; title: string; label: string; icon?: ReactNode; active?: boolean; danger?: boolean; onClick: () => void }>
}

export interface CanvasPluginRuntimePlugin {
  id: string
  name: string
  version: string
  description?: string
  css?: string
  nodes: CanvasPluginRuntimeNodeDefinition[]
  setup?: (app: { version: string; emit: (event: string, payload?: unknown) => void; on: (event: string, handler: (payload: unknown) => void) => () => void; injectCSS: (css: string, key?: string) => () => void }) => void | (() => void)
}

export interface CanvasPluginMount {
  update: (ctx: CanvasPluginRuntimeContext) => void
  unmount: () => void
}

const definitions = new Map<string, CanvasPluginRuntimeNodeDefinition>()
const cleanups = new Map<string, () => void>()
const storagePrefix = 'sub2api.playground.canvas.plugin.'
const eventTarget = typeof window !== 'undefined' ? new EventTarget() : null

export function getCanvasPluginDefinition(type: string): CanvasPluginRuntimeNodeDefinition | undefined {
  return definitions.get(type)
}

export function registerCanvasPlugin(plugin: CanvasPluginRuntimePlugin): CanvasPluginRuntimePlugin {
  unregisterCanvasPlugin(plugin.id)
  plugin.nodes.forEach((definition) => definitions.set(definition.type, definition))
  const disposers: Array<() => void> = []
  if (plugin.css) disposers.push(injectPluginCss(plugin.css, plugin.id))
  const cleanup = plugin.setup?.(getCanvasPluginApp())
  if (typeof cleanup === 'function') disposers.push(cleanup)
  if (disposers.length) cleanups.set(plugin.id, () => disposers.forEach((dispose) => dispose()))
  return plugin
}

export function unregisterCanvasPlugin(pluginId: string): void {
  cleanups.get(pluginId)?.()
  cleanups.delete(pluginId)
  for (const [type] of definitions) if (type.startsWith(`${pluginId}:`)) definitions.delete(type)
}

export async function evaluateCanvasPluginSource(source: string): Promise<CanvasPluginRuntimePlugin> {
  const reactBridge = createModuleUrl(`const r=globalThis.__SUB2API_REACT__; export default r; export const createElement=r.createElement; export const Fragment=r.Fragment; export const useState=r.useState; export const useEffect=r.useEffect; export const useLayoutEffect=r.useLayoutEffect; export const useMemo=r.useMemo; export const useCallback=r.useCallback; export const useRef=r.useRef; export const useReducer=r.useReducer; export const useContext=r.useContext; export const useId=r.useId;`)
  const jsxBridge = createModuleUrl(`const r=globalThis.__SUB2API_REACT__; export const jsx=r.createElement; export const jsxs=r.createElement; export const Fragment=r.Fragment; export const jsxDEV=r.createElement;`)
  const sdkBridge = createModuleUrl(`const r=globalThis.__SUB2API_REACT__; export const definePlugin=(value)=>value; export const getReact=()=>r; export const getRuntime=()=>globalThis.InfiniteCanvasRuntime; export const useState=r.useState; export const useEffect=r.useEffect; export const useLayoutEffect=r.useLayoutEffect; export const useMemo=r.useMemo; export const useCallback=r.useCallback; export const useRef=r.useRef; export const useReducer=r.useReducer; export const useContext=r.useContext; export const useId=r.useId;`)
  ;(globalThis as Record<string, unknown>).__SUB2API_REACT__ = React
  ;(globalThis as Record<string, unknown>).InfiniteCanvasRuntime = getCanvasPluginRuntime()
  const transformed = source
    .replace(/(['"])react\/jsx-runtime\1/g, JSON.stringify(jsxBridge))
    .replace(/(['"])react\1/g, JSON.stringify(reactBridge))
    .replace(/(['"])@infinite-canvas\/plugin-sdk\1/g, JSON.stringify(sdkBridge))
  const sourceUrl = createModuleUrl(transformed, 'text/javascript')
  try {
    const mod = await import(/* @vite-ignore */ sourceUrl) as { default?: unknown; plugin?: unknown }
    const exported = mod.default ?? mod.plugin
    const plugin = typeof exported === 'function' ? (exported as (runtime: unknown) => unknown)(getCanvasPluginRuntime()) : exported
    if (!plugin || typeof plugin !== 'object') throw new Error('插件导出不是有效对象。')
    const value = plugin as Partial<CanvasPluginRuntimePlugin>
    if (typeof value.id !== 'string' || !Array.isArray(value.nodes) || !value.nodes.length) throw new Error('插件缺少 id 或 nodes。')
    return value as CanvasPluginRuntimePlugin
  } finally {
    URL.revokeObjectURL(sourceUrl)
    URL.revokeObjectURL(reactBridge)
    URL.revokeObjectURL(jsxBridge)
    URL.revokeObjectURL(sdkBridge)
  }
}

export async function loadCanvasPluginSource(source: string): Promise<CanvasPluginRuntimePlugin> {
  return registerCanvasPlugin(await evaluateCanvasPluginSource(source))
}

export function mountCanvasPluginContent(definition: CanvasPluginRuntimeNodeDefinition, element: HTMLElement, ctx: CanvasPluginRuntimeContext): CanvasPluginMount {
  const Content = definition.Content
  if (!Content) return { update: () => undefined, unmount: () => undefined }
  const root: Root = createRoot(element)
  const render = (nextContext: CanvasPluginRuntimeContext) => root.render(React.createElement(Content, { ctx: nextContext }))
  render(ctx)
  return { update: render, unmount: () => root.unmount() }
}

export function mountCanvasPluginPanel(definition: CanvasPluginRuntimeNodeDefinition, element: HTMLElement, ctx: CanvasPluginRuntimeContext, onClose: () => void): CanvasPluginMount {
  const Panel = definition.Panel
  if (!Panel) return { update: () => undefined, unmount: () => undefined }
  const root: Root = createRoot(element)
  const render = (nextContext: CanvasPluginRuntimeContext) => root.render(React.createElement(Panel, { ctx: nextContext, onClose }))
  render(ctx)
  return { update: render, unmount: () => root.unmount() }
}

function createModuleUrl(source: string, type = 'text/javascript'): string {
  return URL.createObjectURL(new Blob([source], { type }))
}

function getCanvasPluginRuntime() {
  return {
    React,
    jsx: React.createElement,
    Fragment: React.Fragment,
    version: 'sub2api',
    emit: (event: string, payload?: unknown) => eventTarget?.dispatchEvent(new CustomEvent(`canvas-plugin:${event}`, { detail: payload })),
    on: (event: string, handler: (payload: unknown) => void) => {
      const listener = (value: Event) => handler((value as CustomEvent).detail)
      eventTarget?.addEventListener(`canvas-plugin:${event}`, listener)
      return () => eventTarget?.removeEventListener(`canvas-plugin:${event}`, listener)
    },
    injectCSS: injectPluginCss,
  }
}

function getCanvasPluginApp() {
  const runtime = getCanvasPluginRuntime()
  return { version: runtime.version, emit: runtime.emit, on: runtime.on, injectCSS: runtime.injectCSS }
}

function injectPluginCss(css: string, key?: string): () => void {
  if (typeof document === 'undefined') return () => undefined
  const id = key ? `canvas-plugin-style-${key}` : undefined
  if (id) document.getElementById(id)?.remove()
  const style = document.createElement('style')
  if (id) style.id = id
  style.dataset.canvasPluginStyle = 'true'
  style.textContent = css
  document.head.appendChild(style)
  return () => style.remove()
}

export function createPluginStorage(pluginId: string) {
  const keyFor = (key: string) => `${storagePrefix}${pluginId}.${key}`
  return {
    async get<T = unknown>(key: string): Promise<T | null> {
      try { return JSON.parse(localStorage.getItem(keyFor(key)) || 'null') as T | null } catch { return null }
    },
    async set(key: string, value: unknown): Promise<void> { localStorage.setItem(keyFor(key), JSON.stringify(value)) },
    async remove(key: string): Promise<void> { localStorage.removeItem(keyFor(key)) },
  }
}
