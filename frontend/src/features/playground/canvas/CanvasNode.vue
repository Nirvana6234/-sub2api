<template>
  <article
    class="group absolute rounded-lg border bg-white shadow-lg transition-shadow dark:bg-dark-900"
    :class="[
      selected ? 'z-50 border-teal-500 shadow-teal-500/20 ring-2 ring-teal-500/25' : 'border-gray-200 dark:border-dark-600',
      related && !selected ? 'border-teal-300/80 shadow-teal-500/10 ring-1 ring-teal-400/20 dark:border-teal-700/80' : '',
      focusRelated && !selected ? 'ring-2 ring-teal-400/30 dark:ring-teal-600/30' : '',
      node.type === 'group' ? 'border-dashed bg-teal-50/60 shadow-none dark:bg-teal-950/20' : '',
      isGroupDropTarget ? 'ring-2 ring-dashed ring-teal-500/50' : '',
      node.pluginTransparentBackground ? 'bg-transparent shadow-none dark:bg-transparent' : '',
      node.status === 'generating' ? 'animate-pulse' : '',
      connectionTarget ? 'border-teal-500 shadow-teal-500/30 ring-2 ring-teal-500/35 dark:border-teal-300 dark:ring-teal-300/35' : '',
      referenceSelectionState === 'available' ? 'cursor-pointer' : referenceSelectionState ? 'cursor-not-allowed' : '',
    ]"
    :style="{ left: `${node.x}px`, top: `${node.y}px`, width: `${node.width}px`, minHeight: `${node.height}px` }"
    :data-node-id="node.id"
    @pointerdown.stop="handleNodePointerDown"
    @pointerenter="handleNodePointerEnter"
    @pointerleave="handleNodePointerLeave"
    @dblclick.stop="handleNodeDoubleClick"
    @contextmenu.stop.prevent="emit('context-menu', node.id, $event)"
  >
    <div
      v-if="referenceSelectionState && (referenceSelectionState !== 'available' || nodeHovered)"
      class="pointer-events-none absolute inset-0 z-[60] grid place-items-center rounded-lg"
      :class="referenceSelectionState === 'available' ? 'bg-teal-500/5 ring-2 ring-inset ring-teal-500/80' : 'bg-gray-950/25'"
      aria-hidden="true"
    >
      <span
        v-if="referenceSelectionState !== 'disabled'"
        class="rounded-lg bg-white/95 px-3 py-2 text-xs font-medium text-gray-800 shadow-lg dark:bg-dark-900/95 dark:text-gray-100"
      >
        {{ referenceSelectionState === 'target' ? t('playground.canvasReferenceSelecting') : t('playground.canvasReferenceChoose') }}
      </span>
    </div>
    <div v-if="(selected || nodeHovered) && node.type === 'image' && displayedImageUrl" class="absolute left-1/2 z-50 flex h-9 max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto rounded-md border border-gray-200 bg-white px-1 shadow-lg dark:border-dark-600 dark:bg-dark-800" :class="toolbarPlacementClass" data-canvas-hover-toolbar data-canvas-no-zoom @pointerdown.stop>
      <button v-if="imageToolbarVisible('crop')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasCropImage')" @click.stop="emit('edit-image', node.id, imageIndex)"><Icon name="edit" size="xs" /><span class="sr-only">{{ t('playground.canvasCropImage') }}</span></button>
      <button v-if="imageToolbarVisible('rotateLeft')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasRotateLeft')" @click.stop="emit('transform-image', node.id, imageIndex, 'rotate-left')"><Icon name="rotateLeft" size="xs" /><span class="sr-only">{{ t('playground.canvasRotateLeft') }}</span></button>
      <button v-if="imageToolbarVisible('rotateRight')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasRotateRight')" @click.stop="emit('transform-image', node.id, imageIndex, 'rotate-right')"><Icon name="rotateRight" size="xs" /><span class="sr-only">{{ t('playground.canvasRotateRight') }}</span></button>
      <button v-if="imageToolbarVisible('flipHorizontal')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasFlipHorizontal')" @click.stop="emit('transform-image', node.id, imageIndex, 'flip-horizontal')"><Icon name="flipHorizontal" size="xs" /><span class="sr-only">{{ t('playground.canvasFlipHorizontal') }}</span></button>
      <button v-if="imageToolbarVisible('reversePrompt')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :class="reversePromptBusy ? 'text-teal-600' : ''" :disabled="reversePromptBusy" :title="t('playground.canvasReversePrompt')" @click.stop="emit('reverse-prompt', node.id, imageIndex)"><Icon name="document" size="xs" :class="{ 'animate-pulse': reversePromptBusy }" /><span class="sr-only">{{ t('playground.canvasReversePrompt') }}</span></button>
      <button v-if="imageToolbarVisible('split')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasSplitImage')" @click.stop="emit('split-image', node.id, imageIndex)"><Icon name="grid" size="xs" /><span class="sr-only">{{ t('playground.canvasSplitImage') }}</span></button>
      <button v-if="imageToolbarVisible('upscale')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :class="upscaleBusy ? 'text-teal-600' : ''" :disabled="upscaleBusy" :title="t('playground.canvasUpscaleImage')" @click.stop="emit('upscale-image', node.id, imageIndex)"><Icon name="sparkles" size="xs" :class="{ 'animate-pulse': upscaleBusy }" /><span class="sr-only">{{ t('playground.canvasUpscaleImage') }}</span></button>
      <button v-if="imageToolbarVisible('view')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasViewImage')" @click.stop="emit('view-image', node.id, imageIndex)"><Icon name="eye" size="xs" /><span class="sr-only">{{ t('playground.canvasViewImage') }}</span></button>
      <button v-if="imageToolbarVisible('mask')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasMaskImage')" @click.stop="emit('mask-image', node.id, imageIndex)"><Icon name="edit" size="xs" /><span class="sr-only">{{ t('playground.canvasMaskImage') }}</span></button>
      <button v-if="imageToolbarVisible('angle')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasAngleImage')" @click.stop="emit('angle-image', node.id, imageIndex)"><Icon name="eye" size="xs" /><span class="sr-only">{{ t('playground.canvasAngleImage') }}</span></button>
      <button v-if="imageToolbarVisible('freeResize')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :class="node.freeResize ? 'text-teal-600 dark:text-teal-300' : ''" :title="node.freeResize ? t('playground.canvasLockAspectRatio') : t('playground.canvasFreeResize')" @click.stop="emit('toggle-free-resize', node.id)"><Icon :name="node.freeResize ? 'lockOpen' : 'lock'" size="xs" /><span class="sr-only">{{ node.freeResize ? t('playground.canvasLockAspectRatio') : t('playground.canvasFreeResize') }}</span></button>
      <span v-if="imageToolbarVisible('download') || imageToolbarVisible('saveAsset')" class="mx-0.5 h-5 w-px bg-gray-200 dark:bg-dark-600"></span>
      <button v-if="imageToolbarVisible('download')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasDownload')" @click.stop="emit('download', node.id, imageIndex)"><Icon name="download" size="xs" /><span class="sr-only">{{ t('playground.canvasDownload') }}</span></button>
      <button v-if="imageToolbarVisible('saveAsset')" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasSaveAsset')" @click.stop="emit('save-asset', node.id, imageIndex)"><Icon name="inbox" size="xs" /><span class="sr-only">{{ t('playground.canvasSaveAsset') }}</span></button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-gray-500" :title="t('playground.canvasCustomizeImageToolbar')" @click.stop="openImageToolbarSettings"><Icon name="cog" size="xs" /><span class="sr-only">{{ t('playground.canvasCustomizeImageToolbar') }}</span></button>
    </div>
    <div v-if="(selected || nodeHovered) && node.type === 'video' && node.videoUrl" class="absolute left-1/2 z-50 flex h-9 max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto rounded-md border border-gray-200 bg-white px-1 shadow-lg dark:border-dark-600 dark:bg-dark-800" :class="toolbarPlacementClass" data-canvas-hover-toolbar data-canvas-no-zoom @pointerdown.stop>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasCaptureFirstFrame')" @click.stop="emit('capture-video-frame', node.id, 'first')"><Icon name="chevronLeft" size="xs" /><span class="sr-only">{{ t('playground.canvasCaptureFirstFrame') }}</span></button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasCaptureCurrentFrame')" @click.stop="emit('capture-video-frame', node.id, 'current', videoElement?.currentTime ?? 0)"><Icon name="eye" size="xs" /><span class="sr-only">{{ t('playground.canvasCaptureCurrentFrame') }}</span></button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasCaptureLastFrame')" @click.stop="emit('capture-video-frame', node.id, 'last')"><Icon name="chevronRight" size="xs" /><span class="sr-only">{{ t('playground.canvasCaptureLastFrame') }}</span></button>
    </div>
    <div v-if="(selected || nodeHovered) && remoteToolbarItems.length" class="absolute left-1/2 z-50 flex h-9 max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto rounded-md border border-gray-200 bg-white px-1 shadow-lg dark:border-dark-600 dark:bg-dark-800" :class="toolbarPlacementClass" data-canvas-hover-toolbar data-canvas-no-zoom @pointerdown.stop>
      <button v-for="item in remoteToolbarItems" :key="item.id" type="button" class="btn btn-ghost h-7 gap-1 px-2 text-[11px]" :class="item.danger ? 'text-red-600' : ''" :title="item.title" @click.stop="item.onClick()"><span v-if="item.icon" aria-hidden="true">{{ item.icon }}</span>{{ item.label }}</button>
    </div>
    <template v-if="selected">
      <button v-for="handle in resizeHandles" :key="handle" type="button" class="absolute z-40 h-3 w-3 rounded-sm border border-white bg-teal-500 shadow" :class="resizeHandleClass[handle]" :title="t('playground.canvasResizeNode')" data-canvas-no-zoom @pointerdown.stop="emit('resize-start', node.id, handle, $event)"><span class="sr-only">{{ t('playground.canvasResizeNode') }}</span></button>
    </template>
    <button
      v-if="node.kind !== 'result' && node.type !== 'group'"
      type="button"
      class="absolute -left-2 top-1/2 z-30 h-4 w-4 -translate-y-1/2 rounded-full border-2 border-white bg-teal-500 shadow transition-opacity"
      :class="nodeHovered || selected || connecting || connectionTarget ? 'opacity-100' : 'pointer-events-none opacity-0'"
      :title="t('playground.canvasConnect')"
      data-canvas-no-zoom
      @pointerdown.stop="emit('connect-start', node.id, 'target')"
    >
      <span class="sr-only">{{ t('playground.canvasConnect') }}</span>
    </button>
    <button
      v-if="node.kind !== 'result' && node.type !== 'group' && node.type !== 'config'"
      type="button"
      class="absolute -right-2 top-1/2 z-30 h-4 w-4 -translate-y-1/2 rounded-full border-2 border-white bg-teal-500 shadow transition-opacity"
      :class="nodeHovered || selected || connecting || connectionTarget ? 'opacity-100' : 'pointer-events-none opacity-0'"
      :title="t('playground.canvasConnect')"
      data-canvas-no-zoom
      @pointerdown.stop="emit('connect-start', node.id, 'source')"
    >
      <span class="sr-only">{{ t('playground.canvasConnect') }}</span>
    </button>
    <div class="overflow-hidden rounded-lg">
    <header class="flex h-10 items-center gap-2 border-b border-gray-100 bg-gray-50/90 px-3 dark:border-dark-700 dark:bg-dark-800/90" data-canvas-no-zoom>
      <span class="flex h-6 w-6 items-center justify-center rounded bg-teal-100 text-teal-700 dark:bg-teal-900/50 dark:text-teal-200">
        <Icon :name="node.type === 'group' ? 'grid' : node.type === 'video' ? 'play' : node.type === 'audio' ? 'chatBubble' : node.type === 'text' ? 'document' : node.type === 'config' ? 'cog' : 'sparkles'" size="xs" />
      </span>
      <span v-if="node.kind === 'result'" class="min-w-0 flex-1 truncate text-xs font-medium text-gray-800 dark:text-gray-100">
        {{ t('playground.canvasResult', { index: (node.resultIndex ?? 0) + 1 }) }}
      </span>
      <span v-else-if="node.type === 'group'" class="min-w-0 flex-1 truncate text-xs font-medium text-gray-800 dark:text-gray-100">
        {{ node.prompt || t('playground.canvasGroupNode') }}
      </span>
      <span v-else-if="node.type === 'config'" class="min-w-0 flex-1 truncate text-xs font-medium text-gray-800 dark:text-gray-100">
        {{ t('playground.canvasConfigNode') }}
      </span>
      <span v-else-if="pluginNode" class="min-w-0 flex-1 truncate text-xs font-medium text-gray-800 dark:text-gray-100">{{ node.pluginTitle || node.type }}</span>
      <input
        v-else
        :value="node.prompt"
        class="min-w-0 flex-1 truncate bg-transparent text-xs font-medium text-gray-800 outline-none dark:text-gray-100"
        :aria-label="t('playground.canvasPrompt')"
        :disabled="node.status === 'generating'"
        data-canvas-no-zoom
        @pointerdown.stop="emit('select', node.id, $event)"
        @change="emit('update-prompt', node.id, ($event.target as HTMLInputElement).value)"
      >
      <button v-if="node.kind === 'result' && (node.imageUrl || node.audioUrl)" type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0 text-gray-500" :title="t('playground.canvasDownload')" data-canvas-no-zoom @click.stop="emit('download', node.id)">
        <Icon name="download" size="xs" :class="{ 'animate-pulse': downloading }" />
        <span class="sr-only">{{ t('playground.canvasDownload') }}</span>
      </button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0 text-gray-500" :title="t('playground.canvasNodeInfo')" data-canvas-no-zoom @click.stop="emit('show-info', node.id)">
        <Icon name="infoCircle" size="xs" />
        <span class="sr-only">{{ t('playground.canvasNodeInfo') }}</span>
      </button>
      <button type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0 text-gray-500" :title="t('playground.canvasDeleteNode')" data-canvas-no-zoom @click.stop="emit('delete', node.id)">
        <Icon name="trash" size="xs" />
        <span class="sr-only">{{ t('playground.canvasDeleteNode') }}</span>
      </button>
    </header>

    <CanvasNodeReferenceBar
      v-if="node.kind !== 'result' && node.type !== 'group' && (selected || nodeHovered) && !(pluginNode && pluginPanelOpen)"
      :references="incomingReferences ?? []"
      @start-selection="emit('start-reference-selection')"
      @disconnect-reference="emit('disconnect-reference', node.id, $event)"
      @focus-reference="emit('focus-reference', $event)"
    />

    <div v-if="node.type === 'group'" class="relative flex min-h-40 flex-col gap-3 bg-white p-4 dark:bg-dark-900">
      <div class="flex items-start justify-between gap-3">
        <div class="space-y-1">
          <p class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('playground.canvasGroupNode') }}</p>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasGroupHint') }}</p>
        </div>
        <span class="rounded-full bg-teal-100 px-2.5 py-1 text-[11px] font-medium text-teal-700 dark:bg-teal-900/40 dark:text-teal-200">{{ t('playground.canvasGroupCount', { count: groupChildCount ?? 0 }) }}</span>
      </div>
      <div class="rounded-lg border border-dashed border-teal-200 bg-teal-50/60 p-3 text-xs text-teal-700 dark:border-teal-900/50 dark:bg-teal-950/20 dark:text-teal-200">
        {{ t('playground.canvasGroupHint') }}
      </div>
    </div>
    <div
      v-if="!referenceSelectionState && (selected || nodeHovered || titleEditing)"
      class="absolute left-3 top-[-28px] z-[65] max-w-[calc(100%-1.5rem)]"
      data-canvas-node-title
      @pointerdown.stop
    >
      <input
        v-if="titleEditing"
        ref="titleInputRef"
        v-model="titleDraft"
        type="text"
        maxlength="64"
        class="h-6 max-w-full border-0 border-b border-dashed border-gray-400 bg-transparent px-0 text-left text-xs font-medium text-gray-700 outline-none dark:border-dark-500 dark:text-gray-200"
        :aria-label="t('playground.canvasNodeTitle')"
        @keydown.enter.prevent="finishTitleEditing"
        @keydown.esc.prevent="cancelTitleEditing"
        @blur="finishTitleEditing"
      >
      <button
        v-else
        type="button"
        class="block max-w-full truncate border-b border-dashed border-transparent px-0 py-0.5 text-left text-xs font-medium text-gray-700 opacity-75 transition hover:border-current hover:opacity-100 dark:text-gray-200"
        :title="t('playground.canvasRenameNode')"
        @dblclick.stop="startTitleEditing"
      >
        {{ node.title || fallbackNodeTitle }}
      </button>
    </div>
    <div v-else-if="node.type === 'text'" class="relative bg-white p-3 dark:bg-dark-900" data-canvas-no-zoom>
      <div v-if="selected && node.kind !== 'result'" class="absolute left-1/2 z-50 flex h-9 max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-0.5 overflow-x-auto rounded-md border border-gray-200 bg-white px-1 shadow-lg dark:border-dark-600 dark:bg-dark-800" :class="toolbarPlacementClass" data-canvas-text-toolbar data-canvas-no-zoom @pointerdown.stop>
        <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="isEditingContent ? t('playground.canvasTextPreview') : t('playground.canvasEditText')" @click="isEditingContent ? stopEditingText() : void focusTextEditor()"><Icon :name="isEditingContent ? 'eye' : 'edit'" size="xs" /><span class="sr-only">{{ isEditingContent ? t('playground.canvasTextPreview') : t('playground.canvasEditText') }}</span></button>
        <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasTextSmaller')" @click="adjustTextSize(-1)"><Icon name="minus" size="xs" /><span class="sr-only">{{ t('playground.canvasTextSmaller') }}</span></button>
        <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasTextLarger')" @click="adjustTextSize(1)"><Icon name="plus" size="xs" /><span class="sr-only">{{ t('playground.canvasTextLarger') }}</span></button>
      </div>
      <div v-if="textVariants.length > 1" class="mb-2 flex items-center justify-between gap-2" data-canvas-text-variants>
        <span class="text-[10px] font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">{{ t('playground.canvasTextAlternatives', { count: textVariants.length }) }}</span>
        <button type="button" class="btn btn-ghost h-6 px-2 text-[10px]" data-canvas-text-variants-toggle @click.stop="textVariantsExpanded = !textVariantsExpanded">
          <Icon :name="textVariantsExpanded ? 'chevronUp' : 'chevronDown'" size="xs" />
          {{ textVariantsExpanded ? t('playground.canvasTextCollapseAlternatives') : t('playground.canvasTextExpandAlternatives') }}
        </button>
      </div>
      <div v-if="textVariantsExpanded" class="mb-2 max-h-44 space-y-1.5 overflow-y-auto rounded-lg border border-gray-100 bg-gray-50/70 p-1.5 dark:border-dark-700 dark:bg-dark-950/50" data-canvas-text-variant-list>
        <button v-for="(variant, index) in textVariants" :key="variant.id" type="button" class="block w-full rounded-md border px-2.5 py-2 text-left text-[11px] leading-5 transition" :class="variant.id === primaryTextId ? 'border-teal-300 bg-teal-50 text-teal-900 dark:border-teal-700 dark:bg-teal-950/40 dark:text-teal-100' : 'border-transparent text-gray-600 hover:border-gray-200 hover:bg-white dark:text-dark-300 dark:hover:border-dark-600 dark:hover:bg-dark-900'" :disabled="variant.status === 'generating'" @click.stop="emit('select-text-variant', node.id, variant.id)">
          <span class="mb-1 flex items-center justify-between gap-2 text-[10px] font-semibold uppercase tracking-wide text-gray-400 dark:text-dark-500">
            <span>{{ t('playground.canvasTextAlternative', { index: index + 1 }) }}</span>
            <span v-if="variant.status === 'generating'" class="text-teal-600 dark:text-teal-300">{{ t('playground.canvasGenerating') }}</span>
            <span v-else-if="variant.status === 'error'" class="text-red-600 dark:text-red-300">{{ variant.errorMessage || t('playground.canvasGenerationFailed') }}</span>
          </span>
          <span class="line-clamp-4 whitespace-pre-wrap">{{ variant.content || t('playground.canvasTextPlaceholder') }}</span>
        </button>
      </div>
      <button v-if="!isEditingContent" type="button" class="block w-full rounded-lg border border-dashed border-gray-200 bg-gray-50 p-3 text-left text-gray-800 outline-none transition hover:border-teal-300 hover:bg-teal-50/40 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100 dark:hover:border-teal-700 dark:hover:bg-teal-950/25" :style="{ fontSize: `${node.textFontSize ?? 16}px` }" :aria-label="t('playground.canvasTextContent')" :title="t('playground.canvasTextContent')" data-canvas-text-preview data-canvas-no-zoom @pointerdown.stop="emit('select', node.id, $event)" @dblclick.stop="focusTextEditor">
        <span class="block whitespace-pre-wrap leading-6">{{ displayedTextContent || t('playground.canvasTextPlaceholder') }}</span>
      </button>
      <CanvasMentionEditor
        v-else
        ref="textEditorRef"
        :model-value="displayedTextContent"
        :references="mentionReferences"
        :placeholder="t('playground.canvasTextPlaceholder')"
        :aria-label="t('playground.canvasTextContent')"
        :disabled="node.kind === 'result' || node.status === 'generating'"
        class="min-h-56"
        data-canvas-text-editor
        @pointerdown.stop="emit('select', node.id, $event)"
        @wheel.stop
        @keydown.esc.stop="stopEditingText"
        @update:model-value="emit('update-prompt', node.id, $event)"
      />
    </div>
    <div v-else-if="node.type === 'config'" class="space-y-3 bg-white p-3 dark:bg-dark-900" data-canvas-no-zoom>
      <div class="grid grid-cols-4 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-800" data-canvas-config-mode>
        <button v-for="mode in (['image', 'text', 'video', 'audio'] as const)" :key="mode" type="button" class="rounded-md px-1 py-1.5 text-[10px] font-medium transition" :class="configMode === mode ? 'bg-white text-teal-700 shadow-sm dark:bg-dark-700 dark:text-teal-200' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'" :disabled="node.status === 'generating' || configBusy" @click.stop="emit('update-config', node.id, 'mode', mode)">
          {{ t(`playground.canvasConfigMode${mode.charAt(0).toUpperCase()}${mode.slice(1)}`) }}
        </button>
      </div>
      <div class="grid grid-cols-[minmax(0,1fr)_minmax(0,auto)] items-end gap-2">
        <label class="block min-w-0 text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.modelLabel') }}
            <select class="select mt-1 h-8 w-full text-xs" :value="configModel" :disabled="node.status === 'generating' || configBusy || !configModelOptions.length" @change="emit('update-config', node.id, 'model', ($event.target as HTMLSelectElement).value)">
            <option v-if="!configModelOptions.length" value="">{{ t('playground.noModels') }}</option>
            <option v-for="model in configModelOptions" :key="model.id" :value="model.id">{{ model.id }}</option>
          </select>
        </label>
        <div class="flex min-w-0 items-center justify-end gap-1">
          <button
            type="button"
            class="btn btn-ghost flex h-8 min-w-0 max-w-32 items-center gap-1 rounded-md border border-gray-200 bg-gray-50 px-2 text-[11px] text-gray-700 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-100"
            :class="configComposerOpen ? 'border-teal-300 bg-teal-50 text-teal-700 dark:border-teal-700 dark:bg-teal-900/40 dark:text-teal-200' : ''"
            :disabled="node.status === 'generating' || configBusy"
            :aria-expanded="configComposerOpen"
            :title="t('playground.canvasComposeInputs')"
            data-canvas-config-compose-toggle
            @click.stop="configComposerOpen = !configComposerOpen"
          >
            <Icon name="edit" size="xs" />
            <span class="truncate">{{ t('playground.canvasComposeInputs') }}</span>
          </button>
          <CanvasConfigSettingsPopover
          :mode="configMode"
          :count="node.config?.count ?? '1'"
          :size="node.config?.size ?? '1024x1024'"
          :quality="node.config?.quality ?? 'auto'"
          :background="node.config?.background ?? 'auto'"
          :resolution="node.config?.resolution ?? '720p'"
          :duration="node.config?.duration ?? '8'"
          :aspect-ratio="node.config?.aspectRatio ?? '16:9'"
          :audio-voice="node.config?.audioVoice ?? 'alloy'"
          :audio-format="node.config?.audioFormat ?? 'mp3'"
          :audio-speed="node.config?.audioSpeed ?? '1'"
          :audio-instructions="node.config?.audioInstructions ?? ''"
          :reasoning-effort="node.config?.reasoningEffort ?? 'medium'"
          :disabled="node.status === 'generating' || configBusy"
          @update="emit('update-config', node.id, $event.key, $event.value)"
          />
        </div>
      </div>
      <div v-if="configComposerOpen" class="overflow-hidden rounded-xl border border-teal-200 bg-teal-50/40 dark:border-teal-900/60 dark:bg-teal-950/20" data-canvas-config-composer data-canvas-no-zoom @pointerdown.stop>
        <div class="flex items-center justify-between gap-2 border-b border-teal-100 px-3 py-2 dark:border-teal-900/50">
          <div class="min-w-0">
            <div class="text-[11px] font-semibold text-teal-800 dark:text-teal-100">{{ t('playground.canvasComposeInputs') }}</div>
            <div class="truncate text-[10px] text-teal-700/70 dark:text-teal-200/70">{{ t('playground.canvasComposeInputsHint') }}</div>
          </div>
          <button type="button" class="btn btn-ghost btn-icon h-6 w-6 shrink-0 p-0 text-teal-700 dark:text-teal-200" :title="t('common.close')" @click.stop="configComposerOpen = false">
            <Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span>
          </button>
        </div>
        <div class="space-y-2 p-2.5">
          <div v-if="incomingReferences?.length" class="flex min-w-0 flex-wrap items-center gap-1.5" data-canvas-config-input-summary>
            <span class="shrink-0 text-[10px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasInputsLabel') }}</span>
            <span v-for="reference in incomingReferences" :key="`${reference.sourceNodeId}:${reference.nodeId}`" class="inline-flex max-w-44 items-center gap-1 rounded-full border border-gray-200 bg-white px-2 py-1 text-[10px] text-gray-700 shadow-sm dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200" :title="reference.text || reference.label || reference.title">
              <img v-if="reference.previewUrl && reference.kind === 'image'" :src="reference.previewUrl" alt="" class="h-4 w-4 rounded object-cover">
              <span class="truncate">{{ reference.label || reference.title }}</span>
            </span>
          </div>
          <CanvasMentionEditor
            :model-value="node.prompt"
            :references="mentionReferences"
            :placeholder="t('playground.canvasPrompt')"
            :aria-label="t('playground.canvasPrompt')"
            :disabled="node.status === 'generating' || configBusy"
            class="text-xs"
            @update:model-value="emit('update-prompt', node.id, $event)"
          />
          <button type="button" class="btn btn-ghost h-7 w-full gap-1 border border-dashed border-teal-300 px-2 text-[10px] text-teal-700 dark:border-teal-800 dark:text-teal-200" :disabled="node.status === 'generating' || configBusy" @click.stop="emit('start-reference-selection')">
            <Icon name="plus" size="xs" />{{ t('playground.canvasAddReference') }}
          </button>
        </div>
      </div>
      <template v-else>
        <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasPrompt') }}</label>
        <CanvasMentionEditor
          :model-value="node.prompt"
          :references="mentionReferences"
          :placeholder="t('playground.canvasPrompt')"
          :aria-label="t('playground.canvasPrompt')"
          :disabled="node.status === 'generating' || configBusy"
          class="text-xs"
          @update:model-value="emit('update-prompt', node.id, $event)"
        />
      </template>
      <button type="button" class="btn btn-primary h-8 w-full gap-1 text-xs" :disabled="node.status === 'generating' || configBusy || (!node.prompt.trim() && !(mentionReferences?.length ?? 0) && !configModel)" @click.stop="emit('generate-config', node.id)">
        <Icon :name="node.status === 'generating' || configBusy ? 'x' : 'sparkles'" size="xs" />
        {{ node.status === 'generating' || configBusy ? t('playground.canvasGenerating') : t('playground.generate') }}
      </button>
    </div>
    <div v-else-if="pluginNode" class="relative flex min-h-48 flex-col items-center justify-center gap-2 bg-gray-100 px-5 text-center dark:bg-dark-950" data-canvas-no-zoom>
      <div v-if="remotePluginDefinition" ref="remotePluginMount" class="min-h-44 w-full overflow-hidden" :class="pluginInteractive ? 'pointer-events-auto' : 'pointer-events-none'" :style="{ height: `${remotePluginHeight}px` }" data-canvas-no-zoom @pointerdown="pluginInteractive ? $event.stopPropagation() : undefined" @wheel="pluginInteractive ? $event.stopPropagation() : undefined"></div>
      <template v-else>
      <button type="button" class="absolute right-2 top-2 btn btn-ghost btn-icon h-7 w-7 p-0 text-gray-500" :title="pluginEditing ? t('playground.canvasPluginPreview') : t('playground.canvasPluginEdit')" data-canvas-no-zoom @pointerdown.stop @click.stop="pluginEditing = !pluginEditing"><Icon :name="pluginEditing ? 'eye' : 'edit'" size="xs" /><span class="sr-only">{{ pluginEditing ? t('playground.canvasPluginPreview') : t('playground.canvasPluginEdit') }}</span></button>
      <textarea v-if="pluginEditing" :value="pluginContent" class="h-full min-h-44 w-full resize-none rounded border border-gray-200 bg-white p-3 text-xs leading-5 text-gray-800 outline-none dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100" :placeholder="t('playground.canvasPluginContentPlaceholder')" data-canvas-no-zoom @pointerdown.stop @input="updatePluginContent(($event.target as HTMLTextAreaElement).value)"></textarea>
      <template v-else-if="pluginRenderer === 'sticky-note'">
        <div class="absolute inset-0 flex min-h-48 flex-col items-stretch justify-between p-4 text-left" :style="{ backgroundColor: pluginColor }">
          <p class="whitespace-pre-wrap text-sm leading-6 text-stone-900">{{ pluginContent || t('playground.canvasStickyPlaceholder') }}</p>
          <input type="color" :value="pluginColor" class="h-7 w-7 cursor-pointer self-end rounded-full border-0 bg-transparent p-0" :title="t('playground.canvasStickyColor')" data-canvas-no-zoom @pointerdown.stop @input="updatePluginMetadata({ pluginColor: ($event.target as HTMLInputElement).value })">
        </div>
      </template>
      <div v-else-if="pluginRenderer === 'markdown'" class="prose prose-sm max-w-none self-stretch overflow-auto text-left dark:prose-invert" data-canvas-no-zoom v-html="pluginMarkdownHtml"></div>
      <div v-else-if="pluginRenderer === 'svg'" class="flex min-h-44 w-full items-center justify-center overflow-hidden bg-white/70 p-2 dark:bg-dark-900/70" data-canvas-no-zoom v-html="pluginSvgHtml"></div>
      <iframe v-else-if="pluginRenderer === 'html'" class="min-h-44 w-full flex-1 border-0 bg-white" sandbox="allow-scripts allow-forms" :srcdoc="pluginHtml" title="HTML plugin preview" data-canvas-no-zoom @pointerdown.stop></iframe>
      <div v-else-if="pluginRenderer === 'panorama'" class="relative min-h-44 w-full overflow-hidden rounded bg-black" data-canvas-no-zoom @pointerdown.stop="startPanoramaDrag" @pointermove.stop="movePanoramaDrag" @pointerup.stop="stopPanoramaDrag" @pointercancel.stop="stopPanoramaDrag" @wheel.prevent.stop="zoomPanorama">
        <div v-if="panoramaSource" class="absolute inset-0 cursor-grab bg-cover bg-center" :style="panoramaStyle"></div>
        <div v-else class="absolute inset-0 flex flex-col items-center justify-center gap-3 px-5 text-center text-gray-300"><Icon name="globe" size="lg" /><span class="text-xs">{{ t('playground.canvasPanoramaEmpty') }}</span><button type="button" class="btn btn-ghost h-8 gap-1 border border-white/30 px-3 text-xs text-white" data-canvas-no-zoom @pointerdown.stop @click.stop="panoramaFileInput?.click()"><Icon name="upload" size="xs" />{{ t('playground.canvasPanoramaUpload') }}</button></div>
        <input ref="panoramaFileInput" type="file" accept="image/*" class="hidden" @change="handlePanoramaFile">
        <span v-if="panoramaSource" class="absolute bottom-2 left-2 rounded bg-black/55 px-2 py-1 text-[10px] text-white">{{ t('playground.canvasPanoramaHint') }}</span>
      </div>
      <template v-else>
        <span class="flex h-12 w-12 items-center justify-center rounded-lg bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200"><Icon name="cube" size="lg" /></span>
        <p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ node.pluginTitle || node.type }}</p>
        <p v-if="node.pluginDescription" class="text-xs leading-5 text-gray-500 dark:text-dark-400">{{ node.pluginDescription }}</p>
        <p v-else class="text-xs text-gray-400 dark:text-dark-500">{{ t('playground.canvasPluginNodeHint') }}</p>
      </template>
      </template>
    </div>
    <div v-if="remotePluginDefinition?.Panel && pluginPanelVisible" ref="remotePluginPanelMount" class="border-t border-gray-100 bg-white p-3 dark:border-dark-700 dark:bg-dark-900" data-canvas-no-zoom @pointerdown.stop @wheel.stop></div>
    <section v-else-if="builtinPanelConfig && pluginPanelVisible" class="border-t border-gray-100 bg-white p-3 text-left dark:border-dark-700 dark:bg-dark-900" data-canvas-no-zoom data-canvas-plugin-builtin-panel @pointerdown.stop @wheel.stop>
      <div class="mb-2 flex items-center justify-between gap-2">
        <span class="text-xs font-medium text-gray-700 dark:text-gray-200">{{ t('playground.canvasPrompt') }}</span>
        <div class="flex items-center gap-1">
          <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasPrompt')" data-canvas-plugin-expand-prompt @click.stop="builtinPromptExpanded = true"><Icon name="document" size="xs" /><span class="sr-only">{{ t('playground.canvasPrompt') }}</span></button>
          <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click.stop="emit('close-plugin-panel')"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
        </div>
      </div>
      <div class="mb-2 flex min-h-8 items-center gap-1.5 overflow-x-auto" data-canvas-plugin-references>
        <span class="shrink-0 text-[11px] text-gray-500 dark:text-dark-400">{{ t('playground.canvasReferencesLabel') }}</span>
        <button type="button" class="btn btn-ghost btn-icon h-6 w-6 shrink-0 p-0" :class="{ 'bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-200': pluginReferenceSelecting }" :disabled="builtinBusy" :title="t('common.add')" data-canvas-plugin-add-reference @click.stop="emit('start-plugin-reference-selection', node.id)"><Icon name="plus" size="xs" /><span class="sr-only">{{ t('common.add') }}</span></button>
        <span v-for="reference in builtinReferenceNodes" :key="reference.id" class="inline-flex max-w-36 shrink-0 items-center gap-1 rounded border border-gray-200 bg-gray-50 py-0.5 pl-1.5 pr-0.5 text-[10px] text-gray-600 dark:border-dark-700 dark:bg-dark-950 dark:text-dark-300" :title="builtinReferenceLabel(reference)">
          <span class="truncate">{{ builtinReferenceLabel(reference) }}</span>
          <button type="button" class="btn btn-ghost btn-icon h-4 w-4 shrink-0 p-0 text-gray-400 hover:text-red-600" :disabled="builtinBusy" :title="t('common.remove')" @click.stop="emit('disconnect-plugin-reference', node.id, reference.id)"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.remove') }}</span></button>
        </span>
      </div>
      <textarea :value="builtinPrompt" rows="3" class="w-full resize-y rounded border border-gray-200 bg-gray-50 p-2 text-xs leading-5 text-gray-800 outline-none focus:border-teal-400 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100" :disabled="builtinBusy" :placeholder="t('playground.canvasPrompt')" data-canvas-plugin-prompt @input="emit('update-plugin-composer', node.id, ($event.target as HTMLTextAreaElement).value)" />
      <div class="mt-2 grid grid-cols-2 gap-2">
        <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.modelLabel') }}
          <select class="select mt-1 h-8 w-full text-xs" :value="builtinModel" :disabled="builtinBusy" data-canvas-plugin-model @change="emit('update-plugin-model', node.id, ($event.target as HTMLSelectElement).value)">
            <option v-for="model in builtinModelOptions" :key="model.value" :value="model.value">{{ model.label }}</option>
          </select>
        </label>
        <label v-if="builtinPanelConfig.mode === 'image'" class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasImageSize') }}
          <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('size', '1536x1024')" :disabled="builtinBusy" @change="updateBuiltinOption('size', ($event.target as HTMLSelectElement).value)"><option value="1024x1024">1024 x 1024</option><option value="1536x1024">1536 x 1024</option><option value="1024x1536">1024 x 1536</option></select>
        </label>
        <label v-else-if="builtinPanelConfig.mode === 'video'" class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasVideoResolution') }}
          <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('size', '720p')" :disabled="builtinBusy" @change="updateBuiltinOption('size', ($event.target as HTMLSelectElement).value)"><option value="480p">480p</option><option value="720p">720p</option><option value="1080p">1080p</option></select>
        </label>
        <label v-else-if="builtinPanelConfig.mode === 'audio'" class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">Voice
          <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('voice', 'Ara')" :disabled="builtinBusy" @change="updateBuiltinOption('voice', ($event.target as HTMLSelectElement).value)"><option value="Ara">Ara</option><option value="Rex">Rex</option><option value="Sal">Sal</option></select>
        </label>
        <label v-else class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">System
          <input class="input mt-1 h-8 w-full text-xs" :value="builtinOption('system', '')" :disabled="builtinBusy" @input="updateBuiltinOption('system', ($event.target as HTMLInputElement).value)">
        </label>
      </div>
      <div v-if="builtinPanelConfig.mode === 'image'" class="mt-2 grid grid-cols-2 gap-2">
        <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasQuality') }}
          <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('quality', 'auto')" :disabled="builtinBusy" @change="updateBuiltinOption('quality', ($event.target as HTMLSelectElement).value)"><option value="auto">Auto</option><option value="high">High</option><option value="medium">Medium</option><option value="low">Low</option></select>
        </label>
        <label class="block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasImageCount') }}
          <input class="input mt-1 h-8 w-full text-xs" :value="builtinOption('count', '1')" type="number" min="1" max="4" :disabled="builtinBusy" @change="updateBuiltinOption('count', ($event.target as HTMLInputElement).value)">
        </label>
      </div>
      <label v-else-if="builtinPanelConfig.mode === 'video'" class="mt-2 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasVideoDuration') }}
        <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('seconds', '8')" :disabled="builtinBusy" @change="updateBuiltinOption('seconds', ($event.target as HTMLSelectElement).value)"><option value="5">5s</option><option value="8">8s</option><option value="10">10s</option></select>
      </label>
      <label v-if="builtinPanelConfig.mode === 'video'" class="mt-2 block text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.canvasVideoAspectRatio') }}
        <select class="select mt-1 h-8 w-full text-xs" :value="builtinOption('aspectRatio', '16:9')" :disabled="builtinBusy" @change="updateBuiltinOption('aspectRatio', ($event.target as HTMLSelectElement).value)"><option value="16:9">16:9</option><option value="9:16">9:16</option><option value="1:1">1:1</option><option value="4:3">4:3</option><option value="3:4">3:4</option></select>
      </label>
      <div class="mt-3 flex justify-end gap-2">
        <button v-if="builtinBusy" type="button" class="btn btn-ghost h-8 gap-1 px-3 text-xs text-red-600" @click.stop="emit('cancel-plugin-generation', node.id)"><Icon name="x" size="xs" />{{ t('playground.canvasCancelGeneration') }}</button>
        <button v-else type="button" class="btn btn-primary h-8 gap-1 px-3 text-xs" :disabled="!builtinPrompt.trim()" data-canvas-plugin-generate @click.stop="emit('generate-plugin', node.id)"><Icon name="sparkles" size="xs" />{{ t('playground.generate') }}</button>
      </div>
    </section>
    <div v-else-if="!pluginNode" class="relative flex min-h-64 items-center justify-center bg-gray-100 dark:bg-dark-950">
      <video v-if="node.type === 'video' && node.videoUrl" ref="videoElement" :src="node.videoUrl" class="max-h-[360px] w-full object-contain" controls preload="metadata"></video>
      <audio v-else-if="node.type === 'audio' && node.audioUrl" :src="node.audioUrl" class="w-[90%]" controls preload="metadata"></audio>
            <img v-else-if="displayedImageUrl" :src="displayedImageUrl" :alt="node.prompt || t('playground.generatedImage')" class="max-h-[360px] w-full" :class="node.freeResize ? 'object-fill' : 'object-contain'" draggable="false">
      <div v-else class="flex min-h-64 flex-col items-center justify-center gap-2 px-5 text-center text-gray-400 dark:text-dark-400">
        <Icon :name="displayedImageSlot?.status === 'error' || node.status === 'error' ? 'exclamationCircle' : 'sparkles'" size="lg" :class="displayedImageSlot?.status === 'error' || node.status === 'error' ? 'text-red-500' : ''" />
        <span class="text-xs">{{ displayedImageSlot?.status === 'pending' || node.status === 'generating' ? t('playground.canvasGenerating') : displayedImageSlot?.status === 'error' ? (displayedImageSlot.errorMessage || t('playground.canvasGenerationFailed')) : node.status === 'error' ? node.errorMessage : t('playground.canvasEmptyNode') }}</span>
        <button v-if="node.type === 'image' && node.kind !== 'result' && displayedImageSlot?.status !== 'pending' && node.status !== 'generating'" type="button" class="btn btn-secondary h-7 gap-1 px-2.5 text-[11px]" :title="t('playground.canvasUploadImage')" @click.stop="openImageUpload">
          <Icon name="upload" size="xs" />
          {{ t('playground.canvasUploadImage') }}
        </button>
        <button v-if="displayedImageSlot?.status === 'error'" type="button" class="btn btn-secondary h-7 gap-1 px-2.5 text-[11px]" @click.stop="emit('retry-image', node.id, imageIndex)"><Icon name="refresh" size="xs" />{{ t('playground.canvasRetryImage') }}</button>
      </div>
      <span v-if="imageSlots.length > 1" class="absolute bottom-2 right-2 rounded bg-gray-950/70 px-1.5 py-0.5 text-[10px] text-white">{{ imageSlots.length }}</span>
      <template v-if="imageSlots.length > 1">
        <button type="button" class="absolute left-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-gray-950/55 text-white opacity-0 transition-opacity hover:bg-gray-950/80 group-hover:opacity-100" :title="t('playground.canvasPreviousResult')" data-canvas-no-zoom @click.stop="stepImage(-1)"><Icon name="chevronLeft" size="xs" /><span class="sr-only">{{ t('playground.canvasPreviousResult') }}</span></button>
        <button type="button" class="absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-full bg-gray-950/55 text-white opacity-0 transition-opacity hover:bg-gray-950/80 group-hover:opacity-100" :title="t('playground.canvasNextResult')" data-canvas-no-zoom @click.stop="stepImage(1)"><Icon name="chevronRight" size="xs" /><span class="sr-only">{{ t('playground.canvasNextResult') }}</span></button>
        <span class="absolute bottom-2 left-1/2 -translate-x-1/2 rounded bg-gray-950/65 px-1.5 py-0.5 text-[10px] text-white">{{ imageIndex + 1 }} / {{ imageSlots.length }}</span>
      </template>
    </div>
    <div v-if="node.type === 'image' && imageSlots.length > 1" class="border-t border-gray-100 bg-white/95 px-3 py-2 dark:border-dark-700 dark:bg-dark-900/95" data-canvas-image-batch data-canvas-no-zoom>
      <button type="button" class="flex w-full items-center justify-between gap-2 rounded-md px-1 py-1 text-left text-[10px] font-medium text-gray-500 transition hover:bg-gray-50 hover:text-teal-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-teal-200" data-canvas-image-batch-toggle @click.stop="imageBatchExpanded = !imageBatchExpanded">
        <span>{{ t('playground.canvasImageAlternatives', { count: imageSlots.length }) }}</span>
        <span class="inline-flex items-center gap-1">
          <span>{{ imageBatchExpanded ? t('playground.canvasTextCollapseAlternatives') : t('playground.canvasTextExpandAlternatives') }}</span>
          <Icon :name="imageBatchExpanded ? 'chevronUp' : 'chevronDown'" size="xs" />
        </span>
      </button>
      <div v-if="imageBatchExpanded" class="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-3" data-canvas-image-batch-list>
        <div v-for="(slot, index) in imageSlots" :key="slot.id" class="group relative overflow-hidden rounded-md border" :class="index === imageIndex ? 'border-teal-400 ring-1 ring-teal-300' : 'border-gray-200 dark:border-dark-700'" data-canvas-image-batch-item>
          <button v-if="slot.status === 'success' && slot.url" type="button" class="block aspect-square w-full overflow-hidden bg-gray-100 dark:bg-dark-800" :title="t('playground.canvasSetPrimaryImage')" @click.stop="selectImageVariant(index)" @dblclick.stop="emit('view-image', node.id, index)">
            <img :src="slot.url" :alt="`${t('playground.canvasResult', { index: index + 1 })}`" class="h-full w-full object-cover">
          </button>
          <div v-else class="flex aspect-square w-full flex-col items-center justify-center gap-2 bg-red-50 p-2 text-center dark:bg-red-950/30">
            <Icon :name="slot.status === 'pending' ? 'sparkles' : 'exclamationCircle'" size="sm" :class="slot.status === 'pending' ? 'animate-pulse text-teal-600' : 'text-red-500'" />
            <span class="line-clamp-3 text-[10px] leading-4 text-red-700 dark:text-red-300">{{ slot.status === 'pending' ? t('playground.canvasGenerating') : (slot.errorMessage || t('playground.canvasGenerationFailed')) }}</span>
            <button v-if="slot.status === 'error'" type="button" class="btn btn-secondary h-6 gap-1 px-2 text-[10px]" :title="t('playground.canvasRetryImage')" @click.stop="emit('retry-image', node.id, index)"><Icon name="refresh" size="xs" />{{ t('playground.canvasRetryImage') }}</button>
          </div>
          <div class="absolute inset-x-1 bottom-1 flex items-center justify-between gap-1">
            <span class="rounded bg-gray-950/65 px-1.5 py-0.5 text-[9px] text-white">{{ index + 1 }}</span>
            <div v-if="slot.status === 'success' && slot.url" class="flex items-center gap-0.5">
              <button type="button" class="grid h-6 w-6 place-items-center rounded bg-white/90 text-gray-600 shadow-sm hover:text-teal-700 dark:bg-dark-900/90 dark:text-dark-200 dark:hover:text-teal-200" :title="t('playground.canvasDownload')" @click.stop="emit('download', node.id, index)"><Icon name="download" size="xs" /><span class="sr-only">{{ t('playground.canvasDownload') }}</span></button>
              <button v-if="node.kind === 'result'" type="button" class="grid h-6 w-6 place-items-center rounded bg-white/90 text-teal-700 shadow-sm dark:bg-dark-900/90 dark:text-teal-200" :title="t('playground.canvasUseAsReference')" @click.stop="emit('use-reference', node.id, index)"><Icon name="upload" size="xs" /><span class="sr-only">{{ t('playground.canvasUseAsReference') }}</span></button>
              <button type="button" class="grid h-6 w-6 place-items-center rounded bg-white/90 text-gray-600 shadow-sm hover:text-teal-700 dark:bg-dark-900/90 dark:text-dark-200 dark:hover:text-teal-200" :title="t('playground.canvasDuplicateNode')" @click.stop="emit('duplicate-image', node.id, index)"><Icon name="copy" size="xs" /><span class="sr-only">{{ t('playground.canvasDuplicateNode') }}</span></button>
            </div>
            <button type="button" class="grid h-6 w-6 place-items-center rounded bg-white/90 text-gray-600 shadow-sm hover:text-red-600 dark:bg-dark-900/90 dark:text-dark-200" :title="t('playground.canvasDeleteImage')" @click.stop="emit('delete-image', node.id, index)"><Icon name="trash" size="xs" /><span class="sr-only">{{ t('playground.canvasDeleteImage') }}</span></button>
          </div>
        </div>
      </div>
    </div>

    <footer class="space-y-2 border-t border-gray-100 px-3 py-2.5 dark:border-dark-700" data-canvas-no-zoom>
      <p v-if="node.model" class="truncate text-[11px] text-gray-500 dark:text-dark-400">{{ node.model }}</p>
      <div class="flex items-center justify-between gap-2">
        <span class="truncate text-[11px] text-gray-400">
          {{ node.type === 'text' ? t('playground.canvasTextNode') : node.type === 'group' ? t('playground.canvasGroupCount', { count: groupChildCount ?? 0 }) : node.type === 'audio' ? t('playground.canvasAudioNode') : node.type === 'config' ? t('playground.canvasConfigNode') : pluginNode ? t('playground.canvasPluginNode') : node.kind === 'result' ? node.model : node.referenceImages?.length ? t('playground.canvasReferences', { count: node.referenceImages.length }) : t('playground.canvasTextToImage') }}
        </span>
        <button v-if="(node.type === 'image' && (node.imageUrl || node.imageUrls?.length)) || (node.type === 'video' && node.videoUrl) || (node.type === 'audio' && node.audioUrl) || (node.type === 'text' && (node.textContent || node.prompt).trim())" type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0 text-teal-700 dark:text-teal-300" :title="t('playground.canvasSaveAsset')" data-canvas-no-zoom @pointerdown.stop @click.stop="emit('save-asset', node.id, imageIndex)">
          <Icon name="inbox" size="xs" />
          <span class="sr-only">{{ t('playground.canvasSaveAsset') }}</span>
        </button>
        <div v-if="(node.type === 'image' && (node.kind === 'result' || node.imageCacheKey || node.imageCacheKeys?.length)) || (node.type === 'audio' && node.kind === 'result' && node.audioUrl)" class="flex shrink-0 items-center gap-1">
          <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-teal-700 dark:text-teal-300" :title="t('playground.canvasUseAsReference')" data-canvas-no-zoom @pointerdown.stop @click.stop="emit('use-reference', node.id, imageIndex)">
            <Icon name="upload" size="xs" />
            <span class="sr-only">{{ t('playground.canvasUseAsReference') }}</span>
          </button>
          <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-gray-500" :title="t('playground.canvasDownload')" data-canvas-no-zoom @click.stop="emit('download', node.id, imageIndex)">
            <Icon name="download" size="xs" :class="{ 'animate-pulse': downloading }" />
            <span class="sr-only">{{ t('playground.canvasDownload') }}</span>
          </button>
          <button v-if="node.type === 'audio'" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-gray-500" :class="transcribing ? 'text-teal-700' : ''" :disabled="transcribing" :title="t('playground.canvasTranscribeAudio')" data-canvas-no-zoom @click.stop="emit('transcribe-audio', node.id)">
            <Icon name="document" size="xs" :class="{ 'animate-pulse': transcribing }" />
            <span class="sr-only">{{ t('playground.canvasTranscribeAudio') }}</span>
          </button>
        </div>
        <button v-else-if="node.type === 'text' && node.kind !== 'result'" type="button" class="btn h-8 shrink-0 gap-1 px-2.5 text-xs" :class="node.status === 'generating' ? 'btn-ghost text-red-600' : 'btn-primary'" :title="node.status === 'generating' ? t('playground.canvasCancelGeneration') : (node.textContent || node.prompt).trim() ? t('playground.canvasRewriteText') : t('playground.canvasGenerateText')" data-canvas-no-zoom @click.stop="node.status === 'generating' ? emit('cancel-generation', node.id) : emit('generate-text', node.id)">
          <Icon :name="node.status === 'generating' ? 'x' : 'sparkles'" size="xs" />
          {{ node.status === 'generating' ? t('playground.canvasCancelGeneration') : (node.textContent || node.prompt).trim() ? t('playground.canvasRewriteText') : t('playground.canvasGenerateText') }}
        </button>
        <button v-else-if="node.type === 'video' && node.kind !== 'result'" type="button" class="btn h-8 shrink-0 gap-1 px-2.5 text-xs" :class="node.status === 'generating' ? 'btn-ghost text-red-600' : 'btn-primary'" :disabled="node.status !== 'generating' && !node.prompt.trim()" :title="node.status === 'generating' ? t('playground.canvasCancelGeneration') : t('playground.canvasGenerateVideo')" data-canvas-no-zoom @click.stop="node.status === 'generating' ? emit('cancel-video', node.id) : emit('generate-video', node.id)">
          <Icon :name="node.status === 'generating' ? 'x' : 'play'" size="xs" />
          {{ node.status === 'generating' ? t('playground.canvasCancelGeneration') : t('playground.canvasGenerateVideo') }}
        </button>
        <div v-else-if="node.type === 'audio' && node.kind !== 'result'" class="flex shrink-0 items-center gap-1">
          <button type="button" class="btn h-8 shrink-0 gap-1 px-2.5 text-xs" :class="node.status === 'generating' ? 'btn-ghost text-red-600' : 'btn-primary'" :disabled="node.status === 'generating' || !node.prompt.trim()" :title="t('playground.canvasGenerateAudio')" data-canvas-no-zoom @click.stop="emit('generate-audio', node.id)"><Icon :name="node.status === 'generating' ? 'x' : 'play'" size="xs" />{{ node.status === 'generating' ? t('playground.canvasGenerating') : t('playground.canvasGenerateAudio') }}</button>
          <button v-if="node.audioUrl" type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-gray-500" :class="transcribing ? 'text-teal-700' : ''" :disabled="transcribing" :title="t('playground.canvasTranscribeAudio')" data-canvas-no-zoom @click.stop="emit('transcribe-audio', node.id)"><Icon name="document" size="xs" :class="{ 'animate-pulse': transcribing }" /><span class="sr-only">{{ t('playground.canvasTranscribeAudio') }}</span></button>
        </div>
        <button v-else-if="node.type === 'image'" type="button" class="btn btn-primary h-8 shrink-0 gap-1 px-2.5 text-xs" :disabled="node.status === 'generating' || !node.prompt.trim()" data-canvas-no-zoom @click.stop="emit('generate', node.id)">
          <Icon name="sparkles" size="xs" />
          {{ node.status === 'generating' ? t('playground.canvasGenerating') : t('playground.generate') }}
        </button>
      </div>
    </footer>
    </div>
  </article>
  <Teleport to="body">
    <div v-if="imageToolbarSettingsOpen" class="fixed inset-0 z-[130] flex items-center justify-center bg-gray-950/65 p-4 backdrop-blur-sm" data-canvas-no-zoom data-canvas-image-toolbar-settings @pointerdown.self="cancelImageToolbarSettings">
      <section class="flex max-h-[calc(100vh-2rem)] w-full max-w-lg flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasCustomizeImageToolbar')">
        <header class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <div>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasCustomizeImageToolbar') }}</h2>
            <p class="mt-0.5 text-[11px] text-gray-500 dark:text-dark-400">{{ t('playground.canvasCustomizeImageToolbarHint') }}</p>
          </div>
          <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="cancelImageToolbarSettings"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
        </header>
        <div class="thin-scrollbar max-h-[55vh] space-y-1 overflow-y-auto p-4">
          <label v-for="tool in imageToolbarToolDefinitions" :key="tool.id" class="flex cursor-pointer items-center justify-between gap-3 rounded-lg border border-transparent px-3 py-2.5 transition hover:border-gray-200 hover:bg-gray-50 dark:hover:border-dark-700 dark:hover:bg-dark-800">
            <span class="min-w-0">
              <span class="block text-xs font-medium text-gray-800 dark:text-gray-100">{{ t(tool.label) }}</span>
              <span class="block truncate text-[10px] text-gray-400 dark:text-dark-500">{{ t(tool.hint) }}</span>
            </span>
            <input type="checkbox" class="h-4 w-4 accent-teal-600" :checked="imageToolbarSettingsDraft.includes(tool.id)" @change="toggleImageToolbarDraft(tool.id)">
          </label>
        </div>
        <footer class="flex items-center justify-between gap-2 border-t border-gray-100 px-4 py-3 dark:border-dark-700">
          <button type="button" class="btn btn-ghost h-8 px-3 text-xs" @click="resetImageToolbarDraft">{{ t('common.reset') }}</button>
          <div class="flex gap-2">
            <button type="button" class="btn btn-secondary h-8 px-3 text-xs" @click="cancelImageToolbarSettings">{{ t('common.cancel') }}</button>
            <button type="button" class="btn btn-primary h-8 px-3 text-xs" @click="saveImageToolbarSettings">{{ t('common.save') }}</button>
          </div>
        </footer>
      </section>
    </div>
  </Teleport>
  <Teleport to="body">
    <div v-if="builtinPromptExpanded" class="fixed inset-0 z-[130] flex items-center justify-center bg-gray-950/65 p-4 backdrop-blur-sm" data-canvas-no-zoom data-canvas-plugin-prompt-dialog @pointerdown.self="builtinPromptExpanded = false">
      <section class="flex max-h-[calc(100vh-2rem)] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('playground.canvasPrompt')">
        <header class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasPrompt') }}</h2>
          <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="builtinPromptExpanded = false"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
        </header>
        <textarea :value="builtinPrompt" rows="16" class="m-4 min-h-64 resize-y rounded border border-gray-200 bg-gray-50 p-3 text-sm leading-6 text-gray-800 outline-none focus:border-teal-400 dark:border-dark-700 dark:bg-dark-950 dark:text-gray-100" :disabled="builtinBusy" :placeholder="t('playground.canvasPrompt')" data-canvas-plugin-expanded-prompt @input="emit('update-plugin-composer', node.id, ($event.target as HTMLTextAreaElement).value)" @keydown.esc="builtinPromptExpanded = false" />
        <footer class="flex justify-end border-t border-gray-100 px-4 py-3 dark:border-dark-700"><button type="button" class="btn btn-primary h-8 px-3 text-xs" @click="builtinPromptExpanded = false">{{ t('common.close') }}</button></footer>
      </section>
    </div>
  </Teleport>
  <input ref="imageUploadInput" type="file" accept="image/*" class="hidden" data-canvas-no-zoom @change="handleImageUpload">
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import type { PlaygroundModel } from '@/features/playground/types'
import type { CanvasImageVariant, CanvasNode, CanvasTextVariant } from './types'
import { isPluginNodeType } from './canvasPlugins'
import CanvasMentionEditor from './CanvasMentionEditor.vue'
import CanvasConfigSettingsPopover from './CanvasConfigSettingsPopover.vue'
import CanvasNodeReferenceBar from './CanvasNodeReferenceBar.vue'
import type { CanvasMentionReference, CanvasReferenceItem } from './canvasReferences'
import { renderPlaygroundMarkdown } from '@/features/playground/markdown'
import { sanitizeSvg } from '@/utils/sanitize'
import DOMPurify from 'dompurify'
import { mountCanvasPluginContent, mountCanvasPluginPanel, type CanvasPluginMount, type CanvasPluginRuntimeContext, type CanvasPluginRuntimeNodeDefinition } from './canvasPluginRuntime'

