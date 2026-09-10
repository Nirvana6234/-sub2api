import { defineConfig } from 'vitest/config'
import { resolve } from 'node:path'

export default defineConfig({
  resolve: {
    alias: {
      '@': resolve(__dirname, '../../..'),
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js',
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/features/playground/__tests__/setup.playground.ts'],
    include: [
      'src/features/playground/__tests__/*.spec.ts',
      'src/router/__tests__/feature-access.spec.ts',
    ],
  },
})
