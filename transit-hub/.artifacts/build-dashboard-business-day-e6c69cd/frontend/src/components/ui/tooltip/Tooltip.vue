<script setup lang="ts">
import { ref, useTemplateRef, nextTick } from 'vue'

defineProps<{
  text: string
  wide?: boolean
}>()

const visible = ref(false)
const pos = ref({ x: 0, y: 0 })
const flipped = ref(false)
const triggerRef = useTemplateRef<HTMLElement>('trigger')
const tooltipRef = useTemplateRef<HTMLElement>('tooltip')
let timer: ReturnType<typeof setTimeout> | null = null

const EDGE_PADDING = 8

const updatePosition = async () => {
  await nextTick()
  const trigger = triggerRef.value
  const tip = tooltipRef.value
  if (!trigger || !tip) return
  const rect = trigger.getBoundingClientRect()
  const tipRect = tip.getBoundingClientRect()
  const vw = window.innerWidth

  let x = rect.left + rect.width / 2 - tipRect.width / 2
  if (x < EDGE_PADDING) x = EDGE_PADDING
  if (x + tipRect.width > vw - EDGE_PADDING) x = vw - EDGE_PADDING - tipRect.width

  const above = rect.top - tipRect.height - 8
  if (above < EDGE_PADDING) {
    flipped.value = true
    pos.value = { x, y: rect.bottom + 8 }
  } else {
    flipped.value = false
    pos.value = { x, y: above }
  }
}

const show = () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(async () => {
    visible.value = true
    await updatePosition()
  }, 150)
}

const hide = () => {
  if (timer) clearTimeout(timer)
  timer = null
  visible.value = false
}
</script>

<template>
  <div ref="trigger" class="relative inline-flex" @mouseenter="show" @mouseleave="hide" @focusin="show" @focusout="hide">
    <slot />
    <Teleport to="body">
      <Transition
        enter-active-class="transition duration-150 ease-out"
        enter-from-class="opacity-0 translate-y-1"
        enter-to-class="opacity-100 translate-y-0"
        leave-active-class="transition duration-100 ease-in"
        leave-from-class="opacity-100 translate-y-0"
        leave-to-class="opacity-0 translate-y-1"
      >
        <div
          v-if="visible"
          ref="tooltip"
          role="tooltip"
          :class="[
            'fixed rounded-lg bg-zinc-900 shadow-lg text-xs text-white z-[9999] pointer-events-none',
            wide ? 'w-64 whitespace-normal px-3 py-2 font-normal' : 'whitespace-nowrap px-2.5 py-1.5 font-medium'
          ]"
          :style="{ left: `${pos.x}px`, top: `${pos.y}px` }"
        >
          {{ text }}
          <div
            v-if="flipped"
            class="absolute bottom-full left-1/2 -translate-x-1/2 mb-[-1px] border-4 border-transparent border-b-zinc-900"
          />
          <div
            v-else
            class="absolute top-full left-1/2 -translate-x-1/2 -mt-px border-4 border-transparent border-t-zinc-900"
          />
        </div>
      </Transition>
    </Teleport>
  </div>
</template>