const props = defineProps<{
  node: CanvasNode
  selected: boolean
  connectionTarget?: boolean
  connecting?: boolean
  related?: boolean
  focusRelated?: boolean
  referenceSelectionState?: 'target' | 'disabled' | 'available'
  groupChildCount?: number
  isGroupDropTarget?: boolean
  downloading?: boolean
  reversePromptBusy?: boolean
  upscaleBusy?: boolean
  transcribing?: boolean
  remotePluginDefinition?: CanvasPluginRuntimeNodeDefinition
  pluginContext?: CanvasPluginRuntimeContext
  pluginPanelOpen?: boolean
  pluginGenerationBusy?: boolean
  configBusy?: boolean
  pluginReferenceSelecting?: boolean
  mentionReferences?: CanvasMentionReference[]
  incomingReferences?: CanvasReferenceItem[]
  imageModels?: PlaygroundModel[]
  textModels?: PlaygroundModel[]
  videoModels?: PlaygroundModel[]
}>()

const emit = defineEmits<{
  select: [id: string, event: PointerEvent]
  delete: [id: string]
  generate: [id: string]
  'generate-text': [id: string]
  'generate-image-from-text': [id: string]
  'cancel-generation': [id: string]
  'generate-video': [id: string]
  'cancel-video': [id: string]
  'generate-audio': [id: string]
  'transcribe-audio': [id: string]
  download: [id: string, imageIndex?: number]
  'use-reference': [id: string, imageIndex?: number]
  'select-image': [id: string, imageIndex: number]
  'retry-image': [id: string, imageIndex: number]
  'duplicate-image': [id: string, imageIndex: number]
  'delete-image': [id: string, imageIndex: number]
  'edit-image': [id: string, imageIndex: number]
  'transform-image': [id: string, imageIndex: number, operation: 'rotate-left' | 'rotate-right' | 'flip-horizontal']
  'reverse-prompt': [id: string, imageIndex: number]
  'split-image': [id: string, imageIndex: number]
  'upscale-image': [id: string, imageIndex: number]
  'view-image': [id: string, imageIndex: number]
  'mask-image': [id: string, imageIndex: number]
  'angle-image': [id: string, imageIndex: number]
  'toggle-free-resize': [id: string]
  'capture-video-frame': [id: string, position: 'first' | 'current' | 'last', currentTime?: number]
  'save-asset': [id: string, imageIndex?: number]
  'upload-image': [id: string, file: File]
  'show-info': [id: string]
  'connect-start': [id: string, handleType: 'source' | 'target']
  'update-prompt': [id: string, prompt: string]
  'select-text-variant': [id: string, variantId: string]
  'update-text-size': [id: string, size: number]
  'update-plugin': [id: string, patch: Record<string, unknown>]
  'update-plugin-composer': [id: string, prompt: string]
  'update-plugin-model': [id: string, model: string]
  'generate-plugin': [id: string]
  'cancel-plugin-generation': [id: string]
  'start-plugin-reference-selection': [id: string]
  'disconnect-plugin-reference': [targetId: string, sourceId: string]
  'start-reference-selection': []
  'disconnect-reference': [targetId: string, sourceId: string]
  'focus-reference': [nodeId: string]
  'close-plugin-panel': []
  'double-click': [id: string]
  'update-config': [id: string, key: 'mode' | 'model' | 'count' | 'size' | 'quality' | 'background' | 'resolution' | 'duration' | 'aspectRatio' | 'audioVoice' | 'audioFormat' | 'audioSpeed' | 'audioInstructions' | 'reasoningEffort', value: string]
  'generate-config': [id: string]
  'resize-start': [id: string, corner: ResizeCorner, event: PointerEvent]
  'context-menu': [id: string, event: MouseEvent]
  'select-reference': [id: string]
  'hover-start': [id: string]
  'hover-end': [id: string]
  'update-title': [id: string, title: string]
}>()

