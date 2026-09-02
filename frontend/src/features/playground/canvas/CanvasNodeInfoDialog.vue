<template>
  <Teleport to="body">
    <div
      v-if="visible && node"
      class="fixed inset-0 z-[120] grid place-items-center bg-gray-950/45 p-4 backdrop-blur-[2px]"
      data-canvas-node-info
      @click.self="emit('close')"
      @keydown.esc="emit('close')"
    >
      <section
        class="flex max-h-[min(720px,calc(100vh-2rem))] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900"
        role="dialog"
        aria-modal="true"
        :aria-label="t('playground.canvasNodeInfo')"
        @pointerdown.stop
      >
        <header class="flex items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div class="min-w-0">
            <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasNodeInfo') }}</p>
            <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400">{{ node.title || fallbackTitle }}</p>
          </div>
          <button type="button" class="btn btn-ghost btn-icon h-8 w-8 shrink-0 p-0" :title="t('common.close')" @click="emit('close')">
            <Icon name="x" size="sm" />
            <span class="sr-only">{{ t('common.close') }}</span>
          </button>
        </header>

        <div class="flex items-center gap-1 border-b border-gray-100 px-5 pt-2 dark:border-dark-700" role="tablist">
          <button type="button" class="border-b-2 px-2 pb-2 text-xs font-medium transition" :class="view === 'info' ? 'border-teal-500 text-teal-700 dark:text-teal-300' : 'border-transparent text-gray-400 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'" role="tab" :aria-selected="view === 'info'" @click="view = 'info'">
            {{ t('playground.canvasNodeInfoDetails') }}
          </button>
          <button type="button" class="border-b-2 px-2 pb-2 text-xs font-medium transition" :class="view === 'json' ? 'border-teal-500 text-teal-700 dark:text-teal-300' : 'border-transparent text-gray-400 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'" role="tab" :aria-selected="view === 'json'" @click="view = 'json'">
            JSON
          </button>
        </div>

        <div class="min-h-0 flex-1 overflow-y-auto p-5">
          <dl v-if="view === 'info'" class="space-y-3 text-xs">
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">ID</dt>
              <dd class="break-all font-mono text-gray-700 dark:text-dark-200">{{ node.id }}</dd>
            </div>
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasNodeInfoName') }}</dt>
              <dd class="break-words text-gray-700 dark:text-dark-200">{{ node.title || t('playground.canvasUntitledNode') }}</dd>
            </div>
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasNodeInfoType') }}</dt>
              <dd class="text-gray-700 dark:text-dark-200">{{ typeLabel }}</dd>
            </div>
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasNodeInfoSize') }}</dt>
              <dd class="tabular-nums text-gray-700 dark:text-dark-200">{{ Math.round(node.width) }} × {{ Math.round(node.height) }}</dd>
            </div>
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasNodeInfoPosition') }}</dt>
              <dd class="tabular-nums text-gray-700 dark:text-dark-200">{{ Math.round(node.x) }}, {{ Math.round(node.y) }}</dd>
            </div>
            <div class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasNodeStatus') }}</dt>
              <dd class="text-gray-700 dark:text-dark-200">{{ node.status }}</dd>
            </div>
            <div v-if="node.prompt.trim()" class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasPrompt') }}</dt>
              <dd class="whitespace-pre-wrap break-words text-gray-700 dark:text-dark-200">{{ node.prompt }}</dd>
            </div>
            <div v-if="imageCount > 1" class="grid grid-cols-[88px_minmax(0,1fr)] gap-4">
              <dt class="text-gray-400 dark:text-dark-500">{{ t('playground.canvasImageCount') }}</dt>
              <dd class="text-gray-700 dark:text-dark-200">{{ imageCount }}</dd>
            </div>
          </dl>
          <pre v-else class="min-h-64 overflow-auto rounded-xl border border-gray-200 bg-gray-50 p-4 text-[11px] leading-5 text-gray-700 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-200">{{ safeJson }}</pre>
        </div>
      </section>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasNode } from './types'

const props = defineProps<{
  node: CanvasNode | null
  visible: boolean
}>()

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const view = ref<'info' | 'json'>('info')

watch(() => [props.node?.id, props.visible], () => {
  if (props.visible) view.value = 'info'
})

const fallbackTitle = computed(() => {
  const node = props.node
  if (!node) return ''
  if (node.kind === 'result') return t('playground.canvasResult', { index: (node.resultIndex ?? 0) + 1 })
  if (node.type === 'group') return t('playground.canvasGroupNode')
  if (node.type === 'config') return t('playground.canvasConfigNode')
  if (node.type === 'image' || node.type === 'video' || node.type === 'audio' || node.type === 'text') return t(`playground.canvasNodeType.${node.type}`)
  return node.pluginTitle || node.type
})

const typeLabel = computed(() => {
  const node = props.node
  if (!node) return ''
  if (node.type === 'group') return t('playground.canvasGroupNode')
  if (node.type === 'config') return t('playground.canvasConfigNode')
  if (node.type === 'image' || node.type === 'video' || node.type === 'audio' || node.type === 'text') return t(`playground.canvasNodeType.${node.type}`)
  return node.pluginTitle || node.type
})

const imageCount = computed(() => {
  const node = props.node
  if (!node || node.type !== 'image') return 0
  return node.imageVariants?.length || node.imageUrls?.length || 0
})

const safeJson = computed(() => {
  if (!props.node) return ''
  return JSON.stringify(props.node, (key, value) => {
    if (key === 'url' && typeof value === 'string' && (value.startsWith('data:image/') || value.startsWith('blob:'))) return '[cached image]'
    if (key === 'videoUrl' || key === 'audioUrl') return typeof value === 'string' && value ? '[cached media]' : value
    return value
  }, 2)
})
</script>
