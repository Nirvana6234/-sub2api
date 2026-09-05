<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-7xl px-4 py-6 md:px-6 lg:px-8">
      <header class="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2">
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('playground.galleryTitle') }}</h1>
            <span v-if="images.length" class="rounded border border-gray-200 px-2 py-0.5 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-dark-300">{{ images.length }}</span>
          </div>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('playground.galleryDescription') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button v-if="images.length" type="button" class="btn btn-ghost h-9 gap-1.5 px-3 text-sm text-red-600 dark:text-red-400" @click="clearGallery">
            <Icon name="trash" size="sm" />
            {{ t('playground.galleryClear') }}
          </button>
          <nav class="flex items-center overflow-x-auto rounded-md bg-gray-100 p-1 text-xs dark:bg-dark-800" :aria-label="t('playground.workspaceLabel')">
            <RouterLink to="/playground/chat" class="rounded px-2.5 py-1.5 font-medium text-gray-500 transition-colors hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.chatWorkspace') }}</RouterLink>
            <RouterLink to="/playground/images" class="rounded px-2.5 py-1.5 font-medium text-gray-500 transition-colors hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.imageWorkspace') }}</RouterLink>
            <RouterLink to="/playground/images?view=canvas" class="rounded px-2.5 py-1.5 font-medium text-gray-500 transition-colors hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.canvasWorkspace') }}</RouterLink>
            <RouterLink to="/playground/gallery" class="rounded bg-white px-2.5 py-1.5 font-medium text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300">{{ t('playground.galleryWorkspace') }}</RouterLink>
          </nav>
        </div>
      </header>

      <div v-if="images.length" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        <article v-for="(image, index) in images" :key="image.key" class="group overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <button type="button" class="relative block aspect-square w-full overflow-hidden bg-gray-100 text-left dark:bg-dark-800" @click="selectedIndex = index">
            <img :src="image.url" :alt="image.prompt || t('playground.generatedImage')" class="h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.02]" loading="lazy">
            <span class="absolute inset-x-0 bottom-0 flex items-center justify-between bg-gray-950/70 px-3 py-2 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
              <span>{{ formatDate(image.createdAt) }}</span>
              <span>{{ image.size || image.blob.type || 'image' }}</span>
            </span>
          </button>
          <div class="space-y-2 p-3">
            <p class="line-clamp-2 min-h-10 text-sm leading-5 text-gray-800 dark:text-gray-100">{{ image.prompt || t('playground.generatedImage') }}</p>
            <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
              <span class="min-w-0 flex-1 truncate">{{ image.model }}</span>
              <span v-if="image.quality" class="shrink-0 rounded border border-gray-200 px-1.5 py-0.5 dark:border-dark-700">{{ image.quality }}</span>
              <span v-if="image.sourceImageCount" class="shrink-0 rounded border border-gray-200 px-1.5 py-0.5 dark:border-dark-700">{{ t('playground.galleryReferences', { count: image.sourceImageCount }) }}</span>
            </div>
            <div class="flex items-center justify-end gap-1 border-t border-gray-100 pt-2 dark:border-dark-800">
              <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.downloadImage')" @click="downloadImage(image)">
                <Icon name="download" size="sm" />
                <span class="sr-only">{{ t('playground.downloadImage') }}</span>
              </button>
              <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-red-600 dark:text-red-400" :title="t('common.delete')" @click="deleteImage(image)">
                <Icon name="trash" size="sm" />
                <span class="sr-only">{{ t('common.delete') }}</span>
              </button>
            </div>
          </div>
        </article>
      </div>
      <div v-else class="flex min-h-64 items-center justify-center border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-700 dark:text-dark-400">
        {{ t('playground.galleryEmpty') }}
      </div>
    </main>

    <div v-if="selectedImage" class="fixed inset-0 z-50 flex items-center justify-center bg-gray-950/85 p-3 md:p-6" role="dialog" aria-modal="true" :aria-label="t('playground.galleryPreview')" @click.self="selectedIndex = null">
      <button type="button" class="absolute right-3 top-3 z-10 flex h-10 w-10 items-center justify-center rounded-md bg-gray-950/70 text-white hover:bg-gray-900" :title="t('common.close')" @click="selectedIndex = null">
        <Icon name="x" size="md" />
        <span class="sr-only">{{ t('common.close') }}</span>
      </button>
      <button v-if="images.length > 1" type="button" class="absolute left-3 top-1/2 z-10 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-md bg-gray-950/70 text-white hover:bg-gray-900" :title="t('playground.galleryPrevious')" @click="stepPreview(-1)">
        <Icon name="chevronLeft" size="md" />
        <span class="sr-only">{{ t('playground.galleryPrevious') }}</span>
      </button>
      <button v-if="images.length > 1" type="button" class="absolute right-3 top-1/2 z-10 flex h-11 w-11 -translate-y-1/2 items-center justify-center rounded-md bg-gray-950/70 text-white hover:bg-gray-900" :title="t('playground.galleryNext')" @click="stepPreview(1)">
        <Icon name="chevronRight" size="md" />
        <span class="sr-only">{{ t('playground.galleryNext') }}</span>
      </button>

      <div class="flex max-h-full w-full max-w-6xl flex-col overflow-hidden rounded-lg bg-white shadow-2xl dark:bg-dark-900 lg:flex-row">
        <div class="flex min-h-0 flex-1 items-center justify-center bg-gray-100 p-2 dark:bg-dark-950">
          <img :src="selectedImage.url" :alt="selectedImage.prompt || t('playground.generatedImage')" class="max-h-[70vh] max-w-full object-contain lg:max-h-[88vh]">
        </div>
        <aside class="min-h-0 w-full shrink-0 overflow-y-auto border-t border-gray-200 p-4 dark:border-dark-700 lg:w-80 lg:border-l lg:border-t-0">
          <p class="break-words whitespace-pre-wrap text-sm leading-6 text-gray-800 dark:text-gray-100">{{ selectedImage.prompt || t('playground.generatedImage') }}</p>
          <dl class="mt-5 space-y-3 text-xs">
            <div class="flex justify-between gap-4"><dt class="text-gray-500 dark:text-dark-400">{{ t('playground.modelLabel') }}</dt><dd class="min-w-0 truncate text-right text-gray-800 dark:text-gray-100">{{ selectedImage.model }}</dd></div>
            <div class="flex justify-between gap-4"><dt class="text-gray-500 dark:text-dark-400">{{ t('playground.galleryCreatedAt') }}</dt><dd class="text-right text-gray-800 dark:text-gray-100">{{ formatDate(selectedImage.createdAt) }}</dd></div>
            <div v-if="selectedImage.size" class="flex justify-between gap-4"><dt class="text-gray-500 dark:text-dark-400">{{ t('playground.imageSize') }}</dt><dd class="text-right text-gray-800 dark:text-gray-100">{{ selectedImage.size }}</dd></div>
            <div v-if="selectedImage.quality" class="flex justify-between gap-4"><dt class="text-gray-500 dark:text-dark-400">{{ t('playground.imageQuality') }}</dt><dd class="text-right text-gray-800 dark:text-gray-100">{{ selectedImage.quality }}</dd></div>
            <div v-if="selectedImage.outputFormat" class="flex justify-between gap-4"><dt class="text-gray-500 dark:text-dark-400">{{ t('playground.imageOutputFormat') }}</dt><dd class="text-right uppercase text-gray-800 dark:text-gray-100">{{ selectedImage.outputFormat }}</dd></div>
          </dl>
          <div class="mt-6 flex gap-2">
            <button type="button" class="btn btn-primary flex-1 gap-1.5" @click="downloadImage(selectedImage)">
              <Icon name="download" size="sm" />
              {{ t('playground.downloadImage') }}
            </button>
            <button type="button" class="btn btn-ghost btn-icon h-10 w-10 p-0 text-red-600 dark:text-red-400" :title="t('common.delete')" @click="deleteImage(selectedImage)">
              <Icon name="trash" size="sm" />
              <span class="sr-only">{{ t('common.delete') }}</span>
            </button>
          </div>
        </aside>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { Icon } from '@/components/icons'
