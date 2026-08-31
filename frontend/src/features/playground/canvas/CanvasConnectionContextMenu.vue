<template>
  <div
    class="fixed z-[90] w-52 rounded-xl border border-gray-200 bg-white/98 p-1.5 shadow-2xl backdrop-blur dark:border-dark-600 dark:bg-dark-900/98"
    :style="menuStyle"
    role="menu"
    data-canvas-connection-menu
    data-canvas-no-zoom
    @pointerdown.stop
  >
    <button
      type="button"
      class="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-xs text-red-600 transition-colors hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30"
      role="menuitem"
      @click="emit('delete')"
    >
      <Icon name="trash" size="xs" />
      <span class="flex-1">{{ t('playground.canvasDeleteConnection') }}</span>
      <span class="text-[10px] text-gray-400">Del</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

const props = defineProps<{ x: number; y: number }>()
const emit = defineEmits<{ delete: []; close: [] }>()
const { t } = useI18n()

const menuStyle = computed(() => ({
  left: `${Math.max(8, Math.min(props.x, window.innerWidth - 224))}px`,
  top: `${Math.max(8, Math.min(props.y, window.innerHeight - 72))}px`,
}))

function dismissOnOutsidePointer(event: PointerEvent): void {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('[data-canvas-connection-menu]')) return
  emit('close')
}

onMounted(() => window.addEventListener('pointerdown', dismissOnOutsidePointer))
onBeforeUnmount(() => window.removeEventListener('pointerdown', dismissOnOutsidePointer))
</script>
