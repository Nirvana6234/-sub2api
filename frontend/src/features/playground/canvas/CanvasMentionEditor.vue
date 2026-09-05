<template>
  <div class="relative w-full" data-canvas-mention-wrapper data-canvas-no-zoom @pointerdown.stop>
    <div
      ref="editorRef"
      class="thin-scrollbar min-h-36 max-h-72 w-full overflow-y-auto overscroll-contain whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 p-3 text-sm leading-6 text-gray-800 outline-none focus-within:border-teal-400 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100"
      :class="{ 'opacity-60': disabled }"
      :contenteditable="!disabled"
      role="textbox"
      :aria-label="ariaLabel"
      :data-placeholder="placeholder"
      data-canvas-mention-editor
      @input="handleInput"
      @keydown="handleKeydown"
      @keyup="syncMention"
      @click="syncMention"
      @blur="handleBlur"
      @compositionstart="composing = true"
      @compositionend="composing = false; handleInput()"
    />
    <Teleport to="body">
      <div
        v-if="mention && candidates.length"
        class="fixed z-[120] max-h-56 w-72 overflow-y-auto rounded-xl border border-gray-200 bg-white p-1 shadow-2xl backdrop-blur-md dark:border-dark-600 dark:bg-dark-900"
        :style="mentionMenuStyle"
        data-canvas-mention-menu
        @pointerdown.stop
        @mousedown.stop
        @click.stop
      >
        <button
          v-for="(reference, index) in candidates"
          :key="reference.id"
          type="button"
          class="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left text-xs transition"
          :class="index === activeIndex ? 'bg-teal-50 text-teal-800 dark:bg-teal-900/40 dark:text-teal-100' : 'text-gray-700 hover:bg-gray-50 dark:text-dark-200 dark:hover:bg-dark-800'"
          @pointerdown.prevent.stop="insertReference(reference)"
        >
          <img v-if="reference.previewUrl && reference.kind === 'image'" :src="reference.previewUrl" alt="" class="h-9 w-9 shrink-0 rounded-md object-cover">
          <video v-else-if="reference.previewUrl && reference.kind === 'video'" :src="reference.previewUrl" class="h-9 w-9 shrink-0 rounded-md bg-black object-cover" muted preload="metadata"></video>
          <span v-else class="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-gray-100 text-[10px] font-semibold uppercase text-teal-700 dark:bg-dark-800 dark:text-teal-200">{{ reference.kind === 'group' ? 'G' : reference.kind.slice(0, 1) }}</span>
          <span class="min-w-0 flex-1">
            <span class="block truncate font-medium">{{ reference.label }}</span>
            <span class="block truncate text-[10px] opacity-65">{{ reference.text || reference.title }}</span>
          </span>
        </button>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CanvasMentionReference } from './canvasReferences'

const props = withDefaults(defineProps<{
  modelValue: string
  references?: CanvasMentionReference[]
  placeholder?: string
  ariaLabel?: string
  disabled?: boolean
  onSubmit?: () => void
}>(), {
  references: () => [],
  placeholder: '',
  ariaLabel: 'Text',
  disabled: false,
  onSubmit: undefined,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const editorRef = ref<HTMLDivElement | null>(null)
const mention = ref<{ query: string } | null>(null)
const activeIndex = ref(0)
const composing = ref(false)
const mentionMenuStyle = ref<Record<string, string>>({ left: '8px', top: '8px' })
const candidates = computed(() => {
  if (!mention.value) return []
  const query = mention.value.query.trim().toLowerCase()
  if (!query) return props.references
  return props.references.filter((item) => `${item.label} ${item.title} ${item.kind} ${item.text || ''}`.toLowerCase().includes(query))
})

function tokenFor(nodeId: string): string {
  return `@[node:${nodeId}]`
}

function renderValue(): void {
  const editor = editorRef.value
  if (!editor || document.activeElement === editor) return
  editor.replaceChildren(...parseValue(props.modelValue))
}

function parseValue(value: string): Node[] {
  const nodes: Node[] = []
  const pattern = /@\[node:([^\]]+)\]/g
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = pattern.exec(value))) {
    if (match.index > cursor) nodes.push(document.createTextNode(value.slice(cursor, match.index)))
    const reference = props.references.find((item) => item.nodeId === match?.[1])
    nodes.push(reference ? createChip(reference) : document.createTextNode(match[0]))
    cursor = match.index + match[0].length
  }
  if (cursor < value.length) nodes.push(document.createTextNode(value.slice(cursor)))
  if (!nodes.length && value) nodes.push(document.createTextNode(value))
  return nodes
}