const { t } = useI18n()
const nodeHovered = ref(false)
const titleEditing = ref(false)
const titleDraft = ref('')
const titleInputRef = ref<HTMLInputElement | null>(null)
const fallbackNodeTitle = computed(() => {
  if (props.node.kind === 'result') return t('playground.canvasResult', { index: (props.node.resultIndex ?? 0) + 1 })
  if (props.node.type === 'group') return t('playground.canvasGroupNode')
  if (props.node.type === 'config') return t('playground.canvasConfigNode')
  if (isPluginNodeType(props.node.type)) return props.node.pluginTitle || props.node.type
  return t(`playground.canvasNodeType.${props.node.type}`)
})

watch(() => props.node.title, (value) => {
  if (!titleEditing.value) titleDraft.value = value || ''
}, { immediate: true })

watch(titleEditing, async (editing) => {
  if (editing) {
    await nextTick()
    titleInputRef.value?.focus()
    titleInputRef.value?.select()
  }
})

function startTitleEditing(): void {
  if (props.referenceSelectionState) return
  titleDraft.value = props.node.title || ''
  titleEditing.value = true
}

function cancelTitleEditing(): void {
  titleDraft.value = props.node.title || ''
  titleEditing.value = false
}

function finishTitleEditing(): void {
  if (!titleEditing.value) return
  const title = titleDraft.value.trim().slice(0, 64)
  titleEditing.value = false
  if (title !== (props.node.title || '')) emit('update-title', props.node.id, title)
}

