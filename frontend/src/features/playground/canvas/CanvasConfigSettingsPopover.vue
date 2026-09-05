<template>
  <span ref="triggerRef" class="inline-flex min-w-0" data-canvas-config-settings>
    <button
      type="button"
      class="btn btn-ghost flex h-8 min-w-0 max-w-full items-center gap-1.5 rounded-md border border-gray-200 bg-gray-50 px-2 text-[11px] text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-100"
      :disabled="disabled"
      :aria-expanded="open"
      :title="t('playground.canvasNodeSettings')"
      @click.stop="toggle"
    >
      <Icon name="cog" size="xs" class="shrink-0" />
      <span class="truncate">{{ summary }}</span>
    </button>
  </span>
  <Teleport to="body">
    <div
      v-if="open"
      ref="panelRef"
      class="fixed z-[1200] w-[min(356px,calc(100vw-24px))] max-h-[min(70vh,520px)] overflow-y-auto rounded-2xl border border-gray-200 bg-white p-4 text-gray-800 shadow-2xl dark:border-dark-600 dark:bg-dark-900 dark:text-gray-100"
      :style="panelStyle"
      data-canvas-config-settings-panel
      data-canvas-no-zoom
      @pointerdown.stop
      @mousedown.stop
      @click.stop
      @wheel.stop
    >
      <div class="mb-3 flex items-center justify-between gap-3">
        <div>
          <div class="text-xs font-semibold">{{ t('playground.canvasNodeSettings') }}</div>
          <div class="mt-0.5 text-[10px] text-gray-400 dark:text-dark-400">{{ modeLabel }}</div>
        </div>
        <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-gray-500" :title="t('common.close')" @click.stop="close">
          <Icon name="x" size="xs" />
          <span class="sr-only">{{ t('common.close') }}</span>
        </button>
      </div>

      <div class="space-y-3">
        <label v-if="mode === 'image' || mode === 'text'" class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
          {{ t(mode === 'text' ? 'playground.canvasTextCount' : 'playground.canvasImageCount') }}
          <input
            class="input mt-1 h-8 w-full text-xs"
            type="number"
            min="1"
            max="10"
            :value="count"
            :disabled="disabled"
            @change="emitUpdate('count', ($event.target as HTMLInputElement).value)"
          >
        </label>

        <template v-if="mode === 'image'">
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasImageSize') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="size" :disabled="disabled" @change="emitUpdate('size', ($event.target as HTMLSelectElement).value)">
              <option value="1024x1024">1024 × 1024</option>
              <option value="1536x1024">1536 × 1024</option>
              <option value="1024x1536">1024 × 1536</option>
              <option value="1440x1080">4:3 · 1440 × 1080</option>
              <option value="1080x1440">3:4 · 1080 × 1440</option>
              <option value="1920x1080">16:9 · 1920 × 1080</option>
              <option value="1080x1920">9:16 · 1080 × 1920</option>
              <option value="custom">自定义尺寸</option>
            </select>
          </label>
          <div v-if="size === 'custom'" class="grid grid-cols-[1fr_auto_1fr] items-end gap-2">
            <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">宽度
              <input class="input mt-1 h-8 w-full text-xs" type="number" min="16" max="3840" step="16" :value="customWidth" :disabled="disabled" @change="emitUpdate('customWidth', ($event.target as HTMLInputElement).value)">
            </label>
            <span class="pb-2 text-gray-400">×</span>
            <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">高度
              <input class="input mt-1 h-8 w-full text-xs" type="number" min="16" max="3840" step="16" :value="customHeight" :disabled="disabled" @change="emitUpdate('customHeight', ($event.target as HTMLInputElement).value)">
            </label>
          </div>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasQuality') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="quality" :disabled="disabled" @change="emitUpdate('quality', ($event.target as HTMLSelectElement).value)">
              <option value="auto">{{ t('playground.canvasQualityAuto') }}</option>
              <option value="high">{{ t('playground.canvasQualityHigh') }}</option>
              <option value="medium">{{ t('playground.canvasQualityMedium') }}</option>
              <option value="low">{{ t('playground.canvasQualityLow') }}</option>
            </select>
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasBackground') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="background" :disabled="disabled" @change="emitUpdate('background', ($event.target as HTMLSelectElement).value)">
              <option value="auto">{{ t('playground.canvasBackgroundAuto') }}</option>
              <option value="transparent">{{ t('playground.canvasBackgroundTransparent') }}</option>
              <option value="opaque">{{ t('playground.canvasBackgroundOpaque') }}</option>
            </select>
          </label>
        </template>

        <template v-else-if="mode === 'video'">
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasVideoResolution') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="resolution" :disabled="disabled" @change="emitUpdate('resolution', ($event.target as HTMLSelectElement).value)">
              <option value="480p">480p</option>
              <option value="720p">720p</option>
              <option value="1080p">1080p</option>
            </select>
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasVideoDuration') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="duration" :disabled="disabled" @change="emitUpdate('duration', ($event.target as HTMLSelectElement).value)">
              <option value="5">5s</option>
              <option value="8">8s</option>
              <option value="10">10s</option>
            </select>
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasVideoAspectRatio') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="aspectRatio" :disabled="disabled" @change="emitUpdate('aspectRatio', ($event.target as HTMLSelectElement).value)">
              <option value="16:9">16:9</option>
              <option value="9:16">9:16</option>
              <option value="1:1">1:1</option>
              <option value="4:3">4:3</option>
              <option value="3:4">3:4</option>
            </select>
          </label>
        </template>

        <template v-else-if="mode === 'audio'">
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasAudioVoice') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="audioVoice" :disabled="disabled" @change="emitUpdate('audioVoice', ($event.target as HTMLSelectElement).value)">
              <option v-for="voice in audioVoices" :key="voice" :value="voice">{{ voice }}</option>
            </select>
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasAudioFormat') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="audioFormat" :disabled="disabled" @change="emitUpdate('audioFormat', ($event.target as HTMLSelectElement).value)">
              <option v-for="format in audioFormats" :key="format" :value="format">{{ format.toUpperCase() }}</option>
            </select>
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasAudioSpeed') }}
            <input
              class="input mt-1 h-8 w-full text-xs"
              type="number"
              min="0.25"
              max="4"
              step="0.05"
              :value="audioSpeed"
              :disabled="disabled"
              @change="emitUpdate('audioSpeed', ($event.target as HTMLInputElement).value)"
            >
          </label>
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasAudioInstructions') }}
            <textarea
              class="textarea mt-1 min-h-20 w-full resize-y text-xs leading-5"
              :value="audioInstructions"
              :placeholder="t('playground.canvasAudioInstructionsPlaceholder')"
              :disabled="disabled"
              maxlength="4000"
              @change="emitUpdate('audioInstructions', ($event.target as HTMLTextAreaElement).value)"
            ></textarea>
          </label>
        </template>

        <template v-else-if="mode === 'text'">
          <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">
            {{ t('playground.canvasReasoningEffort') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="reasoningEffort" :disabled="disabled" @change="emitUpdate('reasoningEffort', ($event.target as HTMLSelectElement).value)">
              <option value="none">{{ t('playground.canvasReasoningNone') }}</option>
              <option value="low">{{ t('playground.canvasReasoningLow') }}</option>
              <option value="medium">{{ t('playground.canvasReasoningMedium') }}</option>
              <option value="high">{{ t('playground.canvasReasoningHigh') }}</option>
            </select>
          </label>
        </template>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

type CanvasConfigMode = 'image' | 'text' | 'video' | 'audio'
type ConfigKey = 'count' | 'size' | 'customWidth' | 'customHeight' | 'quality' | 'background' | 'resolution' | 'duration' | 'aspectRatio' | 'audioVoice' | 'audioFormat' | 'audioSpeed' | 'audioInstructions' | 'reasoningEffort'

const props = withDefaults(defineProps<{
  mode: CanvasConfigMode
  count?: string
  size?: string
  customWidth?: string
  customHeight?: string
  quality?: string
  background?: string
  resolution?: string
  duration?: string
  aspectRatio?: string
  audioVoice?: string
  audioFormat?: string
  audioSpeed?: string
  audioInstructions?: string
  reasoningEffort?: 'none' | 'low' | 'medium' | 'high'
  disabled?: boolean
}>(), {
  count: '1',
  size: '1024x1024',
  customWidth: '1024',
  customHeight: '1024',
  quality: 'auto',
  background: 'auto',
  resolution: '720p',
  duration: '8',
  aspectRatio: '16:9',
  audioVoice: 'alloy',
  audioFormat: 'mp3',
  audioSpeed: '1',
  audioInstructions: '',
  reasoningEffort: 'medium',
  disabled: false,
})

const emit = defineEmits<{
  update: [payload: { key: ConfigKey; value: string }]
}>()

const { t } = useI18n()
const open = ref(false)
const triggerRef = ref<HTMLElement | null>(null)
const panelRef = ref<HTMLElement | null>(null)
const panelStyle = ref<Record<string, string>>({})
const audioVoices = ['alloy', 'ash', 'ballad', 'coral', 'echo', 'fable', 'nova', 'onyx', 'sage', 'shimmer', 'verse', 'marin', 'cedar']
const audioFormats = ['mp3', 'wav', 'opus', 'aac', 'flac', 'pcm']

const modeLabel = computed(() => t(`playground.canvasConfigMode${props.mode.charAt(0).toUpperCase()}${props.mode.slice(1)}`))
const summary = computed(() => {
  if (props.mode === 'image') return `${props.size} · ${props.quality} · ${props.count}`
  if (props.mode === 'video') return `${props.resolution} · ${props.duration}s · ${props.aspectRatio}`
  if (props.mode === 'text') return `${props.reasoningEffort} · ${props.count} ${t('playground.canvasTextCount')}`
  return `${props.audioVoice} · ${props.audioFormat} · ${props.audioSpeed}x`
})

function emitUpdate(key: ConfigKey, value: string): void {
  emit('update', { key, value })
}

function syncPosition(): void {
  const trigger = triggerRef.value
  if (!trigger || typeof window === 'undefined') return
  const rect = trigger.getBoundingClientRect()
  const width = Math.min(356, window.innerWidth - 24)
  const gap = 8
  const left = Math.max(12, Math.min(window.innerWidth - width - 12, rect.right - width))
  const showAbove = rect.bottom + gap + 320 > window.innerHeight - 12 && rect.top - gap - 320 >= 12
  panelStyle.value = showAbove
    ? { left: `${left}px`, bottom: `${Math.max(12, window.innerHeight - rect.top + gap)}px` }
    : { left: `${left}px`, top: `${Math.min(window.innerHeight - 12, rect.bottom + gap)}px` }
}

function closeOnOutsidePointer(event: PointerEvent): void {
  const target = event.target
  if (!(target instanceof Node)) return
  if (triggerRef.value?.contains(target) || panelRef.value?.contains(target)) return
  close()
}

function close(): void {
  open.value = false
}

function toggle(): void {
  if (props.disabled) return
  open.value = !open.value
}

watch(open, (visible) => {
  if (!visible) {
    window.removeEventListener('resize', syncPosition)
    window.removeEventListener('scroll', syncPosition, true)
    window.removeEventListener('pointerdown', closeOnOutsidePointer, true)
    return
  }
  syncPosition()
  window.addEventListener('resize', syncPosition)
  window.addEventListener('scroll', syncPosition, true)
  window.addEventListener('pointerdown', closeOnOutsidePointer, true)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', syncPosition)
  window.removeEventListener('scroll', syncPosition, true)
  window.removeEventListener('pointerdown', closeOnOutsidePointer, true)
})
</script>
