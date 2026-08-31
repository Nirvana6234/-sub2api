<template>
  <AppLayout>
    <PlaygroundCanvasView v-if="mode === 'image' && isCanvasView" :key="canvasViewKey" embedded />
    <PlaygroundConsole v-else :key="mode" :mode="mode" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlaygroundConsole from '@/features/playground/PlaygroundConsole.vue'
import PlaygroundCanvasView from './PlaygroundCanvasView.vue'

defineProps<{
  mode: 'chat' | 'image'
}>()
const route = useRoute()
const isCanvasView = computed(() => route.query.view === 'canvas')
const canvasViewKey = computed(() => `canvas-${String(route.query.view ?? '')}`)
</script>