function handleNodePointerEnter(): void {
  nodeHovered.value = true
  emit('hover-start', props.node.id)
}

function handleNodePointerLeave(): void {
  nodeHovered.value = false
  emit('hover-end', props.node.id)
}
const imageToolbarToolDefinitions = [
  { id: 'crop', label: 'playground.canvasCropImage', hint: 'playground.canvasCropImage' },
  { id: 'rotateLeft', label: 'playground.canvasRotateLeft', hint: 'playground.canvasRotateLeft' },
  { id: 'rotateRight', label: 'playground.canvasRotateRight', hint: 'playground.canvasRotateRight' },
  { id: 'flipHorizontal', label: 'playground.canvasFlipHorizontal', hint: 'playground.canvasFlipHorizontal' },
  { id: 'reversePrompt', label: 'playground.canvasReversePrompt', hint: 'playground.canvasReversePrompt' },
  { id: 'split', label: 'playground.canvasSplitImage', hint: 'playground.canvasSplitImage' },
  { id: 'upscale', label: 'playground.canvasUpscaleImage', hint: 'playground.canvasUpscaleImage' },
  { id: 'view', label: 'playground.canvasViewImage', hint: 'playground.canvasViewImage' },
  { id: 'mask', label: 'playground.canvasMaskImage', hint: 'playground.canvasMaskImage' },
  { id: 'angle', label: 'playground.canvasAngleImage', hint: 'playground.canvasAngleImage' },
  { id: 'freeResize', label: 'playground.canvasFreeResize', hint: 'playground.canvasFreeResize' },
  { id: 'download', label: 'playground.canvasDownload', hint: 'playground.canvasDownload' },
  { id: 'saveAsset', label: 'playground.canvasSaveAsset', hint: 'playground.canvasSaveAsset' },
] as const
type ImageToolbarToolId = typeof imageToolbarToolDefinitions[number]['id']
const defaultImageToolbarToolIds: ImageToolbarToolId[] = ['crop', 'reversePrompt', 'split', 'upscale', 'view', 'mask', 'download', 'saveAsset']
const imageToolbarSettingsOpen = ref(false)
const imageToolbarToolIds = ref<ImageToolbarToolId[]>([...defaultImageToolbarToolIds])
const imageToolbarSettingsDraft = ref<ImageToolbarToolId[]>([...defaultImageToolbarToolIds])
const imageIndex = ref(Math.max(0, props.node.primaryImageIndex ?? 0))
const imageBatchExpanded = ref(false)
const imageUploadInput = ref<HTMLInputElement | null>(null)
const textVariantsExpanded = ref(false)
const configComposerOpen = ref(false)
const videoElement = ref<HTMLVideoElement | null>(null)
const imageUrls = computed(() => (props.node.imageUrls ?? []).filter(Boolean))
const imageSlots = computed<CanvasImageVariant[]>(() => {
  if (props.node.imageVariants?.length) return props.node.imageVariants
  return imageUrls.value.map((url, index) => ({ id: `${props.node.id}-image-${index}`, status: 'success', url, cacheKey: props.node.imageCacheKeys?.[index] }))
})
const displayedImageSlot = computed(() => imageSlots.value[imageIndex.value])
const displayedImageUrl = computed(() => {
  const slot = imageSlots.value[imageIndex.value]
  return slot?.status === 'success' ? slot.url : undefined
})
const textVariants = computed<CanvasTextVariant[]>(() => props.node.textVariants ?? [])
const primaryTextId = computed(() => props.node.primaryTextId || textVariants.value.find((variant) => variant.status === 'success')?.id || textVariants.value[0]?.id)
const displayedTextContent = computed(() => {
  const primary = textVariants.value.find((variant) => variant.id === primaryTextId.value)
  return primary?.content || props.node.textContent || props.node.prompt
})
const pluginNode = computed(() => isPluginNodeType(props.node.type))
const pluginRenderer = computed(() => props.node.pluginRenderer ?? 'generic')
const builtinPanelConfig = computed(() => props.node.pluginUseBuiltinPanel)
const builtinBusy = computed(() => Boolean(props.pluginGenerationBusy))
const builtinPrompt = computed(() => String(props.node.pluginMetadata?.composerContent ?? props.node.prompt ?? ''))
const builtinPromptExpanded = ref(false)
const builtinOptions = computed<Record<string, string>>(() => {
  const value = props.node.pluginMetadata?.composerOptions
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, string> : {}
})
const builtinModelOptions = computed(() => builtinPanelConfig.value ? props.pluginContext?.ai.listModels(builtinPanelConfig.value.mode) ?? [] : [])
const builtinModel = computed(() => props.node.model || (builtinPanelConfig.value ? props.pluginContext?.ai.defaultModel(builtinPanelConfig.value.mode) ?? '' : ''))
const configMode = computed<'image' | 'text' | 'video' | 'audio'>(() => props.node.config?.mode ?? 'image')
const configModelOptions = computed(() => {
  if (configMode.value === 'image') return props.imageModels ?? []
  if (configMode.value === 'video') return props.videoModels ?? []
  return props.textModels ?? []
})
const configModel = computed(() => props.node.config?.model || props.node.model || configModelOptions.value[0]?.id || '')
const builtinReferenceNodes = computed(() => builtinPanelConfig.value ? props.pluginContext?.getUpstream() ?? [] : [])
const remotePluginHeight = computed(() => Math.max(176, props.node.height - 104))
const pluginInteractive = computed(() => {
  const definition = props.remotePluginDefinition
  if (!definition?.interactionToggle) return true
  if (!String(props.node.pluginMetadata?.content ?? '').trim()) return true
  try {
    if (definition.forceInteractive?.(props.node)) return true
  } catch (error) {
    console.warn('[canvas-plugin] forceInteractive failed', error)
  }
  return props.node.pluginMetadata?.interactive === true
})
const pluginContent = computed(() => String(props.node.pluginMetadata?.content ?? props.node.textContent ?? props.node.prompt ?? ''))
const pluginColor = computed(() => typeof props.node.pluginMetadata?.pluginColor === 'string' ? props.node.pluginMetadata.pluginColor : '#fde68a')
const pluginMarkdownHtml = computed(() => renderPlaygroundMarkdown(pluginContent.value))
const pluginSvgHtml = computed(() => sanitizeSvg(pluginContent.value))
const pluginHtml = computed(() => DOMPurify.sanitize(pluginContent.value, { ADD_TAGS: ['style'], ADD_ATTR: ['target'] }))
const pluginEditing = ref(false)
const isEditingContent = ref(false)
const textEditorRef = ref<{ focus: () => void } | null>(null)
const remotePluginMount = ref<HTMLElement | null>(null)
const remotePluginPanelMount = ref<HTMLElement | null>(null)
const pluginPanelVisible = ref(Boolean(props.pluginPanelOpen))
let remotePluginMountController: CanvasPluginMount | null = null
let remotePluginPanelController: CanvasPluginMount | null = null
const panoramaFileInput = ref<HTMLInputElement | null>(null)
const panoramaLon = ref(0)
const panoramaLat = ref(50)
const panoramaZoom = ref(1)
const panoramaDragging = ref(false)
const panoramaDragStart = ref({ x: 0, y: 0, lon: 0, lat: 50 })
let panoramaTimer: number | null = null
const panoramaSource = computed(() => pluginRenderer.value === 'panorama' ? pluginContent.value.trim() : '')
const panoramaStyle = computed(() => ({ backgroundImage: `url(${panoramaSource.value})`, backgroundSize: `${100 + panoramaZoom.value * 50}% 100%`, backgroundPosition: `${panoramaLon.value}% ${panoramaLat.value}%` }))
const remoteToolbarItems = computed(() => {
  if (!props.remotePluginDefinition?.toolbar || !props.pluginContext) return []
  let toolbar: ReturnType<NonNullable<CanvasPluginRuntimeNodeDefinition['toolbar']>> = []
  try {
    toolbar = props.remotePluginDefinition.toolbar(props.pluginContext)
  } catch (error) {
    console.warn('[canvas-plugin] toolbar failed', error)
  }
  const items = toolbar.slice(0, 7).map((item) => ({ ...item, icon: typeof item.icon === 'string' ? item.icon : '' }))
  if (props.remotePluginDefinition.interactionToggle && pluginContent.value.trim()) items.unshift({ id: 'canvas-plugin-interaction', title: pluginInteractive.value ? t('playground.canvasPluginMove') : t('playground.canvasPluginInteract'), label: pluginInteractive.value ? t('playground.canvasPluginMove') : t('playground.canvasPluginInteract'), icon: pluginInteractive.value ? 'M' : 'I', active: pluginInteractive.value, danger: false, onClick: () => updatePluginMetadata({ interactive: !pluginInteractive.value }) })
  return items
})
const toolbarPlacementClass = computed(() => props.node.y < 64 ? 'top-full mt-2' : '-top-11')