function createChip(reference: CanvasMentionReference): HTMLElement {
  const chip = document.createElement('span')
  chip.contentEditable = 'false'
  chip.dataset.canvasReferenceNodeId = reference.nodeId
  chip.className = 'mx-0.5 inline-flex max-w-48 items-center gap-1 rounded-md bg-teal-100 px-1.5 py-0.5 align-middle text-xs font-medium text-teal-800 ring-1 ring-teal-200 dark:bg-teal-900/40 dark:text-teal-100 dark:ring-teal-700'
  chip.title = reference.text || reference.title
  if (reference.previewUrl && reference.kind === 'image') {
    const image = document.createElement('img')
    image.src = reference.previewUrl
    image.alt = reference.label
    image.className = 'h-5 w-5 rounded object-cover'
    chip.appendChild(image)
  }
  const label = document.createElement('span')
  label.className = 'truncate'
  label.textContent = reference.label
  chip.appendChild(label)
  return chip
}

function serialize(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) return node.textContent || ''
  if (!(node instanceof HTMLElement)) return ''
  const nodeId = node.dataset.canvasReferenceNodeId
  if (nodeId) return tokenFor(nodeId)
  if (node.tagName === 'BR') return '\n'
  const content = Array.from(node.childNodes).map(serialize).join('')
  return node.tagName === 'DIV' || node.tagName === 'P' ? `${content}\n` : content
}

function serializedValue(): string {
  const editor = editorRef.value
  return editor ? Array.from(editor.childNodes).map(serialize).join('').replace(/\uFEFF/g, '') : props.modelValue
}

function textBeforeCaret(): string {
  const selection = window.getSelection()
  if (!selection?.rangeCount) return ''
  const range = selection.getRangeAt(0).cloneRange()
  const editor = editorRef.value
  if (!editor || !editor.contains(range.startContainer)) return ''
  range.selectNodeContents(editor)
  range.setEnd(selection.getRangeAt(0).startContainer, selection.getRangeAt(0).startOffset)
  return range.cloneContents().textContent || ''
}

function syncMention(): void {
  if (props.disabled) {
    mention.value = null
    return
  }
  const match = /(^|[\s\p{P}\p{S}])@([^\s@]*)$/u.exec(textBeforeCaret())
  if (!match || !props.references.length) {
    mention.value = null
    activeIndex.value = 0
    return
  }
  mention.value = { query: match[2] || '' }
  activeIndex.value = 0
  updateMentionMenuPosition()
}

function updateMentionMenuPosition(): void {
  const editor = editorRef.value
  if (!editor || typeof window === 'undefined') return
  const selection = window.getSelection()
  let caretRect: DOMRect | null = null
  if (selection?.rangeCount && editor.contains(selection.anchorNode)) {
    const range = selection.getRangeAt(0).cloneRange()
    range.collapse(true)
    caretRect = range.getBoundingClientRect()
  }
  const editorRect = editor.getBoundingClientRect()
  const rect = caretRect && (caretRect.width || caretRect.height) ? caretRect : editorRect
  const menuWidth = 288
  const menuHeight = 224
  const gap = 6
  const boundary = 8
  const left = Math.min(Math.max(rect.left, boundary), Math.max(boundary, window.innerWidth - menuWidth - boundary))
  const showAbove = rect.bottom + gap + menuHeight > window.innerHeight - boundary && rect.top - gap - menuHeight >= boundary
  const top = showAbove
    ? Math.max(boundary, rect.top - gap - menuHeight)
    : Math.min(Math.max(boundary, rect.bottom + gap), Math.max(boundary, window.innerHeight - menuHeight - boundary))
  mentionMenuStyle.value = { left: `${left}px`, top: `${top}px` }
}