import { useAppStore, useAuthStore } from '@/stores'
import { downloadPlaygroundImage } from '@/features/playground/imageDownload'
import { listPlaygroundGalleryImages, readCachedPlaygroundImageBlob, removeCachedPlaygroundImages, type PlaygroundGalleryImage } from '@/features/playground/imageCache'

type GalleryImage = PlaygroundGalleryImage & { url: string }

const { t, locale } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const images = ref<GalleryImage[]>([])
const selectedIndex = ref<number | null>(null)
const selectedImage = computed(() => selectedIndex.value === null ? null : images.value[selectedIndex.value] ?? null)
let galleryMounted = false

function revokeImage(image: GalleryImage): void {
  URL.revokeObjectURL(image.url)
}

function formatDate(value: number): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(value)
}

function imageExtension(image: GalleryImage): string {
  if (image.outputFormat) return image.outputFormat === 'jpeg' ? 'jpg' : image.outputFormat
  const mimeExtension = image.blob.type.split('/')[1]?.toLowerCase()
  return mimeExtension === 'jpeg' ? 'jpg' : mimeExtension || 'png'
}

async function downloadImage(image: GalleryImage): Promise<void> {
  try {
    const blob = await readCachedPlaygroundImageBlob(image.key)
    if (!blob) throw new Error(t('playground.imageDownloadMissing'))
    await downloadPlaygroundImage(blob, `playground-${image.createdAt}.${imageExtension(image)}`)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.imageDownloadFailed'))
  }
}

async function deleteImage(image: GalleryImage): Promise<void> {
  if (!window.confirm(t('playground.galleryDeleteConfirm'))) return
  try {
    await removeCachedPlaygroundImages([image.key])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.galleryDeleteFailed'))
    return
  }
  const index = images.value.findIndex((item) => item.key === image.key)
  if (index < 0) return
  revokeImage(images.value[index])
  images.value.splice(index, 1)
  if (selectedIndex.value !== null) {
    selectedIndex.value = images.value.length ? Math.min(selectedIndex.value, images.value.length - 1) : null
  }
}

async function clearGallery(): Promise<void> {
  if (!window.confirm(t('playground.galleryClearConfirm', { count: images.value.length }))) return
  try {
    await removeCachedPlaygroundImages(images.value.map((image) => image.key))
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.galleryDeleteFailed'))
    return
  }
  images.value.forEach(revokeImage)
  images.value = []
  selectedIndex.value = null
}

function stepPreview(offset: number): void {
  if (selectedIndex.value === null || images.value.length < 2) return
  selectedIndex.value = (selectedIndex.value + offset + images.value.length) % images.value.length
}

onMounted(async () => {
  galleryMounted = true
  const userId = authStore.user?.id
  if (typeof userId === 'number' && userId > 0) {
    const loadedImages = await listPlaygroundGalleryImages(userId).catch(() => [])
    if (!galleryMounted) {
      loadedImages.forEach(revokeImage)
      return
    }
    images.value = loadedImages
  }
})

onBeforeUnmount(() => {
  galleryMounted = false
  images.value.forEach(revokeImage)
})
</script>