function handleNodePointerDown(event: PointerEvent): void {
  if (props.referenceSelectionState === 'available') {
    emit('select-reference', props.node.id)
    return
  }
  if (props.referenceSelectionState) return
  emit('select', props.node.id, event)
}
watch(() => [props.node.imageUrls, props.node.imageVariants], () => {
  const maxIndex = Math.max(0, imageSlots.value.length - 1)
  imageIndex.value = Math.min(props.node.primaryImageIndex ?? 0, maxIndex)
  if (imageSlots.value.length <= 1) imageBatchExpanded.value = false
}, { deep: true, immediate: true })
watch(() => props.node.primaryImageIndex, (index) => {
  const maxIndex = Math.max(0, imageSlots.value.length - 1)
  imageIndex.value = Math.min(Math.max(index ?? 0, 0), maxIndex)
}, { immediate: true })
watch(() => props.node.textVariants, (variants) => {
  if (!variants || variants.length <= 1) textVariantsExpanded.value = false
}, { deep: true })
watch(() => props.selected, (selected) => {
  if (!selected) isEditingContent.value = false
})
watch(isEditingContent, (editing, _previous, onCleanup) => {
  if (!editing || typeof window === 'undefined') return
  const handleOutsidePointerDown = (event: PointerEvent) => {
    const target = event.target
    if (!(target instanceof Element)) return
    const nodeElement = target.closest<HTMLElement>('[data-node-id]')
    if (nodeElement?.dataset.nodeId === props.node.id) return
    isEditingContent.value = false
  }
  window.addEventListener('pointerdown', handleOutsidePointerDown, true)
  onCleanup(() => window.removeEventListener('pointerdown', handleOutsidePointerDown, true))
})
const textSizeOptions = [12, 14, 16, 18, 22, 28, 36] as const
const resizeHandles = ['top-left', 'top-right', 'bottom-left', 'bottom-right'] as const
type ResizeCorner = typeof resizeHandles[number]
const resizeHandleClass: Record<ResizeCorner, string> = {
  'top-left': '-left-1.5 -top-1.5 cursor-nwse-resize',
  'top-right': '-right-1.5 -top-1.5 cursor-nesw-resize',
  'bottom-left': '-left-1.5 -bottom-1.5 cursor-nesw-resize',
  'bottom-right': '-right-1.5 -bottom-1.5 cursor-nwse-resize',
}