function removeActiveMention(): void {
  const selection = window.getSelection()
  if (!selection?.rangeCount || !selection.isCollapsed) return
  const range = selection.getRangeAt(0)
  const text = textBeforeCaret()
  const match = /(^|[\s\p{P}\p{S}])@([^\s@]*)$/u.exec(text)
  if (!match) return
  const length = (match[2] || '').length + 1
  const start = Math.max(0, range.startOffset - length)
  if (range.startContainer.nodeType === Node.TEXT_NODE) {
    range.setStart(range.startContainer, start)
    range.deleteContents()
  }
}

function insertReference(reference: CanvasMentionReference): void {
  const editor = editorRef.value
  const selection = window.getSelection()
  if (!editor || !selection?.rangeCount) return
  const range = selection.getRangeAt(0)
  removeActiveMention()
  const nextSelection = window.getSelection()
  const insertionRange = nextSelection?.rangeCount ? nextSelection.getRangeAt(0) : range
  const chip = createChip(reference)
  const space = document.createTextNode(' ')
  insertionRange.insertNode(space)
  insertionRange.insertNode(chip)
  insertionRange.setStartAfter(space)
  insertionRange.collapse(true)
  nextSelection?.removeAllRanges()
  nextSelection?.addRange(insertionRange)
  mention.value = null
  emit('update:modelValue', serializedValue())
}

function adjacentChip(key: string): HTMLElement | null {
  const selection = window.getSelection()
  if (!selection?.rangeCount || !selection.isCollapsed) return null
  const range = selection.getRangeAt(0)
  const previous = key === 'Backspace'
  const container = range.startContainer
  const offset = range.startOffset
  if (container.nodeType === Node.TEXT_NODE) {
    const text = container.textContent || ''
    if ((previous && offset > 0) || (!previous && offset < text.length)) return null
    let sibling: Node | null = previous ? container.previousSibling : container.nextSibling
    while (sibling && sibling.nodeType === Node.TEXT_NODE && !(sibling.textContent || '').trim()) sibling = previous ? sibling.previousSibling : sibling.nextSibling
    return sibling instanceof HTMLElement && sibling.dataset.canvasReferenceNodeId ? sibling : null
  }
  const children = Array.from(container.childNodes)
  const candidate = children[previous ? offset - 1 : offset]
  return candidate instanceof HTMLElement && candidate.dataset.canvasReferenceNodeId ? candidate : null
}

function handleInput(): void {
  if (composing.value) return
  emit('update:modelValue', serializedValue())
  syncMention()
}

function handleKeydown(event: KeyboardEvent): void {
  event.stopPropagation()
  if (mention.value && candidates.value.length) {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const delta = event.key === 'ArrowDown' ? 1 : -1
      activeIndex.value = (activeIndex.value + delta + candidates.value.length) % candidates.value.length
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      insertReference(candidates.value[Math.min(activeIndex.value, candidates.value.length - 1)])
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      mention.value = null
      return
    }
  }
  if ((event.key === 'Backspace' || event.key === 'Delete') && !mention.value) {
    const chip = adjacentChip(event.key)
    if (chip) {
      event.preventDefault()
      chip.remove()
      emit('update:modelValue', serializedValue())
      return
    }
  }
  if (event.key === 'Enter' && !event.shiftKey && props.onSubmit) {
    event.preventDefault()
    props.onSubmit()
    return
  }
  nextTick(syncMention)
}

function handleBlur(): void {
  window.setTimeout(() => { mention.value = null }, 120)
}

function focusEditor(): void {
  editorRef.value?.focus({ preventScroll: true })
  const selection = window.getSelection()
  if (!editorRef.value || !selection) return
  const range = document.createRange()
  range.selectNodeContents(editorRef.value)
  range.collapse(false)
  selection.removeAllRanges()
  selection.addRange(range)
}

watch(() => props.modelValue, renderValue)
watch(() => props.references, renderValue, { deep: true })
onMounted(() => {
  renderValue()
  window.addEventListener('resize', updateMentionMenuPosition)
  window.addEventListener('scroll', updateMentionMenuPosition, true)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', updateMentionMenuPosition)
  window.removeEventListener('scroll', updateMentionMenuPosition, true)
})
defineExpose({ focus: focusEditor })
</script>

<style scoped>
[data-canvas-mention-editor]:empty::before {
  content: attr(data-placeholder);
  color: rgb(156 163 175);
  pointer-events: none;
}
</style>
