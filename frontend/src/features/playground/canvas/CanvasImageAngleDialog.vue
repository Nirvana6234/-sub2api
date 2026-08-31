<template>
  <div class="fixed inset-0 z-[120] flex items-center justify-center bg-gray-950/70 p-4" data-canvas-no-zoom @pointerdown.self="emit('close')">
    <section class="flex max-h-[calc(100%-2rem)] w-full max-w-4xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasAngleImage')" data-image-angle-dialog>
      <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700"><div><h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasAngleImage') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasAngleDescription') }}</p></div><button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" :disabled="busy" @click="emit('close')"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button></header>
      <div class="grid min-h-0 flex-1 gap-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_260px]"><div class="flex min-h-80 items-center justify-center bg-gray-950 p-6"><img :src="imageUrl" :alt="t('playground.canvasAnglePreview')" class="max-h-[56vh] max-w-full object-contain shadow-xl" draggable="false"></div><aside class="space-y-5 border-t border-gray-200 p-5 dark:border-dark-700 md:border-l md:border-t-0"><label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasAngleHorizontal') }}<input v-model.number="horizontalAngle" type="range" min="-60" max="60" step="1" class="mt-2 w-full accent-teal-600"><span class="text-[11px] text-gray-500">{{ horizontalAngle }}°</span></label><label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasAnglePitch') }}<input v-model.number="pitchAngle" type="range" min="-45" max="45" step="1" class="mt-2 w-full accent-teal-600"><span class="text-[11px] text-gray-500">{{ pitchAngle }}°</span></label><label class="block text-xs font-medium text-gray-700 dark:text-dark-200">{{ t('playground.canvasAngleDistance') }}<input v-model.number="cameraDistance" type="range" min="0" max="10" step="0.5" class="mt-2 w-full accent-teal-600"><span class="text-[11px] text-gray-500">{{ cameraDistance }}</span></label><label class="flex items-center justify-between gap-3 text-xs font-medium text-gray-700 dark:text-dark-200"><span>{{ t('playground.canvasAngleWide') }}</span><input v-model="wideAngle" type="checkbox" class="h-4 w-4 accent-teal-600"></label></aside></div>
      <footer class="flex justify-end gap-2 border-t border-gray-200 px-5 py-3 dark:border-dark-700"><button type="button" class="btn btn-ghost" :disabled="busy" @click="emit('close')">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary gap-1.5" data-image-angle-apply :disabled="busy" @click="apply"><Icon name="eye" size="sm" :class="{ 'animate-pulse': busy }" />{{ busy ? t('playground.canvasImageApplying') : t('playground.canvasAngleApply') }}</button></footer>
    </section>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { CanvasImageAngleParams } from './imageTransform'
defineProps<{ imageUrl: string; busy?: boolean }>()
const emit = defineEmits<{ close: []; apply: [params: CanvasImageAngleParams] }>()
const { t } = useI18n()
const horizontalAngle = ref(0)
const pitchAngle = ref(0)
const cameraDistance = ref(4)
const wideAngle = ref(false)
function apply(): void { emit('apply', { horizontalAngle: horizontalAngle.value, pitchAngle: pitchAngle.value, cameraDistance: cameraDistance.value, wideAngle: wideAngle.value }) }
</script>
