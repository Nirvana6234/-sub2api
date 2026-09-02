<template>
  <span class="whitespace-pre-wrap break-words" data-canvas-mention-content>
    <template v-for="(part, index) in parts" :key="`${part.nodeId ?? 'text'}-${index}`">
      <button
        v-if="part.reference"
        type="button"
        class="mx-0.5 inline-flex max-w-48 items-center gap-1 rounded-md bg-teal-100 px-1.5 py-0.5 align-middle text-xs font-medium text-teal-800 ring-1 ring-teal-200 transition hover:bg-teal-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:bg-teal-900/50 dark:text-teal-100 dark:ring-teal-700 dark:hover:bg-teal-800/70"
        :title="part.reference.text || part.reference.title"
        :data-canvas-mention-node-id="part.reference.nodeId"
        @click.stop="emit('activate', part.reference.nodeId)"
      >
        <img v-if="part.reference.previewUrl && part.reference.kind === 'image'" :src="part.reference.previewUrl" alt="" class="h-5 w-5 rounded object-cover">
        <span v-else class="grid h-5 w-5 shrink-0 place-items-center rounded bg-teal-200/70 text-[9px] uppercase text-teal-800 dark:bg-teal-800/70 dark:text-teal-100">{{ part.reference.kind === 'group' ? 'G' : part.reference.kind.slice(0, 1) }}</span>
        <span class="truncate">{{ part.reference.label }}</span>
      </button>
      <span v-else>{{ part.text }}</span>
    </template>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { CanvasMentionReference } from './canvasReferences'

const props = defineProps<{
  content: string
  references?: CanvasMentionReference[]
}>()

const emit = defineEmits<{
  activate: [nodeId: string]
}>()

const parts = computed(() => {
  const references = new Map((props.references ?? []).map((reference) => [reference.nodeId, reference]))
  const result: Array<{ text?: string; nodeId?: string; reference?: CanvasMentionReference }> = []
  const pattern = /@\[node:([^\]]+)\]/g
  let cursor = 0
  let match: RegExpExecArray | null
  while ((match = pattern.exec(props.content))) {
    if (match.index > cursor) result.push({ text: props.content.slice(cursor, match.index) })
    const reference = references.get(match[1])
    if (reference) result.push({ nodeId: reference.nodeId, reference })
    else result.push({ text: match[0] })
    cursor = match.index + match[0].length
  }
  if (cursor < props.content.length) result.push({ text: props.content.slice(cursor) })
  return result.length ? result : [{ text: '' }]
})
</script>
