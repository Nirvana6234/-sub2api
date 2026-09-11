<script setup lang="ts">
import { computed } from 'vue'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    modelValue: string
    type?: string
    placeholder?: string
    autocomplete?: string
  }>(),
  {
    type: 'text',
  },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const value = computed({
  get: () => props.modelValue,
  set: (nextValue: string) => emit('update:modelValue', nextValue),
})
</script>

<template>
  <input
    v-model="value"
    :type="type"
    :placeholder="placeholder"
    :autocomplete="autocomplete"
    :class="cn('h-11 w-full rounded-lg border border-border/70 bg-surface px-4 text-sm text-foreground outline-none transition-[color,background-color,border-color,box-shadow] placeholder:text-muted-foreground focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-primary/30 disabled:cursor-not-allowed disabled:opacity-60')"
  />
</template>
