<template>
  <AppLayout>
    <PlaygroundConsole :key="mode" :mode="mode" />
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PlaygroundConsole from '@/features/playground/PlaygroundConsole.vue'

defineProps<{
  mode: 'chat' | 'image'
}>()
</script>