function stepImage(offset: number): void {
  const count = imageSlots.value.length
  if (count < 2) return
  imageIndex.value = (imageIndex.value + offset + count) % count
}

function selectImageVariant(index: number): void {
  const slot = imageSlots.value[index]
  if (!slot || slot.status !== 'success' || !slot.url) return
  imageIndex.value = index
  emit('select-image', props.node.id, index)
}

function updatePluginContent(content: string): void {
  emit('update-prompt', props.node.id, content)
}

function handleNodeDoubleClick(): void {
  if (props.node.type === 'text' && props.node.kind !== 'result') {
    void focusTextEditor()
    return
  }
  if (props.node.type === 'image' && displayedImageUrl.value) {
    emit('view-image', props.node.id, imageIndex.value)
    return
  }
  if (pluginNode.value && !props.remotePluginDefinition) pluginEditing.value = true
  emit('double-click', props.node.id)
}

async function focusTextEditor(): Promise<void> {
  if (!isEditingContent.value) isEditingContent.value = true
  await nextTick()
  const editor = textEditorRef.value
  if (!editor) return
  editor.focus()
}

function stopEditingText(): void {
  isEditingContent.value = false
}

function adjustTextSize(direction: -1 | 1): void {
  const current = props.node.textFontSize ?? 16
  const sorted = [...textSizeOptions]
  const next = direction > 0
    ? sorted.find((size) => size > current) ?? sorted[sorted.length - 1]
    : [...sorted].reverse().find((size) => size < current) ?? sorted[0]
  emit('update-text-size', props.node.id, next)
}

