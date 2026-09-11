<script setup lang="ts">
import { computed } from 'vue'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex touch-manipulation items-center justify-center gap-2 rounded-lg text-sm font-semibold transition-[color,background-color,border-color,box-shadow,transform] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background active:translate-y-px disabled:pointer-events-none disabled:opacity-50 disabled:transform-none',
  {
    variants: {
      variant: {
        default: 'bg-primary text-primary-foreground hover:bg-primary/90',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-surface-line',
        destructive: 'bg-red-500 text-white hover:bg-red-600 dark:bg-red-600 dark:hover:bg-red-700',
        ghost: 'bg-transparent text-foreground hover:bg-surface-elevated',
      },
      size: {
        default: 'h-10 px-4 py-2',
        sm: 'h-9 px-3',
        lg: 'h-11 px-6',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

const props = withDefaults(
  defineProps<{
    type?: 'button' | 'submit' | 'reset'
    variant?: VariantProps<typeof buttonVariants>['variant']
    size?: VariantProps<typeof buttonVariants>['size']
  }>(),
  {
    type: 'button',
    variant: 'default',
    size: 'default',
  },
)

const classes = computed(() => cn(buttonVariants({ variant: props.variant, size: props.size })))
</script>

<template>
  <button :type="type" :class="classes">
    <slot />
  </button>
</template>
