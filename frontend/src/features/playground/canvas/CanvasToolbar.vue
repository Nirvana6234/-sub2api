<template>
  <div class="canvas-toolbar pointer-events-none absolute bottom-5 left-1/2 z-50 flex h-14 max-w-[calc(100%-2rem)] -translate-x-1/2 items-center gap-1 overflow-x-auto rounded-xl border border-gray-200/90 bg-white/95 px-2 py-0 shadow-[0_18px_45px_rgba(15,23,42,.16)] backdrop-blur-xl dark:border-dark-600/90 dark:bg-dark-900/95 dark:shadow-black/30" data-canvas-no-zoom>
    <button type="button" class="btn btn-icon h-8 w-8 p-0" :class="tool === 'select' ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200' : 'btn-ghost'" :title="t('playground.canvasSelectTool')" @click="emit('update:tool', 'select')">
      <Icon name="cursor" size="sm" />
      <span class="sr-only">{{ t('playground.canvasSelectTool') }}</span>
    </button>
    <button type="button" class="btn btn-icon h-8 w-8 p-0" :class="tool === 'pan' ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200' : 'btn-ghost'" :title="t('playground.canvasPanTool')" @click="emit('update:tool', 'pan')">
      <Icon name="move" size="sm" />
      <span class="sr-only">{{ t('playground.canvasPanTool') }}</span>
    </button>
    <button type="button" class="btn btn-icon h-8 w-8 p-0" :class="tool === 'connect' ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200' : 'btn-ghost'" :title="t('playground.canvasConnect')" @click="emit('update:tool', 'connect')">
      <Icon name="link" size="sm" />
      <span class="sr-only">{{ t('playground.canvasConnect') }}</span>
    </button>
    <span class="mx-1 h-5 w-px bg-gray-200 dark:bg-dark-600"></span>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAddNode')" @click="emit('add-node', 'image')">
      <Icon name="plus" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAddNode') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAddText')" @click="emit('add-node', 'text')">
      <Icon name="document" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAddText') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAddVideo')" @click="emit('add-node', 'video')">
      <Icon name="play" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAddVideo') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAddGroup')" @click="emit('add-node', 'group')">
      <Icon name="grid" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAddGroup') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAddConfig')" @click="emit('add-node', 'config')">
      <Icon name="cog" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAddConfig') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasAssets')" @click="emit('assets')">
      <Icon name="inbox" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAssets') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-teal-700 dark:text-teal-300" :title="t('playground.canvasAssistantTitle')" @click="emit('assistant')">
      <Icon name="chatBubble" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAssistantTitle') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-teal-700 dark:text-teal-300" :title="t('playground.canvasAgentTitle')" @click="emit('agent')">
      <Icon name="sparkles" size="sm" />
      <span class="sr-only">{{ t('playground.canvasAgentTitle') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasPluginsTitle')" @click="emit('plugins')">
      <Icon name="cube" size="sm" />
      <span class="sr-only">{{ t('playground.canvasPluginsTitle') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasPromptLibrary')" @click="emit('prompts')">
      <Icon name="lightbulb" size="sm" />
      <span class="sr-only">{{ t('playground.canvasPromptLibrary') }}</span>
    </button>
    <button v-if="selectedCount" type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-red-500" :title="t('playground.canvasClearSelection')" @click="emit('clear-selection')">
      <Icon name="trash" size="sm" />
      <span class="sr-only">{{ t('playground.canvasClearSelection') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-red-500" :disabled="nodeCount === 0" :title="t('playground.canvasClearCanvas')" @click="emit('clear-canvas')">
      <Icon name="xCircle" size="sm" />
      <span class="sr-only">{{ t('playground.canvasClearCanvas') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasFit')" @click="emit('fit')">
      <Icon name="arrowsUpDown" size="sm" />
      <span class="sr-only">{{ t('playground.canvasFit') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-teal-700 dark:text-teal-300" :disabled="nodeCount === 0" :title="t('playground.canvasArrange')" @click="emit('arrange')">
      <Icon name="sort" size="sm" />
      <span class="sr-only">{{ t('playground.canvasArrange') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasResetView')" @click="emit('reset-view')">
      <Icon name="refresh" size="sm" />
      <span class="sr-only">{{ t('playground.canvasResetView') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasImport')" @click="emit('import')">
      <Icon name="upload" size="sm" />
      <span class="sr-only">{{ t('playground.canvasImport') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasExport')" @click="emit('export')">
      <Icon name="download" size="sm" />
      <span class="sr-only">{{ t('playground.canvasExport') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.canvasWebdavTitle')" @click="emit('sync')">
      <Icon name="cloud" size="sm" />
      <span class="sr-only">{{ t('playground.canvasWebdavTitle') }}</span>
    </button>
    <span class="mx-1 h-5 w-px bg-gray-200 dark:bg-dark-600"></span>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :disabled="!canUndo" :title="t('playground.canvasUndo')" @click="emit('undo')">
      <Icon name="undo" size="sm" />
      <span class="sr-only">{{ t('playground.canvasUndo') }}</span>
    </button>
    <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :disabled="!canRedo" :title="t('playground.canvasRedo')" @click="emit('redo')">
      <Icon name="redo" size="sm" />
      <span class="sr-only">{{ t('playground.canvasRedo') }}</span>
    </button>
    <span class="mx-1 hidden h-5 w-px bg-gray-200 dark:bg-dark-600 sm:block"></span>
    <button v-for="mode in modes" :key="mode.value" type="button" class="btn btn-ghost h-8 px-2 text-[11px]" :class="backgroundMode === mode.value ? 'text-teal-700 dark:text-teal-200' : 'text-gray-500'" :title="mode.label" @click="emit('update:background', mode.value)">
      {{ mode.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasBackgroundMode, CanvasTool } from './types'

defineProps<{
  tool: CanvasTool
  backgroundMode: CanvasBackgroundMode
  canUndo: boolean
  canRedo: boolean
  selectedCount: number
  nodeCount: number
}>()

const emit = defineEmits<{
  'update:tool': [value: CanvasTool]
  'update:background': [value: CanvasBackgroundMode]
  'add-node': [type: 'image' | 'text' | 'video' | 'config' | 'group']
  fit: []
  arrange: []
  'reset-view': []
  import: []
  export: []
  sync: []
  undo: []
  redo: []
  assets: []
  assistant: []
  agent: []
  plugins: []
  prompts: []
  'clear-selection': []
  'clear-canvas': []
}>()

const { t } = useI18n()
const modes = computed(() => [
  { value: 'dots' as const, label: t('playground.canvasDots') },
  { value: 'lines' as const, label: t('playground.canvasLines') },
  { value: 'blank' as const, label: t('playground.canvasBlank') },
])
</script>

<style scoped>
.canvas-toolbar > button {
  pointer-events: auto;
  flex: 0 0 auto;
}

 .canvas-toolbar > span {
  flex: 0 0 auto;
}

 .canvas-toolbar {
  scrollbar-width: thin;
}
</style>