function updatePluginMetadata(patch: Record<string, unknown>): void {
  emit('update-plugin', props.node.id, patch)
}

function imageToolbarVisible(id: ImageToolbarToolId): boolean {
  return imageToolbarToolIds.value.includes(id)
}

function openImageToolbarSettings(): void {
  imageToolbarSettingsDraft.value = [...imageToolbarToolIds.value]
  imageToolbarSettingsOpen.value = true
}

function openImageUpload(): void {
  imageUploadInput.value?.click()
}

function handleImageUpload(event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file?.type.startsWith('image/')) emit('upload-image', props.node.id, file)
  input.value = ''
}

function cancelImageToolbarSettings(): void {
  imageToolbarSettingsOpen.value = false
  imageToolbarSettingsDraft.value = [...imageToolbarToolIds.value]
}

function toggleImageToolbarDraft(id: ImageToolbarToolId): void {
  const next = new Set(imageToolbarSettingsDraft.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  imageToolbarSettingsDraft.value = imageToolbarToolDefinitions
    .map((tool) => tool.id)
    .filter((toolId): toolId is ImageToolbarToolId => next.has(toolId))
}

function resetImageToolbarDraft(): void {
  imageToolbarSettingsDraft.value = [...defaultImageToolbarToolIds]
}

function saveImageToolbarSettings(): void {
  imageToolbarToolIds.value = [...imageToolbarSettingsDraft.value]
  if (typeof window !== 'undefined') {
    window.localStorage.setItem('canvas-image-toolbar-tools-v1', JSON.stringify(imageToolbarToolIds.value))
  }
  imageToolbarSettingsOpen.value = false
}

function restoreImageToolbarSettings(): void {
  if (typeof window === 'undefined') return
  try {
    const raw = window.localStorage.getItem('canvas-image-toolbar-tools-v1')
    if (!raw) return
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return
    const allowed = new Set<ImageToolbarToolId>(imageToolbarToolDefinitions.map((tool) => tool.id))
    const restored = parsed.filter((id): id is ImageToolbarToolId => typeof id === 'string' && allowed.has(id as ImageToolbarToolId))
    if (restored.length) imageToolbarToolIds.value = restored
  } catch {
    window.localStorage.removeItem('canvas-image-toolbar-tools-v1')
  }
}

function builtinReferenceLabel(reference: CanvasNode): string {
  return reference.pluginTitle || reference.prompt || reference.textContent || reference.model || reference.type
}

function builtinOption(key: string, fallback: string): string {
  return builtinOptions.value[key] || fallback
}

function updateBuiltinOption(key: string, value: string): void {
  updatePluginMetadata({ composerOptions: { ...builtinOptions.value, [key]: value } })
}

function startPanoramaDrag(event: PointerEvent): void {
  if (!panoramaSource.value) return
  panoramaDragging.value = true
  panoramaDragStart.value = { x: event.clientX, y: event.clientY, lon: panoramaLon.value, lat: panoramaLat.value }
  ;(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId)
}

function movePanoramaDrag(event: PointerEvent): void {
  if (!panoramaDragging.value) return
  panoramaLon.value = panoramaDragStart.value.lon - (event.clientX - panoramaDragStart.value.x) * 0.16
  panoramaLat.value = Math.max(0, Math.min(100, panoramaDragStart.value.lat + (event.clientY - panoramaDragStart.value.y) * 0.12))
}

function stopPanoramaDrag(): void {
  panoramaDragging.value = false
}

function zoomPanorama(event: WheelEvent): void {
  panoramaZoom.value = Math.max(0, Math.min(2, panoramaZoom.value + (event.deltaY < 0 ? 0.12 : -0.12)))
}

function handlePanoramaFile(event: Event): void {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file || !file.type.startsWith('image/')) return
  const reader = new FileReader()
  reader.onload = () => updatePluginContent(String(reader.result || ''))
  reader.readAsDataURL(file)
}

