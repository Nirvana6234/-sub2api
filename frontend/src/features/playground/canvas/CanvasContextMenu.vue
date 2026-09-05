<template>
  <div class="fixed z-[90] w-52 rounded-xl border border-gray-200 bg-white/98 p-1.5 shadow-2xl backdrop-blur dark:border-dark-600 dark:bg-dark-900/98" :style="menuStyle" role="menu" data-canvas-no-zoom @pointerdown.stop>
    <p class="truncate px-2.5 py-1.5 text-[11px] font-medium text-gray-400">{{ nodeLabel }}</p>
    <button v-for="action in actions" :key="action.id" type="button" class="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-xs transition-colors hover:bg-gray-100 dark:hover:bg-dark-800" :class="action.danger ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-dark-200'" role="menuitem" @click="runAction(action.id)">
      <Icon :name="action.icon" size="xs" />
      <span class="flex-1">{{ action.label }}</span>
      <span v-if="action.shortcut" class="text-[10px] text-gray-400">{{ action.shortcut }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasNode } from './types'

const props = defineProps<{ x: number; y: number; node: CanvasNode }>()
const emit = defineEmits<{ duplicate: []; copy: []; front: []; back: []; download: []; reference: []; 'capture-video-frame': [position: 'first' | 'current' | 'last']; delete: [] }>()
const { t } = useI18n()

const menuStyle = computed(() => ({
  left: `${Math.min(props.x, window.innerWidth - 224)}px`,
  top: `${Math.min(props.y, window.innerHeight - 310)}px`,
}))
const nodeLabel = computed(() => props.node.prompt || t(`playground.canvasNodeType.${props.node.type}`))
const actions = computed(() => [
  { id: 'duplicate' as const, icon: 'copy' as const, label: t('playground.canvasDuplicateNode'), shortcut: 'Ctrl+D' },
  { id: 'copy' as const, icon: 'clipboard' as const, label: t('playground.canvasCopyNode'), shortcut: 'Ctrl+C' },
  { id: 'front' as const, icon: 'arrowUp' as const, label: t('playground.canvasBringFront') },
  { id: 'back' as const, icon: 'arrowDown' as const, label: t('playground.canvasSendBack') },
  ...(props.node.type === 'video' && props.node.videoUrl ? [
    { id: 'capture-first' as const, icon: 'chevronLeft' as const, label: t('playground.canvasCaptureFirstFrame') },
    { id: 'capture-current' as const, icon: 'eye' as const, label: t('playground.canvasCaptureCurrentFrame') },
    { id: 'capture-last' as const, icon: 'chevronRight' as const, label: t('playground.canvasCaptureLastFrame') },
  ] : []),
  ...(props.node.imageUrl || props.node.videoUrl || props.node.audioUrl ? [{ id: 'download' as const, icon: 'download' as const, label: t('playground.canvasDownload') }] : []),
  ...(props.node.kind === 'result' && props.node.type === 'image' ? [{ id: 'reference' as const, icon: 'upload' as const, label: t('playground.canvasUseAsReference') }] : []),
  { id: 'delete' as const, icon: 'trash' as const, label: t('playground.canvasDeleteNode'), shortcut: 'Del', danger: true },
])

function runAction(id: (typeof actions.value)[number]['id']): void {
  if (id === 'duplicate') emit('duplicate')
  else if (id === 'copy') emit('copy')
  else if (id === 'front') emit('front')
  else if (id === 'back') emit('back')
  else if (id === 'capture-first') emit('capture-video-frame', 'first')
  else if (id === 'capture-current') emit('capture-video-frame', 'current')
  else if (id === 'capture-last') emit('capture-video-frame', 'last')
  else if (id === 'download') emit('download')
  else if (id === 'reference') emit('reference')
  else emit('delete')
}
</script>
