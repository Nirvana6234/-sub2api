import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { arrangeCanvasNodes } from '../canvas/canvasLayout'
import type { CanvasConnection, CanvasNode } from '../canvas/types'

describe('playground canvas layout integration', () => {
  const source = readFileSync(resolve(__dirname, '../../../views/user/PlaygroundCanvasView.vue'), 'utf8')
  const nodeSource = readFileSync(resolve(__dirname, '../canvas/CanvasNode.vue'), 'utf8')
  const toolbar = readFileSync(resolve(__dirname, '../canvas/CanvasToolbar.vue'), 'utf8')
  const minimap = readFileSync(resolve(__dirname, '../canvas/CanvasMiniMap.vue'), 'utf8')
  const assistant = readFileSync(resolve(__dirname, '../canvas/CanvasAssistant.vue'), 'utf8')
  const mentionContent = readFileSync(resolve(__dirname, '../canvas/CanvasMentionContent.vue'), 'utf8')
  const nodeInfo = readFileSync(resolve(__dirname, '../canvas/CanvasNodeInfoDialog.vue'), 'utf8')
  const referenceBar = readFileSync(resolve(__dirname, '../canvas/CanvasNodeReferenceBar.vue'), 'utf8')
  const mentionEditor = readFileSync(resolve(__dirname, '../canvas/CanvasMentionEditor.vue'), 'utf8')
  const connectionMenu = readFileSync(resolve(__dirname, '../canvas/CanvasConnectionContextMenu.vue'), 'utf8')
  const configSettings = readFileSync(resolve(__dirname, '../canvas/CanvasConfigSettingsPopover.vue'), 'utf8')
  const contextMenu = readFileSync(resolve(__dirname, '../canvas/CanvasContextMenu.vue'), 'utf8')
  const gallerySource = readFileSync(resolve(__dirname, '../../../views/user/PlaygroundGalleryView.vue'), 'utf8')
  const maskSource = readFileSync(resolve(__dirname, '../canvas/CanvasImageMaskDialog.vue'), 'utf8')

  it('keeps gallery navigation connected to the infinite canvas workspace', () => {
    expect(gallerySource).toContain('to="/playground/images?view=canvas"')
    expect(gallerySource).toContain("t('playground.canvasWorkspace')")
  })

  it('supports targeted image edits with a prompt, mask tools, and mask history', () => {
    expect(maskSource).toContain('data-image-mask-prompt')
    expect(maskSource).toContain("defineEmits<{ close: []; apply: [dataUrl: string, prompt: string] }>()")
    expect(maskSource).toContain('data-image-mask-erase')
    expect(maskSource).toContain('data-image-mask-paint')
    expect(maskSource).toContain('data-image-mask-undo')
    expect(maskSource).toContain('data-image-mask-redo')
    expect(maskSource).toContain('globalCompositeOperation = stroke.mode === \'erase\' ? \'destination-out\' : \'source-over\'')
    expect(source).toContain('async function applyImageMask(maskDataUrl: string, editPrompt: string)')
    expect(source).toContain('const prompt = editPrompt.trim()')
    expect(source).toContain('mask: { id: `mask-${now}`')
    expect(source).toContain('prompt,\n    model,')
  })

  it('collapses the main app sidebar when the full canvas workspace opens', () => {
    expect(source).toContain('appStore.setSidebarCollapsed(true)')
    expect(source).toContain('appStore.setMobileOpen(false)')
    expect(source).not.toContain('if (!props.embedded)')
    expect(source).toContain('sidebarCollapsedBeforeCanvas = appStore.sidebarCollapsed')
    expect(source).toContain('appStore.setSidebarCollapsed(sidebarCollapsedBeforeCanvas)')
  })

  it('keeps the canvas toolbar dock compact and hides the clear-selection action when nothing is selected', () => {
    expect(toolbar).toContain('bottom-5')
    expect(toolbar).toContain('overflow-x-auto')
    expect(toolbar).toContain('v-if="selectedCount"')
    expect(toolbar).toContain('flex h-14')
    expect(toolbar).toContain("@click=\"emit('arrange')\"")
    expect(source).toContain('@arrange="arrangeCanvas"')
    expect(source).toContain('function arrangeCanvas()')
  })

  it('preserves previous image generations instead of deleting derived nodes', () => {
    expect(source).toContain('function archiveImageGeneration(node: CanvasNodeData)')
    expect(source).not.toContain('canvasStore.removeDerivedNodes(id)')
  })

  it('arranges connected nodes into stable left-to-right layers', () => {
    const makeNode = (id: string, x: number, y: number): CanvasNode => ({
      id, type: 'image', kind: 'generator', x, y, width: 200, height: 120,
      prompt: id, model: 'test', status: 'idle', createdAt: 1, updatedAt: 1,
    })
    const nodes = [makeNode('target', 900, 800), makeNode('source', 500, 100), makeNode('middle', 100, 500)]
    const connections: CanvasConnection[] = [
      { id: 'one', from: 'source', to: 'target', kind: 'reference' },
      { id: 'two', from: 'middle', to: 'source', kind: 'reference' },
    ]
    const positions = arrangeCanvasNodes(nodes, connections)
    expect(positions.middle.x).toBeLessThan(positions.source.x)
    expect(positions.source.x).toBeLessThan(positions.target.x)
    expect(positions.middle).toEqual({ x: 48, y: 48 })
  })

  it('keeps the current viewport when arranging nodes', () => {
    const arrangeStart = source.indexOf('function arrangeCanvas()')
    const arrangeEnd = source.indexOf('\nfunction resetCanvasView', arrangeStart)
    const arrangeSource = source.slice(arrangeStart, arrangeEnd)
    expect(arrangeSource).not.toContain('fitCanvas(')
    expect(arrangeSource).toContain('canvasStore.updateNode(node.id, position)')
  })

  it('supports collapsing the canvas projects sidebar to maximize workspace space', () => {
    expect(source).toContain('projectsPanelCollapsed')
    expect(source).toContain('toggleProjectsPanel')
    expect(source).toContain('canvasProjectsCollapse')
    expect(source).toContain('canvasProjectsExpand')
    expect(source).toContain('PROJECTS_PANEL_COLLAPSED_STORAGE_KEY')
    expect(source).toContain('rememberProjectsPanelCollapsed')
  })

  it('shows a focusable, collapsible element tree in the canvas sidebar', () => {
    expect(source).toContain('canvasElements')
    expect(source).toContain('canvasNodeRows')
    expect(source).toContain('collapsedCanvasGroups')
    expect(source).toContain('toggleCanvasGroupCollapse')
    expect(source).toContain('focusCanvasNode')
    expect(source).toContain('canvasNodePreview')
    expect(source).toContain('marginLeft: `${row.depth * 12}px`')
    expect(source).toContain('canvasPanelTab')
    expect(source).toContain('canvasPanelTabs')
    expect(source).toContain('canvasNodeSearch')
    expect(source).toContain('canvasNodeFilter')
    expect(source).toContain('exportSelectedCanvasElements')
    expect(source).toContain('startCanvasPanelResize')
  })

  it('reuses canvas media and text nodes through the assets panel', () => {
    expect(source).toContain('type CanvasAssetItem')
    expect(source).toContain("kind: 'image' | 'video' | 'audio' | 'text'")
    expect(source).toContain('const canvasAssets = computed<CanvasAssetItem[]>(() =>')
    expect(source).toContain('node:${node.id}:video')
    expect(source).toContain('node:${node.id}:audio')
    expect(source).toContain('node:${node.id}:text')
    expect(source).toContain('<video v-else-if="asset.kind === \'video\'')
    expect(source).toContain('<audio v-else-if="asset.kind === \'audio\'')
    expect(source).toContain('function insertCanvasAsset(asset: CanvasAssetItem)')
    expect(source).toContain('canvasAssetKindLabel')
  })

  it('persists reusable assets and exposes save/remove actions', () => {
    expect(source).toContain('listCanvasSavedAssets')
    expect(source).toContain('persistCanvasAsset')
    expect(source).toContain('removeCanvasSavedAsset')
    expect(source).toContain('function saveNodeAsCanvasAsset')
    expect(source).toContain('function removeSavedCanvasAsset')
    expect(source).toContain('@save-asset="saveNodeAsCanvasAsset"')
    expect(nodeSource).toContain("'save-asset': [id: string, imageIndex?: number]")
    expect(nodeSource).toContain("t('playground.canvasSaveAsset')")
    expect(source).toContain(':assets="canvasAssets"')
    expect(source).toContain('@insert="insertAssetFromPicker"')
  })

  it('matches the reference canvas gesture model with marquee selection and temporary pan', () => {
    expect(source).toContain("const tool = ref<CanvasTool>('pan')")
    expect(source).toContain('@pointerdown.capture="handleCanvasPointerCapture"')
    expect(source).toContain('if (event.button === 1)')
    expect(source).toContain('setPointerCapture')
    expect(source).toContain('border-2 border-dashed')
    expect(source).toContain('const pointerTemporaryTool = (event.ctrlKey || event.metaKey) && !temporaryPanBaseTool.value')
    expect(source).toContain("if (nodeElement && currentTool !== 'connect' && !pointerTemporaryTool)")
    expect(source).toContain("if (event.button === 0 && currentTool === 'select' && isBackgroundClick)")
    expect(source).toContain('additive: event.shiftKey')
    expect(source).toContain('initialSelectedNodeIds: event.shiftKey ? new Set(selectedNodeIds.value) : new Set()')
  })

  it('adds a top canvas menu and shortcut guide like the reference infinite canvas app', () => {
    expect(source).toContain('canvasMenuOpen')
    expect(source).toContain('canvasOpenMenu')
    expect(source).toContain('data-canvas-menu')
    expect(source).toContain('canvasShortcuts')
    expect(source).toContain('canvasShortcutsHint')
    expect(source).toContain('newProjectFromMenu')
    expect(source).toContain('importCanvasFromMenu')
    expect(source).toContain('deleteProjectFromMenu')
  })

  it('documents plain drag marquee selection and modifier-based temporary pan', () => {
    expect(source).toContain("{ key: 'Drag', label: t('playground.canvasShortcutBoxSelect') }")
    expect(source).toContain("{ key: 'Ctrl / ⌘', label: t('playground.canvasShortcutTemporaryPan') }")
    expect(source).not.toContain("{ key: 'Ctrl / ⌘ + Drag', label: t('playground.canvasShortcutBoxSelect') }")
  })

  it('keeps header navigation and model selectors in non-overlapping responsive regions', () => {
    expect(source).toContain('data-canvas-header')
    expect(source).toContain('relative z-40 grid gap-4 rounded-2xl border border-gray-200/70 bg-white/70 p-3 shadow-sm backdrop-blur')
    expect(source).toContain('data-canvas-workspace-tabs')
    expect(source).toContain('data-canvas-model-controls')
    expect(source).toContain('2xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.6fr)]')
    expect(source).toContain('2xl:flex-row 2xl:items-center')
    expect(source).toContain('lg:grid-cols-2 2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]')
    expect(source).toContain('block max-w-full truncate text-left text-lg font-semibold')
  })

  it('keeps the empty-canvas add button outside canvas gesture handling', () => {
    expect(source).toContain('class="pointer-events-auto btn btn-primary mt-4 gap-1.5" data-canvas-no-zoom @pointerdown.stop @click="addNodeAtCenter()"')
  })

  it('lets text nodes stay editable in place without fighting canvas drag gestures', () => {
    expect(nodeSource).toContain('data-canvas-text-editor')
    expect(nodeSource).toContain('@pointerdown.stop')
    expect(nodeSource).toContain('@pointerdown.stop="emit(\'select\', node.id, $event)"')
    expect(nodeSource).toContain('@wheel.stop')
    expect(nodeSource).toContain('@dblclick.stop="focusTextEditor"')
    expect(nodeSource).not.toContain('@click="focusTextEditor"')
    expect(nodeSource).toContain("if (props.node.type === 'text' && props.node.kind !== 'result')")
    expect(nodeSource).toContain('textEditorRef')
    expect(nodeSource).toContain('handleOutsidePointerDown')
    expect(nodeSource).toContain('CanvasMentionEditor')
    expect(nodeSource).toContain('editor.focus()')
    expect(nodeSource).toContain("playground.canvasEditText")
    expect(nodeSource).toContain("playground.canvasTextPreview")
    expect(nodeSource).toContain('data-canvas-text-toolbar')
    expect(nodeSource).not.toContain('data-canvas-text-to-image')
    expect(nodeSource).toContain("@dblclick.stop=\"emit('view-image', node.id, index)\"")
    expect(nodeSource).toContain("if (props.node.type === 'image' && displayedImageUrl.value)")
    expect(nodeSource).toContain('const imageIndex = ref(Math.max(0, props.node.primaryImageIndex ?? 0))')
    expect(nodeSource).toContain('{ deep: true, immediate: true }')
    expect(nodeSource).toContain('toolbarPlacementClass')
    expect(nodeSource).toContain("props.node.y < 64 ? 'top-full mt-2' : '-top-11'")
    expect(nodeSource).toContain('adjustTextSize')
    expect(nodeSource).toContain("playground.canvasTextSmaller")
    expect(nodeSource).toContain("playground.canvasTextLarger")
    expect(nodeSource).toContain('v-if="node.type === \'text\'" class="relative bg-white p-3')
    expect(nodeSource).toContain("v-else-if=\"!pluginNode && (node.type === 'image' || node.type === 'video' || node.type === 'audio')\"")
    expect(nodeSource).toContain("node.type === 'image' && node.kind !== 'result'")
    expect(nodeSource).toContain("emit('generate-image-from-text', node.id)")
    expect(source).toContain("selectedNode.type === 'text' && (selectedNode.textContent || selectedNode.prompt).trim()")
    expect(source).toContain('function collectCanvasPromptNodes(project: CanvasProject, targetId: string)')
    expect(source).toContain('if (node.type === \'text\') continue')
    expect(source).toContain('const currentImageReference = await resolveCurrentImageReference(node)')
    expect(source).toContain('references.unshift(currentImageReference)')
    expect(source).toContain("height: type === 'text' ? 240")
    expect(source).toContain("selectedNode.type === 'image' && selectedNode.kind !== 'result'")
    expect(source).toContain("target?.closest('input,textarea,select,[contenteditable=\"true\"]')")
    expect(source).toContain('@generate-image-from-text="generateImageFromText"')
    expect(source).toContain('function generateImageFromText(id: string)')
    expect(source).toContain("const configId = addNode(source.x + source.width + 120, source.y, 'config')")
    expect(source).toContain("const imageId = addNode(source.x + source.width + 520, source.y, 'image')")
    expect(source).toContain('data-canvas-image-model')
    expect(source).toContain('function updateImageNodeModel')
    expect(source).toContain("from: source.id,\n    to: configId")
    expect(source).toContain("from: configId,\n    to: imageId")
    expect(source).toContain('canvasTextCount')
    expect(source).toContain('textVariants')
    expect(source).toContain('selectTextVariant')
    expect(nodeSource).toContain('data-canvas-text-variants')
    expect(nodeSource).toContain('data-canvas-text-variants-toggle')
    expect(nodeSource).toContain("emit('select-text-variant', node.id, variant.id)")
    expect(nodeSource).toContain('data-canvas-image-batch')
    expect(nodeSource).toContain('data-canvas-image-batch-toggle')
    expect(nodeSource).toContain('imageVariants')
    expect(nodeSource).toContain("'retry-image': [id: string, imageIndex: number]")
    expect(nodeSource).toContain("emit('retry-image', node.id, index)")
    expect(nodeSource).toContain("emit('select-image', props.node.id, index)")
    expect(nodeSource).toContain("emit('duplicate-image', node.id, index)")
    expect(nodeSource).toContain("emit('delete-image', node.id, index)")
    expect(source).toContain('@select-image="selectImageVariant"')
    expect(source).toContain('function selectImageVariant(id: string, imageIndex: number)')
    expect(source).toContain('function duplicateImageVariant(id: string, imageIndex: number)')
    expect(source).toContain('function deleteImageVariant(id: string, imageIndex: number)')
  })

  it('supports configurable batch image revisions, clipboard references, and click-only expansion', () => {
    expect(source).toContain('imageRevisionCount')
    expect(source).toContain('function generateImageChildVariants(id: string)')
    expect(source).toContain('tabindex="0" role="region"')
    expect(source).toContain('@paste.stop.prevent="handleReferencePaste"')
    expect(source).toContain('async function handleReferencePaste(event: ClipboardEvent)')
    expect(source).toContain('const prompt = (slot.revisionPrompt ?? \'\').trim()')
    expect(source).toContain('if (slot.status !== \'success\' || !slot.url || !isImageVariantRevised(source, index, slot)) continue')
    expect(source).toContain('const sourceImageIndex = node.kind === \'generator\'')
    expect(source).toContain('upstream.id === sourceImageNodeId ? sourceImageIndex ?? 0 : 0')
    // Hovering the batch strip must not toggle it — only the explicit toggle
    // button click should, since involuntary hover expansion was reported as
    // disruptive when scanning results.
    expect(nodeSource).not.toContain('@mouseenter="imageBatchExpanded = true"')
    expect(nodeSource).not.toContain('@mouseleave="imageBatchExpanded = false"')
    expect(nodeSource).toContain('@click.stop="imageBatchExpanded = !imageBatchExpanded"')
    expect(nodeSource).toContain(':value="slot.revisionPrompt ?? \'\'"')
  })

  it('allows mention lookup after punctuation so multiple assets can be referenced', () => {
    expect(mentionEditor).toContain('const match = /(^|[\\s\\p{P}\\p{S}])@([^\\s@]*)$/u.exec(textBeforeCaret())')
    expect(mentionEditor).toContain('const match = /(^|[\\s\\p{P}\\p{S}])@([^\\s@]*)$/u.exec(text)')
  })

  it('does not turn node action controls into accidental drags', () => {
    expect(source).toContain("target?.closest('button,video,audio,iframe')")
    expect(source).toContain('// Action controls inside a node should select it without stealing the')
  })

  it('uses an upstream-style drag threshold before mutating node positions', () => {
    expect(source).toContain('moved: boolean')
    expect(source).toContain('moved: false')
    expect(source).toContain('Math.hypot(event.clientX - dragging.value.pointerX, event.clientY - dragging.value.pointerY) < 3')
    expect(source).toContain('if (!dragging.value.moved) {')
    expect(source).toContain('canvasStore.checkpoint()')
    expect(source).toContain('if (dragging.value?.moved && activeProject.value)')
  })

  it('culls offscreen nodes with a safe viewport margin while preserving active interactions', () => {
    expect(source).toContain('const visibleCanvasNodes = computed<CanvasNodeData[]>(() =>')
    expect(source).toContain('const canvasViewportSize = ref({ width: 0, height: 0 })')
    expect(source).toContain('const padding = 280')
    expect(source).toContain('const preservedIds = new Set<string>([')
    expect(source).toContain('...(dragging.value?.nodeIds ?? [])')
    expect(source).toContain('connectingFrom.value')
    expect(source).toContain('connectionTargetNodeId.value')
    expect(source).toContain('v-for="node in visibleCanvasNodes"')
    expect(source).toContain('new ResizeObserver(refreshCanvasViewportSize)')
  })

  it('cleans up interrupted pointer gestures when the browser cancels or blurs', () => {
    expect(source).toContain("window.addEventListener('pointercancel', stopPointerAction")
    expect(source).toContain("window.addEventListener('pointercancel', cancelConnection")
    expect(source).toContain('function cancelActiveCanvasPointerGesture(): void')
    expect(source).toContain("window.addEventListener('blur', cancelActiveCanvasPointerGesture)")
    expect(source).toContain("window.removeEventListener('pointerup', stopPointerAction)")
  })

  it('accepts clipboard images and native paste events on the canvas surface', () => {
    expect(source).toContain('importCanvasFiles')
    expect(source).toContain('handlePaste')
    expect(source).toContain("window.addEventListener('paste', handlePaste)")
    expect(source).toContain("window.removeEventListener('paste', handlePaste)")
    expect(source).toContain("if (modifier && event.key.toLowerCase() === 'v') {")
    expect(source).toContain('await addReferenceFiles(files.filter((file) => file.type.startsWith(\'image/\')))')
    expect(source).toContain('item.getAsFile()')
    expect(source).toContain("@copy-image=\"copyImageNode\"")
    // 写剪贴板的实现收敛在 imageClipboard.ts，画布与对话模式共用同一份。
    expect(source).toContain('copyPlaygroundImageToClipboard(source)')
    expect(source).toContain("from '@/features/playground/imageClipboard'")
    expect(nodeSource).toContain("emit('copy-image', node.id, imageIndex)")
  })

  it('adds the reference canvas zoom slider beside the zoom buttons', () => {
    expect(source).toContain('data-canvas-zoom-slider')
    expect(source).toContain('type="range"')
    expect(source).toContain('canvasZoomSlider')
    expect(source).toContain('setZoomPercent')
    expect(source).toContain('min="20" max="300" step="5"')
  })

  it('exports only the current selection when canvas nodes are selected', () => {
    expect(source).toContain('buildCanvasExportState')
    expect(source).toContain('selectedConnectionId.value')
    expect(source).toContain('projects: [selectedProject]')
    expect(source).toContain('selectedNodeIds.value.size')
  })

  it('records canvas background and viewport changes in undoable checkpoints', () => {
    expect(source).toContain('updateCanvasBackgroundMode')
    expect(source).toContain('updateCanvasViewport')
    expect(source).toContain('continuous: true')
    expect(source).toContain('VIEWPORT_CHECKPOINT_IDLE_MS')
    expect(source).toContain('canvasStore.checkpoint()')
  })

  it('matches upstream panning feel with a movement threshold and animation-frame coalescing', () => {
    expect(source).toContain('moved: boolean')
    expect(source).toContain('startedOnBackground: boolean')
    expect(source).toContain('let panningFrame: number | null = null')
    expect(source).toContain('let pendingPanPointer: { clientX: number; clientY: number } | null = null')
    expect(source).toContain('function applyPendingPan')
    expect(source).toContain('function queuePan')
    expect(source).toContain('function cancelPendingPan')
    expect(source).toContain('Math.hypot(deltaX, deltaY) < 3')
    expect(source).toContain('if (!panning.value.moved) {')
    expect(source).toContain('panning.value.moved = true')
    expect(source).toContain('if (panning.value) applyPendingPan()')
    expect(source).toContain('cancelPendingPan()')
    expect(source).toContain('window.requestAnimationFrame')
    expect(source).toContain('window.cancelAnimationFrame')
  })

  it('supports upstream-style node titles and an interactive minimap', () => {
    expect(nodeSource).toContain('data-canvas-node-title')
    expect(nodeSource).toContain('update-title')
    expect(nodeSource).toContain('startTitleEditing')
    expect(nodeSource).toContain('finishTitleEditing')
    expect(source).toContain('@update-title="updateNodeTitle"')
    expect(source).toContain('function updateNodeTitle')
    expect(minimap).toContain('data-canvas-minimap')
    expect(minimap).toContain('@pointerdown.stop.prevent="startMapDrag"')
    expect(minimap).toContain('@pointermove.stop="moveMapDrag"')
    expect(minimap).toContain("'update-viewport'")
    expect(source).toContain('@update-viewport="updateCanvasViewportFromMiniMap"')
  })

  it('renders assistant references as actionable chips and gives config nodes chat/media modes', () => {
    expect(assistant).toContain('CanvasMentionContent')
    expect(assistant).toContain("@activate")
    expect(mentionContent).toContain('data-canvas-mention-content')
    expect(mentionContent).toContain('data-canvas-mention-node-id')
    expect(nodeSource).toContain('data-canvas-config-mode')
    expect(nodeSource).toContain("'generate-config'")
    expect(nodeSource).toContain("'mode' | 'model' | 'count'")
    expect(source).toContain('@generate-config="generateConfigNode"')
    expect(source).toContain('function generateConfigNode')
    expect(source).toContain('resolveCanvasNodeMentions(message.content, project)')
  })

  it('exposes node-level image settings and passes background through generation', () => {
    expect(source).toContain("t('playground.canvasBackground')")
    expect(source).toContain("updateNodeConfig(selectedNode.id, 'background'")
    expect(source).toContain("background: node.config?.background ?? 'auto'")
    expect(source).toContain("resolveCanvasImageSize(node)")
    expect(source).toContain("customWidth")
    expect(source).toContain("generateImageChildVariant")
    expect(nodeSource).toContain("update-image-prompt")
    expect(configSettings).toContain("canvasBackgroundTransparent")
    expect(source).toContain("...(/^gpt-image-/i.test(model) ? {} : { response_format: 'b64_json' })")
  })

  it('exposes the upstream video aspect-ratio control in config and built-in panels', () => {
    expect(nodeSource).toContain('canvasVideoAspectRatio')
    expect(nodeSource).toContain("'aspectRatio'")
    expect(nodeSource).toContain("builtinOption('aspectRatio', '16:9')")
    expect(source).toContain("aspect_ratio: node.config?.aspectRatio ?? '16:9'")
    expect(source).toContain("aspect_ratio: options?.aspectRatio || '16:9'")
    expect(source).toContain("aspectRatio: typeof composerOptions.aspectRatio === 'string' ? composerOptions.aspectRatio : '16:9'")
    expect(source).toContain('data-canvas-video-settings-popover')
    expect(source).toContain(':aspect-ratio="selectedNode.config?.aspectRatio ?? \'16:9\'"')
  })

  it('exposes upstream audio voice, format, speed, and instruction settings', () => {
    expect(configSettings).toContain("canvasAudioVoice")
    expect(configSettings).toContain("canvasAudioFormat")
    expect(configSettings).toContain("canvasAudioSpeed")
    expect(configSettings).toContain("canvasAudioInstructions")
    expect(configSettings).toContain("const audioVoices = ['alloy'")
    expect(configSettings).toContain("const audioFormats = ['mp3', 'wav', 'opus', 'aac', 'flac', 'pcm']")
    expect(nodeSource).toContain(":audio-voice=\"node.config?.audioVoice ?? 'alloy'\"")
    expect(nodeSource).toContain("'audioVoice' | 'audioFormat' | 'audioSpeed' | 'audioInstructions'")
    expect(source).toContain("audioVoice: 'alloy'")
    expect(source).toContain("voice: audioVoice")
    expect(source).toContain("response_format: audioFormat")
    expect(source).toContain("speed: audioSpeed")
    expect(source).toContain("instructions: audioInstructions")
    expect(source).toContain("key === 'audioSpeed'")
    expect(source).toContain('data-canvas-audio-settings-popover')
    expect(source).toContain(':audio-voice="selectedNode.config?.audioVoice ?? \'alloy\'"')
  })

  it('keeps chat reasoning controls and a separate config input composer', () => {
    expect(configSettings).toContain('canvasReasoningEffort')
    expect(configSettings).toContain("value=\"high\"")
    expect(nodeSource).toContain('data-canvas-config-compose-toggle')
    expect(nodeSource).toContain('data-canvas-config-composer')
    expect(nodeSource).toContain('canvasInputsLabel')
    expect(nodeSource).toContain("'reasoningEffort'")
    expect(source).toContain("reasoning_effort: node.config.reasoningEffort")
    expect(source).toContain("reasoningEffort: 'medium'")
  })

  it('passes connected canvas resources into chat and inherited text into media generation', () => {
    expect(source).toContain('const upstreamAttachments = await resolveReferenceAttachments(node)')
    expect(source).toContain('const imageAttachments = [...mentionAttachments')
    expect(source).toContain('function composeCanvasPrompt')
    expect(source).toContain('const prompt = composeCanvasPrompt(node, project)')
    expect(source).toContain('input: prompt')
    expect(source).toContain('canvasNodeHasPromptInput')
    expect(source).toContain('if (node.prompt.trim()) return true')
    expect(nodeSource).toContain('const canGenerateMedia = computed')
    expect(nodeSource).toContain(':disabled="node.status !== \'generating\' && !canGenerateMedia"')
    expect(source).toContain('const referenceImages = await resolveReferenceAttachments(node)')
    expect(source).toContain('reference_images: referenceImages.slice(0, 7).map')
    expect(source).toContain("if (upstream?.type === 'image')")
  })

  it('creates separate result nodes when regenerating populated video or audio nodes', () => {
    expect(source).toContain("const isEmptyVideoNode = !node.videoUrl && !node.videoCacheKey")
    expect(source).toContain("const targetId = retryTargetId || (isEmptyVideoNode ? id : `video-result-${id}-")
    expect(source).toContain("kind: 'result'")
    expect(source).toContain("sourceNodeId: id")
    expect(source).toContain("canvasStore.addConnection({ id: `connection-${id}-${targetId}`, from: id, to: targetId, kind: 'result' })")
    expect(source).toContain("const isEmptyAudioNode = !node.audioUrl && !node.audioCacheKey")
    expect(source).toContain('function retryCanvasGeneration(id: string)')
    expect(source).toContain('void generateVideoNode(source.id, node.kind === \'result\' ? node.id : undefined)')
    expect(source).toContain('void generateAudioNode(source.id, node.kind === \'result\' ? node.id : undefined)')
    expect(nodeSource).toContain("emit('retry-generation', node.id)")
    expect(source).toContain("const targetId = retryTargetId || (isEmptyAudioNode ? id : `audio-result-${id}-")
    expect(source).toContain("const cacheKey = userId.value ? createCanvasMediaCacheKey(userId.value, keyId, targetId)")
    expect(source).toContain('const audioControllers = new Map<string, AbortController>()')
    expect(source).toContain('function cancelAudioNode(id: string)')
    expect(source).toContain('@cancel-audio="cancelAudioNode"')
    expect(nodeSource).toContain("'cancel-audio': [id: string]")
  })

  it('allows generated video and audio results to be downloaded without exposing non-image reference actions', () => {
    expect(source).toContain('selectedNode.kind === \'result\' && (selectedNode.videoUrl || selectedNode.audioUrl)')
    expect(source).toContain('if ((!node?.imageUrl && !node?.videoUrl && !node?.audioUrl) || downloadingNodeId.value) return')
    expect(source).toContain('const videoUrl = node.videoUrl')
    expect(source).toContain('if (videoUrl) {')
    expect(source).toContain('link.download = `sub2api-canvas-${id}.mp4`')
    expect(nodeSource).toContain("node.kind === 'result' && (node.imageUrl || node.videoUrl || node.audioUrl)")
    expect(nodeSource).toContain("(node.type === 'video' && node.kind === 'result' && node.videoUrl)")
    expect(nodeSource).toContain("node.type === 'audio' && node.audioUrl")
    expect(nodeSource).toContain("emit('save-asset', node.id)")
    expect(nodeSource).toContain("emit('transcribe-audio', node.id)")
    expect(nodeSource).toContain('v-if="node.type === \'image\'" type="button" class="btn btn-ghost btn-icon')
    expect(contextMenu).toContain('props.node.imageUrl || props.node.videoUrl || props.node.audioUrl')
    expect(contextMenu).toContain("props.node.kind === 'result' && props.node.type === 'image'")
    expect(contextMenu).toContain("props.node.type === 'video' && props.node.videoUrl")
    expect(contextMenu).toContain("capture-video-frame")
    expect(source).toContain('@capture-video-frame="captureVideoFrame(contextMenu.nodeId, $event)"')
  })

  it('closes the node creation menu when pointerdown happens outside it', () => {
    expect(source).toContain('handleNodeCreateMenuOutsidePointerDown')
    expect(source).toContain("document.addEventListener('pointerdown', handleNodeCreateMenuOutsidePointerDown, true)")
    expect(source).toContain("target?.closest('[data-canvas-create-menu]')")
    const mountedAt = source.indexOf('onMounted(async () =>')
    const listenerAt = source.indexOf("document.addEventListener('pointerdown', handleNodeCreateMenuOutsidePointerDown, true)", mountedAt)
    const asyncLoadAt = source.indexOf('await loadCanvasPluginRuntimes()', mountedAt)
    expect(mountedAt).toBeGreaterThanOrEqual(0)
    expect(listenerAt).toBeGreaterThan(mountedAt)
    expect(listenerAt).toBeLessThan(asyncLoadAt)
  })

  it('treats space and v as temporary pan overrides instead of persistent tool changes', () => {
    expect(source).toContain('temporaryPanBaseTool')
    expect(source).toContain('activeCanvasTool')
    expect(source).toContain('window.addEventListener(\'blur\', resetTemporaryCanvasTool)')
    expect(source).toContain("if (event.code === 'Space') {")
    expect(source).toContain("if (event.key.toLowerCase() === 'v') {")
    expect(source).toContain("if (event.code === 'Space' || event.code === 'KeyV') {")
    expect(source).toContain('resetTemporaryCanvasTool()')
  })

  it('keeps clipboard shortcuts ahead of the temporary V-to-pan gesture', () => {
    const modifierBlock = source.indexOf("const modifier = event.metaKey || event.ctrlKey")
    const temporaryV = source.indexOf("if (event.key.toLowerCase() === 'v')", modifierBlock)
    const clipboardV = source.indexOf("if (modifier && event.key.toLowerCase() === 'v')", modifierBlock)
    expect(modifierBlock).toBeGreaterThan(-1)
    expect(clipboardV).toBeGreaterThan(modifierBlock)
    expect(temporaryV).toBeGreaterThan(clipboardV)
    expect(source).toContain('// Handle clipboard/history commands before the temporary V-to-pan')
  })

  it('never cancels the native paste event from the Ctrl+V keydown branch', () => {
    // 在 keydown 上 preventDefault 会连浏览器的 paste 事件一起取消，handlePaste 就收不到
    // 剪贴板图片，截图粘贴会退化成「贴出上次复制的节点」。该分支必须保持无副作用。
    const branchStart = source.indexOf("if (modifier && event.key.toLowerCase() === 'v') {")
    expect(branchStart).toBeGreaterThan(-1)
    const branch = source.slice(branchStart, source.indexOf('\n  }', branchStart))
    expect(branch).not.toContain('event.preventDefault()')
    expect(branch).not.toContain('pasteNodes(')
    // 图片粘贴要真正到达 handlePaste，且它只在拿到内容时才接管默认行为。
    expect(source).toContain('if (files.length || text) event.preventDefault()')
  })

  it('imports pasted and dropped files as ordinary generator nodes', () => {
    // 导入的素材不是生成结果。写死 kind: 'result' 会让它不能连线、提示词框被禁用、
    // 没有生成按钮，只是一张贴在画布上的死图。
    const importBlock = source.slice(
      source.indexOf('async function importCanvasFiles'),
      source.indexOf('async function uploadImageToNode'),
    )
    expect(importBlock).toContain('...createCanvasNode(')
    expect(importBlock).not.toContain("kind: 'result'")
    expect(importBlock).not.toContain("status: 'success'")
    expect(importBlock).not.toContain('resultIndex: 0')
    // 手动新建与导入共用同一个构造器，属性不会再各写一份而漂移。
    expect(source).toContain('const node = createCanvasNode(type, x, y)')
    expect(source).toContain("kind: 'generator'")
  })

  it('still accepts pasted images while a text input or the mention editor has focus', () => {
    // CanvasMentionEditor 是 contenteditable，早期守卫会在它有焦点时整个跳过粘贴，
    // 截图就被它吞掉。文本仍走浏览器默认行为，但文件必须由画布接管。
    expect(source).toContain("const editable = Boolean(target?.closest('input,textarea,select,[contenteditable=\"true\"]'))")
    expect(source).toContain('if (editable && !files.length) return')
  })

  it('reuses config outputs and suppresses duplicate connection edges while generating', () => {
    expect(source).toContain('const existingOutput = project.nodes.find((node) => node.sourceNodeId === id')
    expect(source).toContain('existingOutput?.status === \'generating\'')
    expect(source).toContain('configBusyNodeIds')
    expect(source).toContain('if (!project.connections.some((connection) => connection.from === id && connection.to === outputId))')
    expect(source).toContain("if (activeProject.value?.connections.some((connection) => connection.from === from && connection.to === to))")
  })

  it('keeps canvas connection handles quiet until a node is active', () => {
    expect(nodeSource).toContain("nodeHovered || selected || connecting || connectionTarget ? 'opacity-100' : 'pointer-events-none opacity-0'")
    expect(nodeSource).toContain('transition-opacity')
  })

  it('keeps connection gestures directional and hides config output handles', () => {
    expect(nodeSource).toContain("emit('connect-start', node.id, 'target')")
    expect(nodeSource).toContain("emit('connect-start', node.id, 'source')")
    expect(nodeSource).toContain("node.type !== 'config'")
    expect(source).toContain("const connectingHandleType = ref<'source' | 'target'>('source')")
    expect(source).toContain("const from = handleType === 'source' ? sourceId : id")
    expect(source).toContain("const to = handleType === 'source' ? id : sourceId")
    expect(source).toContain("connectingHandleType.value = handleType")
    expect(source).not.toContain("tool.value = 'connect'\n  window.addEventListener('pointermove', handlePointerMove)")
  })

  it('snaps connection drags to valid nearby nodes and highlights the target', () => {
    expect(source).toContain('const connectionTargetNodeId = ref<string | null>(null)')
    expect(source).toContain(':connection-target="connectionTargetNodeId === node.id"')
    expect(source).toContain(':connecting="connectingFrom === node.id"')
    expect(source).toContain('function connectionDropTargetAtPointer(clientX: number, clientY: number)')
    expect(source).toContain('const hitsHandle = dx * dx + dy * dy <= handleRadius * handleRadius')
    expect(source).toContain('const hitsExpanded = worldX >= node.x - padding')
    expect(source).toContain('connectionTargetNodeId.value = connectionDropTargetAtPointer(event.clientX, event.clientY).nodeId')
    expect(source).toContain('if (dropTarget.isNearNode)')
    expect(nodeSource).toContain('connectionTarget ?')
    expect(nodeSource).toContain('|| connecting || connectionTarget')
  })

  it('supports an upstream-style context menu for deleting connections', () => {
    expect(source).toContain('@context-menu="openConnectionContextMenu"')
    expect(source).toContain('const connectionContextMenu = ref<{ x: number; y: number; connectionId: string } | null>(null)')
    expect(source).toContain('function openConnectionContextMenu(id: string, event: MouseEvent)')
    expect(source).toContain('function deleteConnectionFromContextMenu()')
    expect(source).toContain('<CanvasConnectionContextMenu v-if="connectionContextMenu"')
    expect(connectionMenu).toContain("t('playground.canvasDeleteConnection')")
    expect(connectionMenu).toContain("onMounted(() => window.addEventListener('pointerdown', dismissOnOutsidePointer))")
    expect(connectionMenu).toContain('data-canvas-connection-menu')
    expect(readFileSync(resolve(__dirname, '../canvas/CanvasConnections.vue'), 'utf8')).toContain("@contextmenu.stop.prevent=\"emit('context-menu', connection.id, $event)\"")
  })

  it('picks the batch image count from a dropdown so it commits on selection', () => {
    // 数字输入框可以在打字过程中停在半成品状态（比如刚删掉数字还没打新的），批量提示词
    // 框却已经据此增减——下拉菜单选完即确认，不会有那种中间态。
    expect(source).toContain('<select id="canvas-node-image-count"')
    expect(source).not.toContain('<input id="canvas-node-image-count"')
    expect(source).toContain(':value="String(selectedNode.imageCount ?? 4)"')
    expect(source).toContain('<option v-for="count in 10" :key="count" :value="String(count)">{{ count }}</option>')
    expect(source).toContain('@change="updateNodeImageCount(selectedNode.id, Number(($event.target as HTMLSelectElement).value))"')
  })

  it('lets a batch generation authorize a distinct prompt per planned image slot', () => {
    // 面板只在 imageCount > 1 时露出，count=1 没有「批量」这回事，不需要多此一举。
    expect(source).toContain('v-if="(selectedNode.imageCount ?? 4) > 1"')
    expect(source).toContain('data-canvas-batch-prompts')
    expect(source).toContain(':value="selectedNode.imagePrompts?.[slotIndex - 1] ?? \'\'"')
    expect(source).toContain('updateNodeImagePrompt(selectedNode.id, slotIndex - 1,')
    expect(source).toContain('function updateNodeImagePrompt(id: string, index: number, value: string)')
    // 解析函数是生成逻辑与面板共用的唯一真源：面板留空 => 这里回落共享 prompt。
    expect(source).toContain('function resolveCanvasImageSlotPrompt(node: CanvasNodeData, index: number): string {')
    expect(source).toContain('return node.imagePrompts?.[index]?.trim() || node.prompt')
    // runGeneration 必须按 slot 各自解析 prompt 后再各自建 request，而不是循环外建一份
    // 共享 request.body 复用 N 次——那样批量生成的 N 张会退化回同一段提示词。
    const requestLoop = source.slice(source.indexOf('for (let requestIndex = 0;'), source.indexOf('const successfulVariants ='))
    expect(requestLoop).toContain('const slotPrompt = resolveCanvasImageSlotPrompt(node, slotStart)')
    expect(requestLoop).toContain('prompt: slotPrompt,')
    expect(requestLoop).toContain('prompt: result.revised_prompt?.trim() || slotPrompt,')
    expect(requestLoop).not.toContain('prompt: node.prompt,')
  })

  it('shows connected references inline with add, preview, focus, and disconnect actions', () => {
    expect(nodeSource).toContain('CanvasNodeReferenceBar')
    expect(source).toContain(':incoming-references="canvasReferenceItemsByNodeId[node.id] ?? []"')
    expect(source).toContain('@start-reference-selection="startPluginReferenceSelection(node.id)"')
    expect(source).toContain('@disconnect-reference="disconnectPluginReference"')
    expect(source).toContain('@focus-reference="focusCanvasNode"')
    expect(source).toContain('const canvasReferenceItemsByNodeId = computed')
    expect(source).toContain('buildCanvasReferenceItems(project, target.id)')
    expect(referenceBar).toContain('data-canvas-node-reference-bar')
    expect(referenceBar).toContain("emit('disconnect-reference', reference.sourceNodeId)")
    expect(referenceBar).toContain("emit('focus-reference', reference.nodeId)")
    expect(referenceBar).toContain("emit('start-selection')")
  })

  it('lets empty or populated media nodes accept direct uploads like the reference toolbar', () => {
    expect(nodeSource).toContain('data-canvas-no-zoom @change="handleMediaUpload"')
    expect(nodeSource).toContain('const canUploadMedia = computed')
    expect(nodeSource).toContain("(props.node.type !== 'image' || props.node.kind !== 'result')")
    expect(nodeSource).toContain('const canReplaceMedia = computed')
    expect(nodeSource).toContain("emit('upload-image', props.node.id, file)")
    expect(nodeSource).toContain("emit('upload-video', props.node.id, file)")
    expect(nodeSource).toContain("emit('upload-audio', props.node.id, file)")
    expect(nodeSource).toContain("playground.canvasUploadImage")
    expect(nodeSource).toContain("playground.canvasUploadVideo")
    expect(nodeSource).toContain("playground.canvasReplaceAudio")
    expect(source).toContain('@upload-image="uploadImageToNode"')
    expect(source).toContain('@upload-video="uploadVideoToNode"')
    expect(source).toContain('@upload-audio="uploadAudioToNode"')
    expect(source).toContain('async function uploadImageToNode(id: string, file: File)')
    expect(source).toContain('createPlaygroundImageCacheKey(owner, keyId')
    expect(source).toContain("async function uploadMediaToNode(id: string, file: File, type: 'video' | 'audio')")
    expect(source).not.toContain("node.type !== type || node.kind === 'result'")
    expect(source).toContain('createCanvasMediaCacheKey(owner, keyId, `${node.id}-${type}-upload-${now}`)')
    expect(source).toContain("t('playground.canvasMediaUploadFailed')")
  })

  it('keeps the mention menu anchored to the caret and inside the viewport', () => {
    expect(mentionEditor).toContain('<Teleport to="body">')
    expect(mentionEditor).toContain('data-canvas-mention-menu')
    expect(mentionEditor).toContain(':style="mentionMenuStyle"')
    expect(mentionEditor).toContain('function updateMentionMenuPosition')
    expect(mentionEditor).toContain('const showAbove = rect.bottom + gap + menuHeight > window.innerHeight - boundary')
    expect(mentionEditor).toContain("window.addEventListener('scroll', updateMentionMenuPosition, true)")
    expect(mentionEditor).not.toContain('absolute left-0 top-full')
  })

  it('allows image hover tools to be customized and persisted like upstream', () => {
    expect(nodeSource).toContain('data-canvas-image-toolbar-settings')
    expect(nodeSource).toContain('imageToolbarToolDefinitions')
    expect(nodeSource).toContain('restoreImageToolbarSettings')
    expect(nodeSource).toContain("localStorage.setItem('canvas-image-toolbar-tools-v1'")
    expect(nodeSource).toContain('resetImageToolbarDraft')
    expect(nodeSource).toContain("openImageToolbarSettings")
    expect(nodeSource).toContain("imageToolbarVisible('crop')")
    expect(nodeSource).toContain("imageToolbarVisible('saveAsset')")
  })

  it('uses the actual minimap viewport instead of hard-coded outer dimensions', () => {
    expect(minimap).toContain('const mapDimensions = computed')
    expect(minimap).toContain('mapDimensions.value.width')
    expect(minimap).toContain('mapDimensions.value.height')
  })

  it('matches upstream image resize behavior with a persistent free-resize toggle', () => {
    expect(nodeSource).toContain("toggle-free-resize")
    expect(nodeSource).toContain("node.freeResize ? 'lockOpen' : 'lock'")
    expect(nodeSource).toContain("node.freeResize ? 'object-fill' : 'object-contain'")
    expect(source).toContain('@toggle-free-resize="toggleFreeResize"')
    expect(source).toContain('function toggleFreeResize')
    expect(source).toContain('freeResize: !node.freeResize')
    expect(source).toContain('keepRatio: boolean; ratio: number')
    expect(source).toContain("const keepRatio = node.type === 'video' || (node.type === 'image' && !node.freeResize)")
    expect(source).toContain('if (resizing.value.keepRatio)')
  })

  it('reveals media actions on hover like the upstream node hover toolbar', () => {
    expect(nodeSource).toContain('(selected || nodeHovered) && node.type === \'image\'')
    expect(nodeSource).toContain('(selected || nodeHovered) && node.type === \'video\'')
    expect(nodeSource).toContain('data-canvas-hover-toolbar')
  })

  it('only expands the reference images bar on selection, not on hover', () => {
    // 悬停就展开参考图缩略图会让鼠标扫过画布时节点不断长高/收缩，体验很糟；
    // 改成只在点击选中后展开。媒体节点右上角的小工具栏（复制/下载）仍然是悬停展开，
    // 那类是「贴边浮出」不占用节点高度，跟这里的「撑高节点」是两回事，不能混着改。
    expect(nodeSource).toContain("node.kind !== 'result' && node.type !== 'group' && selected && !(pluginNode && pluginPanelOpen)")
    expect(nodeSource).not.toContain("node.kind !== 'result' && node.type !== 'group' && (selected || nodeHovered)")
  })

  it('exposes upstream-style node details and a redacted JSON inspector', () => {
    expect(nodeSource).toContain("emit('show-info', node.id)")
    expect(source).toContain('@show-info="openCanvasNodeInfo"')
    expect(source).toContain('const canvasInfoNode = computed')
    expect(nodeInfo).toContain('data-canvas-node-info')
    expect(nodeInfo).toContain("view === 'json'")
    expect(nodeInfo).toContain("'[cached image]'")
    expect(nodeInfo).toContain('canvasNodeInfoDetails')
  })
})