onMounted(() => {
  restoreImageToolbarSettings()
  mountRemotePlugin()
  panoramaTimer = window.setInterval(() => {
    if (pluginRenderer.value === 'panorama' && panoramaSource.value && !panoramaDragging.value) panoramaLon.value = (panoramaLon.value + 0.025) % 100
  }, 50)
})

onBeforeUnmount(() => {
  remotePluginMountController?.unmount()
  remotePluginPanelController?.unmount()
  if (panoramaTimer !== null) window.clearInterval(panoramaTimer)
})

watch(() => props.remotePluginDefinition, () => { void nextTick(mountRemotePlugin) })
watch(() => props.pluginContext, (context) => {
  if (!context) return
  remotePluginMountController?.update(context)
  remotePluginPanelController?.update(context)
})
watch(() => props.pluginPanelOpen, (open) => {
  pluginPanelVisible.value = Boolean(open)
  void nextTick(mountRemotePluginPanel)
})

function mountRemotePlugin(): void {
  remotePluginMountController?.unmount()
  remotePluginMountController = null
  if (props.remotePluginDefinition && remotePluginMount.value && props.pluginContext) remotePluginMountController = mountCanvasPluginContent(props.remotePluginDefinition, remotePluginMount.value, props.pluginContext)
  mountRemotePluginPanel()
}

function mountRemotePluginPanel(): void {
  remotePluginPanelController?.unmount()
  remotePluginPanelController = null
  if (props.remotePluginDefinition?.Panel && pluginPanelVisible.value && remotePluginPanelMount.value && props.pluginContext) {
    remotePluginPanelController = mountCanvasPluginPanel(props.remotePluginDefinition, remotePluginPanelMount.value, props.pluginContext, () => {
      pluginPanelVisible.value = false
      remotePluginPanelController?.unmount()
      remotePluginPanelController = null
      props.pluginContext?.closePanel()
      emit('close-plugin-panel')
    })
  }
}
</script>
