<template>
  <component :is="props.embedded ? 'div' : AppLayout">
    <main class="flex min-h-[calc(100vh-7rem)] w-full flex-col gap-3">
      <header class="grid gap-4 rounded-2xl border border-gray-200/70 bg-white/70 p-3 shadow-sm backdrop-blur dark:border-dark-700/70 dark:bg-dark-950/60" data-canvas-header>
        <div class="grid min-w-0 gap-4 2xl:grid-cols-[minmax(0,0.9fr)_minmax(0,1.6fr)] 2xl:items-center">
          <div class="flex min-w-0 flex-col gap-3 2xl:flex-row 2xl:items-center">
            <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
            <div class="relative shrink-0" data-canvas-no-zoom>
              <button type="button" class="grid h-8 w-8 place-items-center rounded-full text-gray-500 transition hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white" :title="t('playground.canvasOpenMenu')" :aria-expanded="canvasMenuOpen" @click="canvasMenuOpen = !canvasMenuOpen; shortcutsOpen = false">
                <Icon name="menu" size="sm" />
                <span class="sr-only">{{ t('playground.canvasOpenMenu') }}</span>
              </button>
              <div v-if="canvasMenuOpen" class="absolute left-0 top-full z-[80] mt-2 w-64 overflow-hidden rounded-xl border border-gray-200 bg-white/98 p-1.5 text-sm shadow-2xl backdrop-blur dark:border-dark-600 dark:bg-dark-900/98" data-canvas-menu @pointerdown.stop>
                <button type="button" class="canvas-menu-item" @click="openCanvasProjectsFromMenu"><Icon name="grid" size="sm" />{{ t('playground.canvasProjects') }}</button>
                <button type="button" class="canvas-menu-item" @click="newProjectFromMenu"><Icon name="plus" size="sm" />{{ t('playground.canvasNewProject') }}</button>
                <button type="button" class="canvas-menu-item" @click="duplicateActiveProjectFromMenu"><Icon name="copy" size="sm" />{{ t('playground.canvasDuplicateProject') }}</button>
                <span class="my-1 block h-px bg-gray-100 dark:bg-dark-700"></span>
                <button type="button" class="canvas-menu-item" @click="importCanvasFromMenu"><Icon name="upload" size="sm" />{{ t('playground.canvasImport') }}</button>
                <button type="button" class="canvas-menu-item" @click="exportCanvasFromMenu"><Icon name="download" size="sm" />{{ t('playground.canvasExport') }}</button>
                <button type="button" class="canvas-menu-item" @click="pluginManagerOpen = true; closeCanvasMenu()"><Icon name="cube" size="sm" />{{ t('playground.canvasPluginsTitle') }}</button>
                <span class="my-1 block h-px bg-gray-100 dark:bg-dark-700"></span>
                <button type="button" class="canvas-menu-item" :disabled="!canvasStore.canUndo.value" @click="canvasStore.undo(); closeCanvasMenu()"><Icon name="undo" size="sm" />{{ t('playground.canvasUndo') }}<kbd class="ml-auto text-[10px] text-gray-400">Ctrl Z</kbd></button>
                <button type="button" class="canvas-menu-item" :disabled="!canvasStore.canRedo.value" @click="canvasStore.redo(); closeCanvasMenu()"><Icon name="redo" size="sm" />{{ t('playground.canvasRedo') }}<kbd class="ml-auto text-[10px] text-gray-400">Ctrl Y</kbd></button>
                <button type="button" class="canvas-menu-item text-red-600 dark:text-red-400" @click="deleteProjectFromMenu"><Icon name="trash" size="sm" />{{ t('playground.canvasDeleteCurrentProject') }}</button>
              </div>
            </div>
            <button type="button" class="grid h-8 w-8 shrink-0 place-items-center rounded-full text-gray-500 transition hover:bg-gray-100 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white" :title="projectsPanelCollapsed ? t('playground.canvasProjectsExpand') : t('playground.canvasProjectsCollapse')" data-canvas-no-zoom @click="toggleProjectsPanel">
              <Icon :name="projectsPanelCollapsed ? 'chevronRight' : 'chevronLeft'" size="sm" />
              <span class="sr-only">{{ projectsPanelCollapsed ? t('playground.canvasProjectsExpand') : t('playground.canvasProjectsCollapse') }}</span>
            </button>
            <div class="min-w-0 flex-1">
              <input v-if="titleEditing" v-model="titleDraft" class="input h-8 w-full max-w-md text-lg font-semibold" :aria-label="t('playground.canvasProjectName')" @keydown.enter="commitProjectTitle" @keydown.esc="cancelProjectTitle" @blur="commitProjectTitle">
              <button v-else type="button" class="block max-w-full truncate text-left text-lg font-semibold text-gray-900 hover:text-teal-700 dark:text-white dark:hover:text-teal-200" :title="t('playground.canvasRenameProject')" @click="startProjectTitleEdit">{{ activeProject?.title || t('playground.canvasTitle') }}</button>
              <p class="truncate text-xs text-gray-500 dark:text-dark-400">{{ activeProject?.title }} · {{ t('playground.canvasDescription') }}</p>
            </div>
            </div>
            <nav class="flex max-w-full shrink-0 flex-wrap items-center gap-1 rounded-2xl bg-gray-100 p-1 text-xs dark:bg-dark-800" :aria-label="t('playground.workspaceLabel')" data-canvas-workspace-tabs>
            <RouterLink to="/playground/chat" class="whitespace-nowrap rounded-xl px-3 py-1.5 font-medium text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.chatWorkspace') }}</RouterLink>
            <RouterLink to="/playground/images" class="whitespace-nowrap rounded-xl px-3 py-1.5 font-medium transition-colors" :class="!isCanvasView ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'">{{ t('playground.imageWorkspace') }}</RouterLink>
            <RouterLink to="/playground/images?view=canvas" class="whitespace-nowrap rounded-xl px-3 py-1.5 font-medium transition-colors" :class="isCanvasView ? 'bg-white text-teal-700 shadow-sm dark:bg-dark-700 dark:text-teal-200' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'">{{ t('playground.canvasWorkspace') }}</RouterLink>
            <RouterLink to="/playground/gallery" class="whitespace-nowrap rounded-xl px-3 py-1.5 font-medium text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.galleryWorkspace') }}</RouterLink>
            </nav>
          </div>
          <div class="flex min-w-0 flex-col gap-2" data-canvas-model-controls>
            <div class="grid gap-2 lg:grid-cols-2 2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
              <div class="flex min-w-0 flex-wrap items-center gap-2 rounded-lg border border-gray-200 bg-white/80 px-2 py-1 dark:border-dark-700 dark:bg-dark-900/80" data-canvas-no-zoom>
            <span class="shrink-0 text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.imageWorkspace') }}</span>
            <select v-model="selectedKeyId" class="select h-8 min-w-0 flex-1 text-xs" :aria-label="t('playground.keyLabel')">
              <option v-for="key in playgroundKeys" :key="key.id" :value="key.id">{{ playgroundKeyLabel(key) }}</option>
            </select>
            <select v-model="selectedModel" class="select h-8 min-w-0 flex-1 text-xs" :disabled="loadingModels || models.length === 0" :aria-label="t('playground.modelLabel')">
              <option value="" disabled>{{ loadingModels ? t('playground.loadingModels') : t('playground.noModels') }}</option>
              <option v-for="model in models" :key="model.id" :value="model.id">{{ model.id }}</option>
            </select>
              </div>
              <div class="flex min-w-0 flex-wrap items-center gap-2 rounded-lg border border-gray-200 bg-white/80 px-2 py-1 dark:border-dark-700 dark:bg-dark-900/80" data-canvas-no-zoom>
            <span class="shrink-0 text-[11px] font-medium text-gray-500 dark:text-dark-400">{{ t('playground.chatWorkspace') }}</span>
            <select v-model="selectedTextKeyId" class="select h-8 min-w-0 flex-1 text-xs" :aria-label="t('playground.keyLabel')">
              <option v-for="key in playgroundKeys" :key="key.id" :value="key.id">{{ playgroundKeyLabel(key) }}</option>
            </select>
            <select v-model="selectedTextModel" class="select h-8 min-w-0 flex-1 text-xs" :disabled="loadingTextModels || textModels.length === 0" :aria-label="t('playground.modelLabel')">
              <option value="" disabled>{{ loadingTextModels ? t('playground.loadingModels') : t('playground.noModels') }}</option>
              <option v-for="model in textModels" :key="model.id" :value="model.id">{{ model.id }}</option>
            </select>
              </div>
              <button type="button" class="btn btn-ghost h-10 w-full justify-center px-3 lg:col-span-2 2xl:col-span-1 2xl:w-10 2xl:p-0" :title="t('playground.refreshModels')" :disabled="(loadingModels && loadingTextModels) || (!selectedKeyId && !selectedTextKeyId)" data-canvas-no-zoom @click="refreshModels">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingModels || loadingTextModels }" />
              <span class="sr-only 2xl:not-sr-only 2xl:hidden">{{ t('playground.refreshModels') }}</span>
              </button>
            </div>
          </div>
        </div>
      </header>

      <section class="relative flex min-h-[620px] min-w-0 flex-1 overflow-hidden rounded-lg border border-gray-200 bg-gray-50 shadow-sm dark:border-dark-700 dark:bg-dark-950">
        <button
          type="button"
          class="absolute left-4 top-4 z-30 hidden h-8 w-8 place-items-center rounded-full border border-gray-200 bg-white/90 text-gray-500 shadow-sm backdrop-blur transition hover:text-gray-800 dark:border-dark-600 dark:bg-dark-900/90 dark:text-dark-300 dark:hover:text-white lg:grid"
          :title="projectsPanelCollapsed ? t('playground.canvasProjectsExpand') : t('playground.canvasProjectsCollapse')"
          @click="toggleProjectsPanel"
        >
          <Icon :name="projectsPanelCollapsed ? 'chevronRight' : 'chevronLeft'" size="sm" />
          <span class="sr-only">{{ projectsPanelCollapsed ? t('playground.canvasProjectsExpand') : t('playground.canvasProjectsCollapse') }}</span>
        </button>
        <aside v-if="!projectsPanelCollapsed" class="relative z-30 hidden shrink-0 flex-col overflow-y-auto border-r border-gray-200 bg-white/95 p-3 dark:border-dark-700 dark:bg-dark-900/95 lg:flex" :style="{ width: `${canvasPanelWidth}px` }" data-canvas-no-zoom>
          <div class="mb-3 flex items-center justify-between gap-2">
            <h2 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">{{ t('playground.canvasProjects') }}</h2>
            <div class="flex items-center gap-1">
              <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasNewProject')" @click="newProject">
                <Icon name="plus" size="xs" />
                <span class="sr-only">{{ t('playground.canvasNewProject') }}</span>
              </button>
              <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.canvasDuplicateProject')" @click="duplicateActiveProject">
                <Icon name="copy" size="xs" />
                <span class="sr-only">{{ t('playground.canvasDuplicateProject') }}</span>
              </button>
            </div>
          </div>
          <div class="space-y-1">
            <div v-for="project in canvasStore.state.projects" :key="project.id" class="group flex w-full items-center justify-between gap-2 rounded px-2.5 py-2 text-left text-xs transition-colors" :class="project.id === canvasStore.state.activeProjectId ? 'bg-teal-50 font-medium text-teal-800 dark:bg-teal-900/30 dark:text-teal-200' : 'text-gray-600 hover:bg-gray-50 dark:text-dark-300 dark:hover:bg-dark-800'">
              <button type="button" class="min-w-0 flex-1 truncate text-left" @click="canvasStore.state.activeProjectId = project.id">{{ project.title }}</button>
              <span class="flex shrink-0 items-center gap-1 text-[10px] text-gray-400">
                <span>{{ project.nodes.length }}</span>
                <button type="button" class="hidden h-5 w-5 items-center justify-center rounded text-gray-400 hover:bg-white hover:text-gray-700 group-hover:flex dark:hover:bg-dark-700 dark:hover:text-gray-100" :title="t('common.renameProject')" @click.stop="renameProject(project.id)"><Icon name="edit" size="xs" /><span class="sr-only">{{ t('common.renameProject') }}</span></button>
                <button type="button" class="hidden h-5 w-5 items-center justify-center rounded text-gray-400 hover:bg-white hover:text-red-600 group-hover:flex dark:hover:bg-dark-700" :title="t('common.deleteProject')" @click.stop="deleteProject(project.id)"><Icon name="trash" size="xs" /><span class="sr-only">{{ t('common.deleteProject') }}</span></button>
              </span>
            </div>
          </div>
          <div class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700">
            <nav class="flex items-center gap-4 border-b border-gray-100 dark:border-dark-700" aria-label="Canvas side panel tabs">
              <button v-for="tab in canvasPanelTabs" :key="tab.value" type="button" class="relative pb-2 text-[11px] font-semibold transition-colors" :class="canvasPanelTab === tab.value ? 'text-teal-700 dark:text-teal-200' : 'text-gray-400 hover:text-gray-700 dark:text-dark-500 dark:hover:text-dark-200'" @click="canvasPanelTab = tab.value">
                {{ tab.label }}
                <span v-if="canvasPanelTab === tab.value" class="absolute inset-x-0 -bottom-px h-0.5 rounded-full bg-teal-500"></span>
              </button>
            </nav>
          </div>
          <div v-if="canvasPanelTab === 'canvas'" class="min-h-0 flex-1 pt-3">
            <div class="mb-2 flex items-center justify-between gap-2">
              <div class="flex min-w-0 items-center gap-2">
                <h3 class="truncate text-[11px] font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">{{ t('playground.canvasElements') }}</h3>
                <span class="text-[10px] tabular-nums text-gray-400 dark:text-dark-500">{{ canvasNodeRows.length }}</span>
              </div>
              <div class="flex items-center gap-1">
                <button type="button" class="btn btn-ghost h-6 px-1.5 text-[10px]" :class="{ 'text-teal-700 dark:text-teal-200': canvasElementSelectMode }" :title="canvasElementSelectMode ? t('common.cancel') : t('playground.canvasSelectElements')" @click="toggleCanvasElementSelectMode">
                  <Icon name="check" size="xs" />
                  {{ canvasElementSelectMode ? t('common.cancel') : t('playground.canvasSelectElements') }}
                </button>
                <button v-if="canvasElementSelectMode" type="button" class="btn btn-ghost btn-icon h-6 w-6 p-0" :disabled="!canvasElementSelection.size" :title="t('playground.canvasExportSelection')" @click="exportSelectedCanvasElements">
                  <Icon name="download" size="xs" />
                  <span class="sr-only">{{ t('playground.canvasExportSelection') }}</span>
                </button>
              </div>
            </div>
            <div class="mb-2 flex gap-1.5">
              <label class="relative min-w-0 flex-1">
                <Icon name="search" size="xs" class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-400" />
                <input v-model="canvasNodeSearch" type="search" class="input h-7 w-full pl-7 text-[11px]" :placeholder="t('playground.canvasSearchNodes')" />
              </label>
              <select v-model="canvasNodeFilter" class="select h-7 w-20 shrink-0 text-[10px]" :aria-label="t('playground.canvasFilterNodes')">
                <option v-for="filter in canvasNodeFilters" :key="filter.value" :value="filter.value">{{ filter.label }}</option>
              </select>
            </div>
            <div v-if="canvasNodeRows.length" class="space-y-1">
              <div
                v-for="row in canvasNodeRows"
                :key="row.node.id"
                class="group flex min-w-0 items-center gap-1 rounded-lg transition-colors"
                :class="(canvasElementSelectMode ? canvasElementSelection.has(row.node.id) : selectedNodeIds.has(row.node.id)) ? 'bg-teal-50 text-teal-800 dark:bg-teal-900/30 dark:text-teal-200' : 'text-gray-600 hover:bg-gray-50 dark:text-dark-300 dark:hover:bg-dark-800'"
                :style="{ marginLeft: `${row.depth * 12}px` }"
              >
                <button
                  v-if="row.hasChildren"
                  type="button"
                  class="grid h-6 w-5 shrink-0 place-items-center rounded text-gray-400 hover:bg-white hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-100"
                  :title="collapsedCanvasGroups.has(row.node.id) ? t('playground.canvasExpandGroup') : t('playground.canvasCollapseGroup')"
                  @click="toggleCanvasGroupCollapse(row.node.id)"
                >
                  <Icon :name="collapsedCanvasGroups.has(row.node.id) ? 'chevronRight' : 'chevronDown'" size="xs" />
                  <span class="sr-only">{{ collapsedCanvasGroups.has(row.node.id) ? t('playground.canvasExpandGroup') : t('playground.canvasCollapseGroup') }}</span>
                </button>
                <span v-else class="w-5 shrink-0"></span>
                <button type="button" class="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1.5 py-1.5 text-left" :title="canvasElementSelectMode ? t('playground.canvasSelectElements') : t('playground.canvasFocusNode')" @click="canvasElementSelectMode ? toggleCanvasElementSelection(row.node.id) : focusCanvasNode(row.node.id)">
                  <span class="grid h-6 w-6 shrink-0 place-items-center rounded-md bg-gray-100 text-gray-500 dark:bg-dark-800 dark:text-dark-300">
                    <Icon :name="canvasElementSelection.has(row.node.id) && canvasElementSelectMode ? 'check' : canvasNodeIcon(row.node)" size="xs" />
                  </span>
                  <span class="min-w-0">
                    <span class="block truncate text-[11px] font-medium">{{ canvasNodeLabel(row.node) }}</span>
                    <span class="block truncate text-[10px] text-gray-400 dark:text-dark-500">{{ canvasNodePreview(row.node) }}</span>
                  </span>
                </button>
              </div>
            </div>
            <p v-else class="rounded-lg border border-dashed border-gray-200 px-2.5 py-3 text-center text-[11px] text-gray-400 dark:border-dark-700 dark:text-dark-500">{{ canvasNodeSearch || canvasNodeFilter !== 'all' ? t('playground.canvasNoMatchingNodes') : t('playground.canvasElementsEmpty') }}</p>
          </div>
          <div v-else-if="canvasPanelTab === 'assets'" class="min-h-0 flex-1 pt-3">
            <div class="mb-2 flex gap-1.5">
              <label class="relative min-w-0 flex-1">
                <Icon name="search" size="xs" class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-400" />
                <input v-model="canvasAssetSearch" type="search" class="input h-7 w-full pl-7 text-[11px]" :placeholder="t('playground.canvasSearchAssets')" />
              </label>
              <button type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0" :title="t('playground.canvasOpenAssets')" @click="openAssetPicker"><Icon name="externalLink" size="xs" /><span class="sr-only">{{ t('playground.canvasOpenAssets') }}</span></button>
            </div>
            <div v-if="filteredCanvasAssets.length" class="grid grid-cols-2 gap-2">
              <div v-for="asset in filteredCanvasAssets" :key="asset.key" class="relative overflow-hidden rounded-lg border border-gray-200 bg-gray-50 text-left transition hover:border-teal-400 hover:shadow-sm dark:border-dark-700 dark:bg-dark-950">
                <button type="button" class="group block w-full text-left" :title="asset.title" @click="insertCanvasAsset(asset)">
                  <div class="relative aspect-square overflow-hidden bg-gray-100 dark:bg-dark-800">
                    <img v-if="asset.kind === 'image' && asset.url" :src="asset.url" :alt="asset.title" class="h-full w-full object-cover transition-transform group-hover:scale-[1.03]">
                    <video v-else-if="asset.kind === 'video' && asset.url" :src="asset.url" muted preload="metadata" class="h-full w-full object-cover"></video>
                    <audio v-else-if="asset.kind === 'audio' && asset.url" :src="asset.url" controls class="absolute inset-x-1 bottom-2 w-[calc(100%-0.5rem)]" @pointerdown.stop @click.stop></audio>
                    <div v-else class="flex h-full items-center justify-center p-2 text-[11px] leading-5 text-gray-600 dark:text-dark-300">{{ asset.text || asset.title }}</div>
                    <span class="absolute left-1.5 top-1.5 rounded bg-gray-950/65 px-1.5 py-0.5 text-[9px] font-semibold tracking-wide text-white">{{ canvasAssetKindLabel(asset.kind) }}</span>
                  </div>
                  <span class="block truncate px-1.5 py-1 text-[10px] text-gray-600 dark:text-dark-300">{{ asset.title }}</span>
                </button>
                <button v-if="asset.source === 'saved'" type="button" class="absolute right-1.5 top-1.5 grid h-5 w-5 place-items-center rounded bg-gray-950/65 text-white hover:bg-red-600" :title="t('playground.canvasRemoveSavedAsset')" @click.stop="removeSavedCanvasAsset(asset.savedAssetId || '')"><Icon name="trash" size="xs" /><span class="sr-only">{{ t('playground.canvasRemoveSavedAsset') }}</span></button>
              </div>
            </div>
            <div v-else class="rounded-xl border border-dashed border-gray-200 bg-gray-50/70 p-3 text-center dark:border-dark-700 dark:bg-dark-950/40">
              <Icon name="inbox" size="sm" class="mx-auto text-gray-400" />
              <p class="mt-2 text-[11px] leading-5 text-gray-500 dark:text-dark-400">{{ canvasAssetSearch ? t('playground.canvasNoMatchingAssets') : t('playground.canvasAssetsDescription') }}</p>
              <button type="button" class="btn btn-secondary mt-3 h-8 w-full gap-1.5 text-[11px]" @click="openAssetPicker"><Icon name="inbox" size="xs" />{{ t('playground.canvasOpenAssets') }}</button>
            </div>
          </div>
          <div v-else class="min-h-0 flex-1 pt-3">
            <div class="mb-2 flex gap-1.5">
              <label class="relative min-w-0 flex-1">
                <Icon name="search" size="xs" class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-400" />
                <input v-model="canvasPromptSearch" type="search" class="input h-7 w-full pl-7 text-[11px]" :placeholder="t('playground.canvasSearchPrompts')" />
              </label>
              <button type="button" class="btn btn-ghost btn-icon h-7 w-7 shrink-0 p-0" :title="t('playground.canvasOpenPrompts')" @click="openPromptLibrary"><Icon name="externalLink" size="xs" /><span class="sr-only">{{ t('playground.canvasOpenPrompts') }}</span></button>
            </div>
            <div v-if="filteredCanvasPrompts.length" class="space-y-1.5 overflow-y-auto">
              <article v-for="prompt in filteredCanvasPrompts" :key="prompt.id" class="rounded-lg border border-gray-200 bg-gray-50/70 p-2 dark:border-dark-700 dark:bg-dark-950/40">
                <div class="flex items-start gap-2">
                  <button type="button" class="min-w-0 flex-1 text-left" :title="prompt.content" @click="insertPromptFromLibrary(prompt.content, false)">
                    <span class="block truncate text-[11px] font-semibold text-gray-700 dark:text-gray-200">{{ prompt.title }}</span>
                    <span class="mt-1 block line-clamp-2 text-[10px] leading-4 text-gray-500 dark:text-dark-400">{{ prompt.content }}</span>
                  </button>
                  <button type="button" class="btn btn-ghost btn-icon h-6 w-6 shrink-0 p-0 text-teal-700 dark:text-teal-300" :title="t('playground.canvasPromptInsert')" @click="insertPromptFromLibrary(prompt.content, false)"><Icon name="plus" size="xs" /><span class="sr-only">{{ t('playground.canvasPromptInsert') }}</span></button>
                </div>
              </article>
            </div>
            <div v-else class="rounded-xl border border-dashed border-gray-200 bg-gray-50/70 p-3 text-center dark:border-dark-700 dark:bg-dark-950/40">
              <Icon name="lightbulb" size="sm" class="mx-auto text-gray-400" />
              <p class="mt-2 text-[11px] leading-5 text-gray-500 dark:text-dark-400">{{ canvasPromptSearch ? t('playground.canvasNoMatchingPrompts') : t('playground.canvasPromptLibraryDescription') }}</p>
              <button type="button" class="btn btn-secondary mt-3 h-8 w-full gap-1.5 text-[11px]" @click="openPromptLibrary"><Icon name="lightbulb" size="xs" />{{ t('playground.canvasOpenPrompts') }}</button>
            </div>
          </div>
          <button type="button" class="absolute inset-y-0 -right-2 z-40 hidden w-4 cursor-col-resize lg:block" :title="t('playground.canvasResizePanel')" @pointerdown="startCanvasPanelResize"></button>
          <div class="mt-6 border-t border-gray-100 pt-4 text-[11px] leading-5 text-gray-400 dark:border-dark-700 dark:text-dark-500">
            <p>{{ t('playground.canvasTipPan') }}</p>
            <p>{{ t('playground.canvasTipZoom') }}</p>
            <p>{{ t('playground.canvasTipAdd') }}</p>
          </div>
        </aside>

        <div ref="canvasRef" class="relative min-w-0 flex-1 select-none overflow-hidden" :class="canvasCursor" :style="canvasSurfaceStyle" data-canvas-surface @pointerdown.capture="handleCanvasPointerCapture" @pointerdown="handleCanvasPointerDown" @wheel.prevent="handleWheel" @dblclick="handleCanvasDoubleClick" @dragover.prevent @drop.prevent="handleCanvasDrop" @contextmenu.prevent>
          <CanvasToolbar
            v-show="!pluginPanelNodeId"
            :tool="activeCanvasTool"
            :background-mode="activeProject?.backgroundMode ?? 'dots'"
            :can-undo="canvasStore.canUndo.value"
            :can-redo="canvasStore.canRedo.value"
            :selected-count="selectedNodeIds.size"
            :node-count="activeProject?.nodes.length ?? 0"
            @update:tool="setCanvasTool"
            @update:background="updateCanvasBackgroundMode"
            @add-node="addNodeAtCenter"
            @fit="fitCanvas"
            @reset-view="resetCanvasView"
            @import="importCanvas"
            @export="exportCanvas"
            @sync="webdavOpen = true"
            @undo="canvasStore.undo()"
            @redo="canvasStore.redo()"
            @assets="openAssetPicker"
            @assistant="assistantOpen = true"
            @agent="agentOpen = true"
            @plugins="pluginManagerOpen = true"
            @prompts="openPromptLibrary"
            @clear-selection="clearSelectedNodes"
            @clear-canvas="clearCanvas"
          />
          <div class="absolute right-4 top-4 z-20 flex items-center gap-2 rounded-xl border border-gray-200 bg-white/90 px-2 py-1 text-[11px] text-gray-500 shadow-sm backdrop-blur dark:border-dark-600 dark:bg-dark-900/90 dark:text-dark-300" data-canvas-no-zoom data-canvas-zoom-slider>
            <button type="button" class="btn btn-ghost btn-icon h-6 w-6 p-0" :title="t('playground.canvasZoomOut')" @click="zoomBy(0.85)">
              <Icon name="minus" size="xs" />
              <span class="sr-only">{{ t('playground.canvasZoomOut') }}</span>
            </button>
            <label class="sr-only" for="canvas-zoom-slider">{{ t('playground.canvasZoomSlider') }}</label>
            <input id="canvas-zoom-slider" class="h-1.5 w-24 accent-teal-600" type="range" min="20" max="300" step="5" :value="Math.round((activeProject?.viewport.scale ?? 1) * 100)" :aria-label="t('playground.canvasZoomSlider')" @input="setZoomPercent(Number(($event.target as HTMLInputElement).value))">
            <span class="w-10 text-center tabular-nums">{{ Math.round((activeProject?.viewport.scale ?? 1) * 100) }}%</span>
            <button type="button" class="btn btn-ghost btn-icon h-6 w-6 p-0" :title="t('playground.canvasZoomIn')" @click="zoomBy(1.15)">
              <Icon name="plus" size="xs" />
              <span class="sr-only">{{ t('playground.canvasZoomIn') }}</span>
            </button>
          </div>
          <button type="button" class="absolute right-4 top-16 z-20 grid h-9 w-9 place-items-center rounded-full border border-gray-200 bg-white/90 text-gray-500 shadow-sm backdrop-blur transition hover:text-teal-700 dark:border-dark-600 dark:bg-dark-900/90 dark:text-dark-300 dark:hover:text-teal-200" :title="t('playground.canvasShortcuts')" data-canvas-no-zoom @click="shortcutsOpen = true; canvasMenuOpen = false">
            <Icon name="questionCircle" size="sm" />
            <span class="sr-only">{{ t('playground.canvasShortcuts') }}</span>
          </button>

          <div v-if="nodeCreatePosition" class="absolute z-50 w-64 rounded-2xl border border-gray-200 bg-white/95 p-2 shadow-2xl backdrop-blur dark:border-dark-600 dark:bg-dark-900/95" :style="{ left: `${nodeCreatePosition.x}px`, top: `${nodeCreatePosition.y}px` }" data-canvas-create-menu data-canvas-no-zoom @pointerdown.stop>
            <div class="flex items-center justify-between px-2 py-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">
              <span>{{ pendingConnectionSource ? t('playground.canvasCreateConnectedTitle') : t('playground.canvasCreateMenuTitle') }}</span>
              <button type="button" class="btn btn-ghost btn-icon h-6 w-6 p-0" :title="t('common.close')" @click="nodeCreatePosition = null; pendingConnectionSource = null"><Icon name="x" size="xs" /><span class="sr-only">{{ t('common.close') }}</span></button>
            </div>
            <div class="grid gap-1">
              <button v-for="option in nodeCreateOptions" :key="option.type" type="button" class="flex items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-teal-50 dark:hover:bg-teal-900/30" :data-node-create-type="option.type" @click="createNodeFromMenu(option.type)">
                <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-200"><Icon :name="option.icon" size="sm" /></span>
                <span class="min-w-0"><span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ option.label }}</span><span class="block truncate text-[11px] text-gray-400">{{ option.description }}</span></span>
              </button>
            </div>
          </div>
          <div v-if="selectionBox" class="pointer-events-none absolute z-30 border-2 border-dashed border-teal-500 bg-teal-400/10" :style="selectionBoxStyle" aria-hidden="true"></div>

          <div class="absolute left-0 top-0 origin-top-left" :style="worldStyle">
            <CanvasConnections :connections="activeProject?.connections ?? []" :nodes="activeProject?.nodes ?? []" :draft-path="draftPath" :selected-connection-id="selectedConnectionId" :active-connection-ids="relatedCanvasConnectionIds" @select="selectConnection" @delete="deleteConnection" @context-menu="openConnectionContextMenu" />
          <CanvasNode
              v-for="node in visibleCanvasNodes"
              :key="node.id"
              :node="node"
              :selected="selectedNodeIds.has(node.id)"
              :connection-target="connectionTargetNodeId === node.id"
              :connecting="connectingFrom === node.id"
              :related="relatedCanvasNodeIds.has(node.id)"
              :focus-related="activeRelatedNodeId === node.id"
              :reference-selection-state="canvasReferenceSelectionState(node)"
              :downloading="downloadingNodeId === node.id"
              :reverse-prompt-busy="reversePromptIds.has(node.id)"
              :upscale-busy="upscaleNodeIds.has(node.id)"
              :transcribing="transcribingNodeIds.has(node.id)"
              :remote-plugin-definition="getCanvasPluginDefinition(node.type)"
              :plugin-context="createCanvasPluginContext(node)"
              :plugin-panel-open="pluginPanelNodeId === node.id"
              :plugin-generation-busy="pluginGenerationBusy === node.id"
              :config-busy="configBusyNodeIds.has(node.id)"
              :plugin-reference-selecting="pluginReferenceTargetId === node.id"
              :mention-references="canvasMentionReferencesByNodeId[node.id] ?? []"
              :incoming-references="canvasReferenceItemsByNodeId[node.id] ?? []"
              :has-inherited-prompt="canvasNodeHasPromptInput(node)"
              :image-models="models"
              :text-models="textModels"
              :video-models="videoModels"
              :group-child-count="groupChildCount(node.id)"
              :is-group-drop-target="dropTargetGroupId === node.id"
              @select="selectNode"
              @select-reference="selectCanvasReference"
              @hover-start="hoveredNodeId = $event"
              @hover-end="hoveredNodeId = hoveredNodeId === $event ? null : hoveredNodeId"
              @delete="deleteNode"
              @generate="generateNode"
              @generate-text="generateTextNode"
              @generate-image-from-text="generateImageFromText"
              @cancel-generation="cancelNodeGeneration"
              @select-text-variant="selectTextVariant"
              @select-image="selectImageVariant"
              @retry-image="retryImageVariant"
              @duplicate-image="duplicateImageVariant"
              @delete-image="deleteImageVariant"
              @generate-video="generateVideoNode"
              @cancel-video="cancelVideoNode"
               @generate-audio="generateAudioNode"
               @cancel-audio="cancelAudioNode"
               @retry-generation="retryCanvasGeneration"
               @transcribe-audio="transcribeAudioNode"
              @download="downloadNode"
              @use-reference="useResultAsReference"
              @edit-image="openImageEditor"
              @transform-image="quickTransformImage"
              @reverse-prompt="reversePromptImage"
              @split-image="openImageSplit"
              @upscale-image="openImageUpscale"
               @view-image="openImageViewer"
               @mask-image="openImageMask"
              @angle-image="openImageAngle"
               @toggle-free-resize="toggleFreeResize"
               @capture-video-frame="captureVideoFrame"
               @edit-video="openVideoEdit"
               @save-asset="saveNodeAsCanvasAsset"
               @upload-image="uploadImageToNode"
               @upload-video="uploadVideoToNode"
               @upload-audio="uploadAudioToNode"
               @show-info="openCanvasNodeInfo"
               @connect-start="startConnection"
              @update-prompt="updateNodePrompt"
              @update-title="updateNodeTitle"
              @update-text-size="updateTextNodeSize"
              @update-plugin="updateNodePlugin"
              @update-plugin-composer="updatePluginComposer"
              @update-plugin-model="updatePluginModel"
              @generate-plugin="generateBuiltinPluginNode"
              @cancel-plugin-generation="cancelBuiltinPluginGeneration"
              @start-plugin-reference-selection="startPluginReferenceSelection"
              @disconnect-plugin-reference="disconnectPluginReference"
              @start-reference-selection="startPluginReferenceSelection(node.id)"
              @disconnect-reference="disconnectPluginReference"
              @focus-reference="focusCanvasNode"
              @close-plugin-panel="pluginPanelNodeId = null"
              @double-click="handleCanvasNodeDoubleClick"
              @update-config="updateNodeConfig"
              @generate-config="generateConfigNode"
              @resize-start="startNodeResize"
              @context-menu="openNodeContextMenu"
            />
          </div>

          <div v-if="!activeProject?.nodes.length" class="pointer-events-none absolute inset-0 flex items-center justify-center px-6 text-center">
            <div class="max-w-sm">
              <span class="mx-auto flex h-12 w-12 items-center justify-center rounded-lg border border-dashed border-teal-300 bg-teal-50 text-teal-700 dark:border-teal-800 dark:bg-teal-950/40 dark:text-teal-300">
                <Icon name="sparkles" size="lg" />
              </span>
              <h2 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasEmptyTitle') }}</h2>
              <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('playground.canvasEmptyDescription') }}</p>
              <button type="button" class="pointer-events-auto btn btn-primary mt-4 gap-1.5" @click="addNodeAtCenter()">
                <Icon name="plus" size="sm" />
                {{ t('playground.canvasAddNode') }}
              </button>
            </div>
          </div>
          <CanvasMiniMap :nodes="activeProject?.nodes ?? []" :viewport="activeProject?.viewport ?? { x: 0, y: 0, scale: 1 }" @update-viewport="updateCanvasViewportFromMiniMap" />
          <CanvasContextMenu v-if="contextMenu && contextMenuNode" :x="contextMenu.x" :y="contextMenu.y" :node="contextMenuNode" @duplicate="duplicateContextNode" @copy="copyContextNode" @front="bringContextNodeFront" @back="sendContextNodeBack" @download="downloadNode(contextMenu.nodeId); contextMenu = null" @reference="useResultAsReference(contextMenu.nodeId); contextMenu = null" @delete="deleteNode(contextMenu.nodeId); contextMenu = null" />
          <CanvasConnectionContextMenu v-if="connectionContextMenu" :x="connectionContextMenu.x" :y="connectionContextMenu.y" @delete="deleteConnectionFromContextMenu" @close="connectionContextMenu = null" />
          <CanvasAssistant v-if="assistantOpen" :messages="activeProject?.assistantMessages ?? []" :context-count="assistantContextNodes.length" :references="assistantMentionReferences" :models="textModels" :model="assistantModel" :loading="assistantLoading" @update:model="assistantModel = $event" @send="sendAssistantMessage" @cancel="cancelAssistant" @insert="insertAssistantMessage" @activate="focusCanvasNode" @clear="clearAssistantMessages" @close="assistantOpen = false" />
          <CanvasAgentDialog v-if="agentOpen" :endpoint="agentEndpoint" :token="agentToken" :connected="agentConnected" :busy="agentBusy" :sending="agentSending" :conversation-status="agentConversation.status" :active-thread-id="agentConversation.threadId" :threads="agentThreads" :skills="agentSkills" :approval="agentApproval" :messages="agentMessages" :error-message="agentError" @connect="connectCanvasAgent" @disconnect="disconnectCanvasAgent" @sync="publishAgentState" @send="sendCanvasAgentTurn" @cancel="cancelCanvasAgentTurn" @resume="resumeCanvasAgentThread" @approve="resolveCanvasAgentApproval" @toggle-skill="toggleCanvasAgentSkill" @close="agentOpen = false" />
          <CanvasPluginManagerDialog v-if="pluginManagerOpen" :plugins="plugins" :official-plugins="officialPlugins" :busy="pluginBusy" :error-message="pluginError" @install="installCanvasPlugin" @toggle="toggleCanvasPlugin" @uninstall="uninstallCanvasPluginEntry" @close="pluginManagerOpen = false" />
          <CanvasPromptLibrary v-if="promptLibraryOpen" :prompts="promptLibraryEntries" :refreshing="promptRegistryLoading" @close="promptLibraryOpen = false" @insert="insertPromptFromLibrary" @add="addCustomPrompt" @remove="removeCustomPrompt" @refresh="refreshPromptRegistry" />
          <CanvasWebDavDialog v-if="webdavOpen" :config="webdavConfig" :busy="webdavBusy" @close="webdavOpen = false" @submit="handleWebdav" />
          <CanvasImageEditor v-if="imageEditorOpen && imageEditorUrl" :image-url="imageEditorUrl" :busy="imageEditorBusy" @close="closeImageEditor" @apply="applyImageTransform" />
          <CanvasImageSplitDialog v-if="splitDialogOpen && splitDialogUrl" :image-url="splitDialogUrl" :busy="splitDialogBusy" @close="closeSplitDialog" @apply="applyImageSplit" />
          <CanvasImageUpscaleDialog v-if="upscaleDialogOpen && upscaleDialogUrl" :image-url="upscaleDialogUrl" :busy="upscaleDialogBusy" @close="closeUpscaleDialog" @apply="applyImageUpscale" />
          <CanvasImageViewer v-if="imageViewerOpen && imageViewerUrl" :image-url="imageViewerUrl" :title="imageViewerTitle" @close="closeImageViewer" />
          <CanvasImageMaskDialog v-if="maskDialogOpen && maskDialogUrl" :image-url="maskDialogUrl" :busy="maskDialogBusy" @close="closeMaskDialog" @apply="applyImageMask" />
           <CanvasImageAngleDialog v-if="angleDialogOpen && angleDialogUrl" :image-url="angleDialogUrl" :busy="angleDialogBusy" @close="closeAngleDialog" @apply="applyImageAngle" />
           <CanvasNodeInfoDialog :node="canvasInfoNode" :visible="Boolean(canvasInfoNodeId)" @close="canvasInfoNodeId = null" />
          <div v-if="shortcutsOpen" class="absolute inset-0 z-[90] flex items-center justify-center bg-gray-950/30 p-4 backdrop-blur-sm" data-canvas-no-zoom @pointerdown.self="shortcutsOpen = false">
            <section class="w-full max-w-xl overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-dark-600 dark:bg-dark-900">
              <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
                <div>
                  <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasShortcuts') }}</h3>
                  <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('playground.canvasShortcutsHint') }}</p>
                </div>
                <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="shortcutsOpen = false"><Icon name="x" size="sm" /><span class="sr-only">{{ t('common.close') }}</span></button>
              </div>
              <div class="grid gap-2 p-4 sm:grid-cols-2">
                <div v-for="item in canvasShortcuts" :key="item.key" class="rounded-xl border border-gray-100 bg-gray-50/80 px-3 py-2.5 dark:border-dark-700 dark:bg-dark-800/70">
                  <kbd class="inline-flex rounded-md bg-white px-2 py-1 text-[11px] font-semibold text-teal-700 shadow-sm ring-1 ring-gray-200 dark:bg-dark-900 dark:text-teal-200 dark:ring-dark-600">{{ item.key }}</kbd>
                  <p class="mt-2 text-xs leading-5 text-gray-600 dark:text-dark-300">{{ item.label }}</p>
                </div>
              </div>
            </section>
          </div>

          <button
            v-if="pluginReferenceTargetId"
            type="button"
            class="absolute left-1/2 top-4 z-[90] -translate-x-1/2 rounded-full border border-teal-200 bg-white/95 px-4 py-2 text-xs font-medium text-teal-800 shadow-lg backdrop-blur dark:border-teal-800 dark:bg-dark-900/95 dark:text-teal-100"
            data-canvas-reference-selection-hint
            data-canvas-no-zoom
            @click="cancelPluginReferenceSelection"
          >
            {{ t('playground.canvasReferenceSelectionHint') }}
          </button>
        </div>

        <aside v-if="selectedNode && !selectedNode.pluginHidePanel" class="z-30 w-72 shrink-0 overflow-y-auto border-l border-gray-200 bg-white/95 p-4 dark:border-dark-700 dark:bg-dark-900/95" data-canvas-no-zoom>
          <div class="mb-4 flex items-center justify-between gap-2">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.canvasNodeSettings') }}</h2>
            <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('common.close')" @click="selectedNodeId = null">
              <Icon name="x" size="xs" />
              <span class="sr-only">{{ t('common.close') }}</span>
            </button>
          </div>
          <label v-if="selectedNode.type !== 'config' && !selectedNode.pluginUseBuiltinPanel" class="mb-1.5 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-node-prompt">{{ selectedNode.type === 'text' ? t('playground.canvasTextContent') : t('playground.canvasPrompt') }}</label>
          <CanvasMentionEditor
            v-if="selectedNode.type === 'text' && !selectedNode.pluginUseBuiltinPanel"
            id="canvas-node-prompt"
            :model-value="selectedNode.textContent ?? selectedNode.prompt"
            :references="canvasMentionReferencesByNodeId[selectedNode.id] ?? []"
            :placeholder="t('playground.canvasTextPlaceholder')"
            :aria-label="t('playground.canvasTextContent')"
            :disabled="selectedNode.kind === 'result' || selectedNode.status === 'generating'"
            class="text-sm"
            @update:model-value="updateSelectedPrompt"
          />
          <textarea v-else-if="selectedNode.type !== 'config' && !selectedNode.pluginUseBuiltinPanel" id="canvas-node-prompt" :value="selectedNode.prompt" rows="7" class="input min-h-36 w-full resize-y text-sm leading-6" :disabled="selectedNode.kind === 'result' || selectedNode.status === 'generating'" @input="updateSelectedPrompt(($event.target as HTMLTextAreaElement).value)"></textarea>
          <section v-if="selectedNode && selectedIncomingReferences.length" class="mt-4" data-canvas-reference-bar>
            <div class="mb-2 flex items-center justify-between gap-2">
              <label class="text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasConnectedReferences') }}</label>
              <button type="button" class="btn btn-ghost h-7 px-2 text-[11px]" :disabled="selectedNode.kind === 'result' || selectedNode.status === 'generating'" data-canvas-add-connected-reference @click="startPluginReferenceSelection(selectedNode.id)">
                <Icon name="plus" size="xs" />
                {{ t('playground.canvasConnectReference') }}
              </button>
            </div>
            <div class="grid grid-cols-3 gap-2">
              <button v-for="reference in selectedIncomingReferences" :key="`${reference.sourceNodeId}:${reference.nodeId}`" type="button" class="group relative overflow-hidden rounded border border-gray-200 bg-gray-50 text-left dark:border-dark-700 dark:bg-dark-950" :title="reference.text || reference.label" :data-canvas-connected-reference="reference.sourceNodeId" @click="selectedNode.kind !== 'result' && disconnectPluginReference(selectedNode.id, reference.sourceNodeId)">
                <div class="aspect-square bg-gray-100 dark:bg-dark-800">
                  <img v-if="reference.previewUrl && reference.kind === 'image'" :src="reference.previewUrl" :alt="reference.label" class="h-full w-full object-cover">
                  <video v-else-if="reference.previewUrl && reference.kind === 'video'" :src="reference.previewUrl" class="h-full w-full object-cover" muted playsinline preload="metadata"></video>
                  <audio v-else-if="reference.previewUrl && reference.kind === 'audio'" :src="reference.previewUrl" class="w-full" controls></audio>
                  <div v-else class="flex h-full w-full items-center justify-center px-2 text-center text-[11px] text-gray-500 dark:text-dark-400">
                    <span class="line-clamp-2">{{ reference.text || reference.label }}</span>
                  </div>
                </div>
                <div class="space-y-1 px-2 py-2">
                  <p class="truncate text-[11px] font-medium text-gray-700 dark:text-gray-100">{{ reference.title }}</p>
                  <p class="truncate text-[10px] text-gray-400 dark:text-dark-500">{{ reference.label }}</p>
                </div>
              </button>
            </div>
          </section>
          <template v-if="selectedNode.type === 'text' && selectedNode.kind !== 'result'">
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-text-size">{{ t('playground.canvasTextSize') }}</label>
            <select id="canvas-text-size" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.textFontSize ?? 16" data-canvas-text-size @change="updateTextNodeSize(selectedNode.id, Number(($event.target as HTMLSelectElement).value))">
              <option v-for="size in [12, 14, 16, 18, 22, 28, 36]" :key="size" :value="size">{{ size }}px</option>
            </select>
            <div v-if="textReferenceNodes.length" class="mt-4">
              <p class="mb-1.5 text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasTextReferences') }}</p>
              <div class="flex flex-wrap gap-1.5">
                <button v-for="reference in textReferenceNodes" :key="reference.id" type="button" class="btn btn-ghost h-7 max-w-full truncate px-2 text-[11px]" :data-canvas-text-reference="reference.id" @click="insertTextNodeReference(selectedNode.id, reference.id)">@ {{ reference.pluginTitle || reference.prompt || reference.textContent || reference.type }}</button>
              </div>
            </div>
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-text-model">{{ t('playground.canvasTextModel') }}</label>
            <select id="canvas-text-model" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.model || selectedTextModel" :disabled="selectedNode.status === 'generating'" @change="updateTextNodeModel(selectedNode.id, ($event.target as HTMLSelectElement).value)">
              <option v-for="model in textModels" :key="model.id" :value="model.id">{{ model.id }}</option>
            </select>
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-text-instruction">{{ t('playground.canvasTextInstruction') }}</label>
            <textarea id="canvas-text-instruction" v-model="textInstruction" rows="4" class="input mt-1.5 min-h-24 w-full resize-y text-sm leading-6" :placeholder="t('playground.canvasTextInstructionPlaceholder')" :disabled="selectedNode.status === 'generating'"></textarea>
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-text-reasoning">{{ t('playground.canvasReasoningEffort') }}</label>
            <select id="canvas-text-reasoning" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.config?.reasoningEffort ?? 'medium'" :disabled="selectedNode.status === 'generating'" @change="updateNodeConfig(selectedNode.id, 'reasoningEffort', ($event.target as HTMLSelectElement).value)">
              <option value="none">{{ t('playground.canvasReasoningNone') }}</option>
              <option value="low">{{ t('playground.canvasReasoningLow') }}</option>
              <option value="medium">{{ t('playground.canvasReasoningMedium') }}</option>
              <option value="high">{{ t('playground.canvasReasoningHigh') }}</option>
            </select>
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-text-count">{{ t('playground.canvasTextCount') }}</label>
            <input id="canvas-text-count" :value="selectedNode.textCount ?? 1" type="number" min="1" max="10" step="1" class="input mt-1.5 h-9 w-full text-sm" :disabled="selectedNode.status === 'generating'" data-canvas-text-count @change="updateTextNodeCount(selectedNode.id, Number(($event.target as HTMLInputElement).value))">
          </template>
          <template v-if="selectedNode.type === 'image' && selectedNode.kind !== 'result'">
          <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-image-model">{{ t('playground.canvasImageModel') }}</label>
          <select id="canvas-image-model" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.model || selectedModel" :disabled="selectedNode.status === 'generating' || !models.length" data-canvas-image-model @change="updateImageNodeModel(selectedNode.id, ($event.target as HTMLSelectElement).value)">
            <option v-for="model in models" :key="model.id" :value="model.id">{{ model.id }}</option>
          </select>
          <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-node-image-count">{{ t('playground.canvasImageCount') }}</label>
          <input id="canvas-node-image-count" :value="selectedNode.imageCount ?? 4" type="number" min="1" max="10" step="1" class="input mt-1.5 h-9 w-full text-sm" :disabled="selectedNode.status === 'generating'" @change="updateNodeImageCount(selectedNode.id, Number(($event.target as HTMLInputElement).value))">
          <div class="mt-4 grid grid-cols-2 gap-2">
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasImageSize') }}
              <select class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.config?.size ?? '1024x1024'" :disabled="selectedNode.status === 'generating'" @change="updateNodeConfig(selectedNode.id, 'size', ($event.target as HTMLSelectElement).value)">
                <option value="1024x1024">1024 × 1024</option>
                <option value="1536x1024">1536 × 1024</option>
                <option value="1024x1536">1024 × 1536</option>
              </select>
            </label>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasQuality') }}
              <select class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.config?.quality ?? 'auto'" :disabled="selectedNode.status === 'generating'" @change="updateNodeConfig(selectedNode.id, 'quality', ($event.target as HTMLSelectElement).value)">
                <option value="auto">Auto</option>
                <option value="high">High</option>
                <option value="medium">Medium</option>
                <option value="low">Low</option>
              </select>
            </label>
          </div>
          <label class="mt-3 block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasBackground') }}
            <select class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.config?.background ?? 'auto'" :disabled="selectedNode.status === 'generating'" @change="updateNodeConfig(selectedNode.id, 'background', ($event.target as HTMLSelectElement).value)">
              <option value="auto">{{ t('playground.canvasBackgroundAuto') }}</option>
              <option value="transparent">{{ t('playground.canvasBackgroundTransparent') }}</option>
              <option value="opaque">{{ t('playground.canvasBackgroundOpaque') }}</option>
            </select>
          </label>
          </template>
          <template v-if="selectedNode.type === 'video' && selectedNode.kind !== 'result'">
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-video-model">{{ t('playground.canvasVideoModel') }}</label>
            <select id="canvas-video-model" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.model || videoModels[0]?.id" :disabled="selectedNode.status === 'generating'" @change="updateVideoNodeModel(selectedNode.id, ($event.target as HTMLSelectElement).value)"><option v-for="model in videoModels" :key="model.id" :value="model.id">{{ model.id }}</option></select>
            <div class="mt-4" data-canvas-video-settings-popover>
              <CanvasConfigSettingsPopover
                mode="video"
                :count="selectedNode.config?.count ?? '1'"
                :resolution="selectedNode.config?.resolution ?? '720p'"
                :duration="selectedNode.config?.duration ?? '8'"
                :aspect-ratio="selectedNode.config?.aspectRatio ?? '16:9'"
                :disabled="selectedNode.status === 'generating'"
                @update="updateNodeConfig(selectedNode.id, $event.key, $event.value)"
              />
            </div>
          </template>
          <template v-if="selectedNode.type === 'audio' && selectedNode.kind !== 'result'">
            <label class="mt-4 block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-audio-model">{{ t('playground.canvasTextModel') }}</label>
            <select id="canvas-audio-model" class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.model || selectedTextModel" :disabled="selectedNode.status === 'generating'" @change="updateTextNodeModel(selectedNode.id, ($event.target as HTMLSelectElement).value)"><option v-for="model in textModels" :key="model.id" :value="model.id">{{ model.id }}</option></select>
            <div class="mt-4" data-canvas-audio-settings-popover>
              <CanvasConfigSettingsPopover
                mode="audio"
                :count="selectedNode.config?.count ?? '1'"
                :audio-voice="selectedNode.config?.audioVoice ?? 'alloy'"
                :audio-format="selectedNode.config?.audioFormat ?? 'mp3'"
                :audio-speed="selectedNode.config?.audioSpeed ?? '1'"
                :audio-instructions="selectedNode.config?.audioInstructions ?? ''"
                :disabled="selectedNode.status === 'generating'"
                @update="updateNodeConfig(selectedNode.id, $event.key, $event.value)"
              />
            </div>
          </template>
          <div v-if="selectedNode.type === 'config'" class="space-y-2">
            <div class="grid grid-cols-4 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-800" data-canvas-config-panel-mode>
                <button v-for="mode in (['image', 'text', 'video', 'audio'] as const)" :key="mode" type="button" class="rounded-md px-1 py-1.5 text-[10px] font-medium transition" :class="(selectedNode.config?.mode ?? 'image') === mode ? 'bg-white text-teal-700 shadow-sm dark:bg-dark-700 dark:text-teal-200' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'" :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)" @click="updateNodeConfig(selectedNode.id, 'mode', mode)">
                {{ t(`playground.canvasConfigMode${mode.charAt(0).toUpperCase()}${mode.slice(1)}`) }}
              </button>
            </div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.modelLabel') }}
              <select class="select mt-1.5 h-9 w-full text-xs" :value="selectedNode.config?.model || selectedNode.model || ((selectedNode.config?.mode ?? 'image') === 'image' ? models[0]?.id : (selectedNode.config?.mode ?? 'image') === 'video' ? videoModels[0]?.id : textModels[0]?.id)" :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)" @change="updateNodeConfig(selectedNode.id, 'model', ($event.target as HTMLSelectElement).value)">
                <option v-for="model in ((selectedNode.config?.mode ?? 'image') === 'image' ? models : (selectedNode.config?.mode ?? 'image') === 'video' ? videoModels : textModels)" :key="model.id" :value="model.id">{{ model.id }}</option>
              </select>
            </label>
            <CanvasConfigSettingsPopover
              :mode="selectedNode.config?.mode ?? 'image'"
              :count="selectedNode.config?.count ?? '1'"
              :size="selectedNode.config?.size ?? '1024x1024'"
              :quality="selectedNode.config?.quality ?? 'auto'"
              :background="selectedNode.config?.background ?? 'auto'"
              :resolution="selectedNode.config?.resolution ?? '720p'"
              :duration="selectedNode.config?.duration ?? '8'"
              :aspect-ratio="selectedNode.config?.aspectRatio ?? '16:9'"
              :audio-voice="selectedNode.config?.audioVoice ?? 'alloy'"
              :audio-format="selectedNode.config?.audioFormat ?? 'mp3'"
              :audio-speed="selectedNode.config?.audioSpeed ?? '1'"
              :audio-instructions="selectedNode.config?.audioInstructions ?? ''"
              :reasoning-effort="selectedNode.config?.reasoningEffort ?? 'medium'"
              :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)"
              @update="updateNodeConfig(selectedNode.id, $event.key, $event.value)"
            />
            <p class="text-xs leading-5 text-gray-600 dark:text-dark-300">{{ t('playground.canvasConfigHint') }}</p>
            <CanvasMentionEditor
              :model-value="selectedNode.prompt"
              :references="canvasMentionReferencesByNodeId[selectedNode.id] ?? []"
              :placeholder="t('playground.canvasPrompt')"
              :aria-label="t('playground.canvasPrompt')"
              :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)"
              class="text-sm"
              @update:model-value="updateSelectedPrompt"
            />
            <div class="grid grid-cols-2 gap-2">
              <label class="block text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasImageCount') }}
                <input class="input mt-1.5 h-9 w-full text-sm" type="number" min="1" max="10" :value="selectedNode.config?.count ?? '1'" :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)" @change="updateNodeConfig(selectedNode.id, 'count', ($event.target as HTMLInputElement).value)">
              </label>
              <button type="button" class="btn btn-primary mt-5 h-9 gap-1.5 text-xs" :disabled="selectedNode.status === 'generating' || configBusyNodeIds.has(selectedNode.id)" @click="generateConfigNode(selectedNode.id)">
                <Icon :name="selectedNode.status === 'generating' ? 'x' : 'sparkles'" size="xs" />
                {{ selectedNode.status === 'generating' ? t('playground.canvasCancelGeneration') : t('playground.generate') }}
              </button>
            </div>
          </div>
          <div v-if="selectedNode.type === 'image'" class="mt-4">
            <div class="mb-1.5 flex items-center justify-between gap-2">
              <label class="text-xs font-medium text-gray-700 dark:text-dark-300">{{ t('playground.canvasReferencesLabel') }}</label>
              <button type="button" class="btn btn-ghost h-7 px-2 text-[11px]" :disabled="selectedNode.kind === 'result' || selectedNode.status === 'generating'" @click="referenceInput?.click()">
                <Icon name="upload" size="xs" />
                {{ t('common.add') }}
              </button>
              <input ref="referenceInput" type="file" accept="image/*" multiple class="hidden" @change="handleReferenceFiles">
            </div>
            <div v-if="selectedNode.referenceImages?.length" class="grid grid-cols-3 gap-2">
              <div v-for="(reference, index) in selectedNode.referenceImages" :key="reference.id" class="group relative aspect-square overflow-hidden rounded border border-gray-200 dark:border-dark-700">
                <img v-if="referencePreviews[reference.cacheKey]" :src="referencePreviews[reference.cacheKey]" :alt="reference.name" class="h-full w-full object-cover">
                <button type="button" class="absolute right-1 top-1 hidden h-5 w-5 items-center justify-center rounded bg-gray-950/70 text-white group-hover:flex" :title="t('common.remove')" @click="removeReference(index)">
                  <Icon name="x" size="xs" />
                  <span class="sr-only">{{ t('common.remove') }}</span>
                </button>
              </div>
            </div>
            <p v-else class="rounded border border-dashed border-gray-200 px-3 py-4 text-center text-xs text-gray-400 dark:border-dark-700">{{ t('playground.canvasReferencesEmpty') }}</p>
          </div>
          <div class="mt-5 space-y-2 text-xs text-gray-500 dark:text-dark-400">
            <div class="flex justify-between gap-3"><span>{{ t('playground.modelLabel') }}</span><span class="max-w-36 truncate text-right text-gray-800 dark:text-gray-100">{{ selectedNode.model || selectedModel || '-' }}</span></div>
            <div class="flex justify-between gap-3"><span>{{ t('playground.canvasNodeStatus') }}</span><span class="text-right">{{ nodeStatusLabel(selectedNode.status) }}</span></div>
          </div>
          <div v-if="selectedNode.kind === 'result' && selectedNode.type === 'image'" class="mt-6 grid grid-cols-2 gap-2">
            <button type="button" class="btn btn-ghost gap-1.5" @click="useResultAsReference(selectedNode.id)">
              <Icon name="upload" size="sm" />
              {{ t('playground.canvasUseAsReference') }}
            </button>
            <button type="button" class="btn btn-primary gap-1.5" :disabled="!selectedNode.imageUrl || downloadingNodeId === selectedNode.id" @click="downloadNode(selectedNode.id)">
              <Icon name="download" size="sm" />
              {{ t('playground.canvasDownload') }}
            </button>
          </div>
          <button v-else-if="selectedNode.kind === 'result' && (selectedNode.videoUrl || selectedNode.audioUrl)" type="button" class="btn btn-primary mt-6 w-full gap-1.5" :disabled="downloadingNodeId === selectedNode.id" @click="downloadNode(selectedNode.id)">
            <Icon name="download" size="sm" />
            {{ t('playground.canvasDownload') }}
          </button>
          <div v-if="selectedNode.kind === 'result' && selectedNode.type === 'video' && selectedNode.videoUrl" class="mt-3 space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-300" for="canvas-video-edit-prompt">{{ t('playground.canvasEditVideo') }}</label>
            <textarea id="canvas-video-edit-prompt" v-model="videoEditPrompt" class="textarea min-h-20 w-full text-xs" :placeholder="t('playground.canvasVideoEditPrompt')" :disabled="videoEditBusy"></textarea>
            <button type="button" class="btn btn-secondary h-8 w-full gap-1.5 text-xs" :disabled="videoEditBusy || !videoEditPrompt.trim() || !videoModels.length" @click="editVideoNode(selectedNode.id)">
              <Icon :name="videoEditBusy ? 'refresh' : 'edit'" size="xs" :class="{ 'animate-spin': videoEditBusy }" />
              {{ videoEditBusy ? t('playground.canvasGenerating') : t('playground.canvasEditVideo') }}
            </button>
          </div>
          <button v-else-if="selectedNode.type === 'image'" type="button" class="btn btn-primary mt-6 w-full gap-1.5" :disabled="selectedNode.status === 'generating' || !selectedNode.prompt.trim() || !canGenerate" @click="generateNode(selectedNode.id)">
            <Icon name="sparkles" size="sm" />
            {{ t('playground.generate') }}
          </button>
          <button v-else-if="selectedNode.type === 'video' && selectedNode.kind !== 'result'" type="button" class="btn btn-primary mt-6 w-full gap-1.5" :disabled="selectedNode.status !== 'generating' && (!canvasNodeHasPromptInput(selectedNode) || !videoModels.length)" @click="generateVideoNode(selectedNode.id)">
            <Icon :name="selectedNode.status === 'generating' ? 'x' : 'play'" size="sm" />
            {{ selectedNode.status === 'generating' ? t('playground.canvasCancelGeneration') : t('playground.canvasGenerateVideo') }}
          </button>
          <button v-else-if="selectedNode.type === 'audio' && selectedNode.kind !== 'result'" type="button" class="btn btn-primary mt-6 w-full gap-1.5" :disabled="selectedNode.status !== 'generating' && !canvasNodeHasPromptInput(selectedNode)" @click="selectedNode.status === 'generating' ? cancelAudioNode(selectedNode.id) : generateAudioNode(selectedNode.id)"><Icon :name="selectedNode.status === 'generating' ? 'x' : 'play'" size="sm" />{{ selectedNode.status === 'generating' ? t('playground.canvasCancelGeneration') : t('playground.canvasGenerateAudio') }}</button>
          <button v-else-if="selectedNode.type === 'text' && selectedNode.kind !== 'result'" type="button" class="btn btn-primary mt-6 w-full gap-1.5" :disabled="!canGenerateText(selectedNode)" @click="generateTextNode(selectedNode.id)">
            <Icon :name="selectedNode.status === 'generating' ? 'x' : 'sparkles'" size="sm" />
            {{ selectedNode.status === 'generating' ? t('playground.canvasCancelGeneration') : (selectedNode.textContent || selectedNode.prompt).trim() ? t('playground.canvasRewriteText') : t('playground.canvasGenerateText') }}
          </button>
          <p v-else-if="selectedNode.type !== 'config'" class="mt-6 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ selectedNode.kind === 'result' && selectedNode.type === 'text' ? t('playground.canvasTextResultHint') : t('playground.canvasNodeSavedLocally') }}</p>
          <p v-if="selectedNode.type === 'image' && !canGenerate" class="mt-2 text-xs leading-5 text-amber-700 dark:text-amber-300">{{ t('playground.canvasUnsupportedKey') }}</p>
          <p class="mt-6 text-center text-[10px] text-gray-400 dark:text-dark-500">{{ t('playground.canvasCredit') }}</p>
        </aside>
      </section>
    </main>
    <input ref="importInput" type="file" accept="application/json,.zip,application/zip" class="hidden" @change="handleImportFile">
    <CanvasAssetPicker v-if="assetPickerOpen" :assets="canvasAssets" @close="closeAssetPicker" @insert="insertAssetFromPicker" />
  </component>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { Icon } from '@/components/icons'
import { keysAPI } from '@/api'
import type { ApiKey } from '@/types'
import { useAppStore, useAuthStore } from '@/stores'
import { fetchPlaygroundModels, fetchPlaygroundVideo, fetchPlaygroundVideoContent, sendPlaygroundChat, sendPlaygroundImageGeneration, sendPlaygroundSpeech, sendPlaygroundTranscription, sendPlaygroundVideoEdit, sendPlaygroundVideoGeneration } from '@/features/playground/api'
import { cachePlaygroundImage, createPlaygroundImageCacheKey, listPlaygroundGalleryImages, readCachedPlaygroundImageBlob, removeCachedPlaygroundImages, restoreCachedPlaygroundImage, type PlaygroundGalleryImage } from '@/features/playground/imageCache'
import { downloadPlaygroundImage, imageFilenameExtension, playgroundImageUrl } from '@/features/playground/imageDownload'
import { buildPlaygroundImageRequest, normalizePlaygroundChatModels, normalizePlaygroundImageModels, normalizePlaygroundKeys, normalizePlaygroundVideoModels, formatPlaygroundKeyLabel, resolvePlaygroundDefaultKeyId, resolvePlaygroundDefaultModel, resolveSelectedKeyId, resolveSelectedModel } from '@/features/playground/viewModel'
import type { PlaygroundAttachment, PlaygroundKeySummary, PlaygroundModel } from '@/features/playground/types'
import { buildCanvasMentionReferences, buildCanvasReferenceItems, expandCanvasGroupResourceNodes, getCanvasNodeLabel, getCanvasNodePreviewUrl, getCanvasNodeText, isCanvasResourceNode, type CanvasMentionReference, type CanvasReferenceItem } from '@/features/playground/canvas/canvasReferences'
import { listCanvasSavedAssets, removeCanvasSavedAsset, saveCanvasAsset as persistCanvasAsset, type CanvasSavedAsset } from '@/features/playground/canvas/canvasAssetStore'
import CanvasConnections from '@/features/playground/canvas/CanvasConnections.vue'
import CanvasConnectionContextMenu from '@/features/playground/canvas/CanvasConnectionContextMenu.vue'
import CanvasMiniMap from '@/features/playground/canvas/CanvasMiniMap.vue'
import CanvasNode from '@/features/playground/canvas/CanvasNode.vue'
import CanvasConfigSettingsPopover from '@/features/playground/canvas/CanvasConfigSettingsPopover.vue'
import CanvasMentionEditor from '@/features/playground/canvas/CanvasMentionEditor.vue'
import CanvasToolbar from '@/features/playground/canvas/CanvasToolbar.vue'
import CanvasContextMenu from '@/features/playground/canvas/CanvasContextMenu.vue'
import CanvasAssetPicker from '@/features/playground/canvas/CanvasAssetPicker.vue'
import CanvasAssistant from '@/features/playground/canvas/CanvasAssistant.vue'
import CanvasPromptLibrary from '@/features/playground/canvas/CanvasPromptLibrary.vue'
import CanvasWebDavDialog from '@/features/playground/canvas/CanvasWebDavDialog.vue'
import CanvasImageEditor from '@/features/playground/canvas/CanvasImageEditor.vue'
import CanvasImageSplitDialog from '@/features/playground/canvas/CanvasImageSplitDialog.vue'
import CanvasImageUpscaleDialog from '@/features/playground/canvas/CanvasImageUpscaleDialog.vue'
import CanvasImageViewer from '@/features/playground/canvas/CanvasImageViewer.vue'
import CanvasImageMaskDialog from '@/features/playground/canvas/CanvasImageMaskDialog.vue'
import CanvasImageAngleDialog from '@/features/playground/canvas/CanvasImageAngleDialog.vue'
import CanvasNodeInfoDialog from '@/features/playground/canvas/CanvasNodeInfoDialog.vue'
import CanvasAgentDialog from '@/features/playground/canvas/CanvasAgentDialog.vue'
import CanvasPluginManagerDialog from '@/features/playground/canvas/CanvasPluginManagerDialog.vue'
import { useCanvasStore } from '@/features/playground/canvas/canvasStore'
import type { CanvasBackgroundMode, CanvasImageVariant, CanvasNode as CanvasNodeData, CanvasPersistedState, CanvasProject, CanvasPromptEntry, CanvasReferenceImage, CanvasTextVariant, CanvasTool, CanvasViewport } from '@/features/playground/canvas/types'
import { cacheCanvasMedia, createCanvasMediaCacheKey, readCachedCanvasMediaBlob, restoreCanvasMedia } from '@/features/playground/canvas/mediaCache'
import { captureCanvasVideoFrame, type CanvasVideoFramePosition } from '@/features/playground/canvas/videoFrame'
import { createCanvasArchive, readCanvasArchive } from '@/features/playground/canvas/canvasArchive'
import { agentNodeToCanvasNode, CanvasAgentBridge, createCanvasAgentSnapshot, type CanvasAgentApproval, type CanvasAgentConversation, type CanvasAgentEvent, type CanvasAgentSkill, type CanvasAgentThread, type CanvasAgentToolCall } from '@/features/playground/canvas/canvasAgent'
import { loadCachedCanvasPrompts, refreshCanvasPromptRegistry } from '@/features/playground/canvas/promptRegistry'
import { downloadCanvasState, uploadCanvasState, type CanvasWebDavConfig } from '@/features/playground/canvas/webdavSync'
import { splitCanvasImage, transformCanvasImage, transformCanvasImageAngle, upscaleCanvasImage, type CanvasImageAngleParams, type CanvasImageGrid, type CanvasImageTransform, type CanvasImageUpscaleParams } from '@/features/playground/canvas/imageTransform'
import { fetchCanvasPluginCatalog, installCanvasPluginFromUrl, isPluginNodeType, loadCanvasPlugins, setCanvasPluginEnabled, uninstallCanvasPlugin as removeCanvasPlugin, type CanvasPluginCatalogEntry, type CanvasPluginManifest } from '@/features/playground/canvas/canvasPlugins'
import { createPluginStorage, getCanvasPluginDefinition, loadCanvasPluginSource, unregisterCanvasPlugin, type CanvasPluginRuntimeContext } from '@/features/playground/canvas/canvasPluginRuntime'

const PROJECTS_PANEL_COLLAPSED_STORAGE_KEY = 'sub2api.playground.canvas.projectsPanelCollapsed'

const props = defineProps<{
  embedded?: boolean
}>()

const { t } = useI18n()
const route = useRoute()
const appStore = useAppStore()
let sidebarCollapsedBeforeCanvas: boolean | null = null
const authStore = useAuthStore()
const userId = computed(() => authStore.user?.id ?? null)
const canvasStore = useCanvasStore(userId.value ?? 0)
const canvasRef = ref<HTMLElement | null>(null)
const canvasViewportSize = ref({ width: 0, height: 0 })
let canvasResizeObserver: ResizeObserver | null = null
const referenceInput = ref<HTMLInputElement | null>(null)
const importInput = ref<HTMLInputElement | null>(null)
const playgroundKeys = ref<PlaygroundKeySummary[]>([])
const models = ref<PlaygroundModel[]>([])
const textModels = ref<PlaygroundModel[]>([])
const videoModels = ref<PlaygroundModel[]>([])
const selectedKeyId = ref<number | null>(null)
const selectedTextKeyId = ref<number | null>(null)
const selectedModel = ref('')
const selectedTextModel = ref('')
const textInstruction = ref('')
const selectedNodeId = ref<string | null>(null)
const selectedNodeIds = ref<Set<string>>(new Set())
const selectedConnectionId = ref<string | null>(null)
const loadingModels = ref(false)
const loadingTextModels = ref(false)
let suppressCanvasModelWatchers = false
const activeGenerationIds = ref<Set<string>>(new Set())
const queuedGenerationIds = ref<Set<string>>(new Set())
const generationQueue: Array<() => Promise<void>> = []
const generationTokens = new Map<string, string>()
const textGenerationControllers = new Map<string, AbortController>()
const assistantOpen = ref(false)
const assistantLoading = ref(false)
const assistantModel = ref('')
const assistantController = ref<AbortController | null>(null)
const agentOpen = ref(false)
const agentConnected = ref(false)
const agentBusy = ref(false)
const agentSending = ref(false)
const agentError = ref('')
const agentConversation = ref<CanvasAgentConversation>({ revision: 0, conversationId: '', threadId: '', status: 'idle', mcpStatuses: {} })
const agentMessages = ref<Array<{ id: string; role: 'user' | 'assistant' | 'system' | 'error'; text: string }>>([])
const agentThreads = ref<CanvasAgentThread[]>([])
const agentApproval = ref<CanvasAgentApproval | null>(null)
const agentSkills = ref<CanvasAgentSkill[]>([])
const agentEndpoint = ref('http://127.0.0.1:17371')
const agentToken = ref('')
const agentBridge = new CanvasAgentBridge()
let agentPublishTimer: number | null = null
const pluginManagerOpen = ref(false)
const plugins = ref<CanvasPluginManifest[]>([])
const officialPlugins = ref<CanvasPluginCatalogEntry[]>([])
const officialPluginsLoaded = ref(false)
const pluginBusy = ref(false)
const pluginError = ref('')
const pluginPanelNodeId = ref<string | null>(null)
const pluginGenerationBusy = ref<string | null>(null)
const pluginGenerationControllers = new Map<string, AbortController>()
const promptLibraryOpen = ref(false)
const customPrompts = ref<CanvasPromptEntry[]>([])
const remotePrompts = ref<CanvasPromptEntry[]>([])
const promptRegistryLoading = ref(false)
const promptRegistryLoaded = ref(false)
const canvasAssetSearch = ref('')
const canvasPromptSearch = ref('')
const webdavOpen = ref(false)
const webdavBusy = ref(false)
const webdavConfig = ref<CanvasWebDavConfig>({ url: '', username: '', password: '', fileName: 'sub2api-canvas.json' })

watch(pluginManagerOpen, (open) => {
  if (!open || officialPluginsLoaded.value) return
  officialPluginsLoaded.value = true
  void fetchCanvasPluginCatalog().then((entries) => { officialPlugins.value = entries }).catch(() => { officialPluginsLoaded.value = false })
})
const imageEditorOpen = ref(false)
const imageEditorBusy = ref(false)
const imageEditorUrl = ref('')
const imageEditorTarget = ref<{ nodeId: string; imageIndex: number } | null>(null)
const splitDialogOpen = ref(false)
const splitDialogBusy = ref(false)
const splitDialogUrl = ref('')
const splitDialogTarget = ref<{ nodeId: string; imageIndex: number } | null>(null)
const upscaleDialogOpen = ref(false)
const upscaleDialogBusy = ref(false)
const upscaleDialogUrl = ref('')
const upscaleDialogTarget = ref<{ nodeId: string; imageIndex: number } | null>(null)
const upscaleNodeIds = ref<Set<string>>(new Set())
const imageViewerOpen = ref(false)
const imageViewerUrl = ref('')
const imageViewerTitle = ref('')
const maskDialogOpen = ref(false)
const maskDialogBusy = ref(false)
const maskDialogUrl = ref('')
const maskDialogTarget = ref<{ nodeId: string; imageIndex: number } | null>(null)
const angleDialogOpen = ref(false)
const angleDialogBusy = ref(false)
const angleDialogUrl = ref('')
const angleDialogTarget = ref<{ nodeId: string; imageIndex: number } | null>(null)
const canvasInfoNodeId = ref<string | null>(null)
const reversePromptIds = ref<Set<string>>(new Set())
const transcribingNodeIds = ref<Set<string>>(new Set())
const videoControllers = new Map<string, AbortController>()
const audioControllers = new Map<string, AbortController>()
const videoEditPrompt = ref('')
const videoEditBusy = ref(false)
const runningGenerations = ref(0)
const MAX_CONCURRENT_GENERATIONS = 4
const downloadingNodeId = ref<string | null>(null)
const connectingFrom = ref<string | null>(null)
const connectingHandleType = ref<'source' | 'target'>('source')
const connectionTargetNodeId = ref<string | null>(null)
const pluginReferenceTargetId = ref<string | null>(null)
const lastReferenceTargetId = ref<string | null>(null)
const hoveredNodeId = ref<string | null>(null)
const pointerPosition = ref({ x: 0, y: 0 })
const nodeCreatePosition = ref<{ x: number; y: number } | null>(null)
const pendingConnectionSource = ref<string | null>(null)
const pendingConnectionHandleType = ref<'source' | 'target'>('source')
const selectionBox = ref<{
  startX: number
  startY: number
  currentX: number
  currentY: number
  additive: boolean
  initialSelectedNodeIds: Set<string>
} | null>(null)
const resizing = ref<{ id: string; corner: 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right'; pointerX: number; pointerY: number; x: number; y: number; width: number; height: number; keepRatio: boolean; ratio: number } | null>(null)
// The reference canvas opens in move mode so a blank-canvas drag pans and a
// node drag moves the element immediately. Selection remains available as an
// explicit tool, while Control/Command temporarily starts box selection.
const tool = ref<CanvasTool>('pan')
const temporaryPanBaseTool = ref<CanvasTool | null>(null)
const projectsPanelCollapsed = ref(readProjectsPanelCollapsed())
const collapsedCanvasGroups = ref<Set<string>>(new Set())
const CANVAS_PANEL_WIDTH_STORAGE_KEY = 'sub2api.playground.canvas.panelWidth'
const CANVAS_PANEL_MIN_WIDTH = 208
const CANVAS_PANEL_MAX_WIDTH = 360
const canvasPanelWidth = ref(readCanvasPanelWidth())
const canvasPanelTab = ref<'canvas' | 'assets' | 'prompts'>('canvas')
const canvasNodeSearch = ref('')
const canvasNodeFilter = ref<'all' | CanvasNodeData['type']>('all')
const canvasElementSelectMode = ref(false)
const canvasElementSelection = ref<Set<string>>(new Set())
const canvasPanelResize = ref<{ startX: number; startWidth: number } | null>(null)
const dragging = ref<{ nodeIds: string[]; pointerX: number; pointerY: number; positions: Record<string, { x: number; y: number }>; moved: boolean } | null>(null)
const panning = ref<{
  pointerX: number
  pointerY: number
  x: number
  y: number
  moved: boolean
  startedOnBackground: boolean
} | null>(null)
let panningFrame: number | null = null
let pendingPanPointer: { clientX: number; clientY: number } | null = null
const capturedPointerId = ref<number | null>(null)
const imageObjectUrls = new Set<string>()
const clipboard = ref('')
const assetPickerOpen = ref(false)
const assetPickerImages = ref<Array<PlaygroundGalleryImage & { url: string }>>([])
const savedCanvasAssets = ref<CanvasSavedAsset[]>([])
const savedCanvasAssetUrls = new Map<string, string>()
type CanvasAssetItem = {
  key: string
  kind: 'image' | 'video' | 'audio' | 'text'
  source: 'gallery' | 'node' | 'saved'
  title: string
  subtitle?: string
  url?: string
  text?: string
  nodeId?: string
  savedAssetId?: string
  imageIndex?: number
  cacheKey?: string
}
const contextMenu = ref<{ x: number; y: number; nodeId: string } | null>(null)
const connectionContextMenu = ref<{ x: number; y: number; connectionId: string } | null>(null)
const canvasMenuOpen = ref(false)
const shortcutsOpen = ref(false)
const titleEditing = ref(false)
const titleDraft = ref('')
const VIEWPORT_CHECKPOINT_IDLE_MS = 350
let viewportCheckpointArmed = false
let viewportCheckpointResetTimer: number | null = null

const activeProject = computed(() => canvasStore.activeProject.value)
const visibleCanvasNodes = computed<CanvasNodeData[]>(() => {
  const project = activeProject.value
  if (!project) return []

  const rect = canvasRef.value?.getBoundingClientRect()
  const width = canvasViewportSize.value.width || rect?.width || 0
  const height = canvasViewportSize.value.height || rect?.height || 0
  if (!width || !height) return project.nodes

  const scale = Math.max(project.viewport.scale, 0.05)
  const padding = 280
  const viewLeft = -project.viewport.x / scale - padding
  const viewTop = -project.viewport.y / scale - padding
  const viewRight = viewLeft + width / scale + padding * 2
  const viewBottom = viewTop + height / scale + padding * 2
  const preservedIds = new Set<string>([
    ...selectedNodeIds.value,
    ...(dragging.value?.nodeIds ?? []),
    connectingFrom.value,
    connectionTargetNodeId.value,
    hoveredNodeId.value,
    pluginPanelNodeId.value,
    contextMenu.value?.nodeId,
  ].filter((id): id is string => Boolean(id)))

  return project.nodes.filter((node) => (
    preservedIds.has(node.id)
    || (
      node.x + node.width > viewLeft
      && node.x < viewRight
      && node.y + node.height > viewTop
      && node.y < viewBottom
    )
  ))
})
const canvasInfoNode = computed(() => activeProject.value?.nodes.find((node) => node.id === canvasInfoNodeId.value) ?? null)
const isCanvasView = computed(() => route.query.view === 'canvas')
const canvasPanelTabs = computed(() => [
  { value: 'canvas' as const, label: t('playground.canvasElements') },
  { value: 'assets' as const, label: t('playground.canvasAssets') },
  { value: 'prompts' as const, label: t('playground.canvasPromptLibrary') },
])
const canvasNodeFilters = computed(() => [
  { value: 'all' as const, label: t('common.all') },
  { value: 'image' as const, label: t('playground.canvasNodeType.image') },
  { value: 'video' as const, label: t('playground.canvasNodeType.video') },
  { value: 'text' as const, label: t('playground.canvasNodeType.text') },
  { value: 'audio' as const, label: t('playground.canvasNodeType.audio') },
  { value: 'config' as const, label: t('playground.canvasNodeType.config') },
  { value: 'group' as const, label: t('playground.canvasNodeType.group') },
])
const canvasAssets = computed<CanvasAssetItem[]>(() => {
  const nodeAssets: CanvasAssetItem[] = []
  for (const node of activeProject.value?.nodes ?? []) {
    const title = canvasNodeLabel(node)
    const subtitle = node.model || node.type
    if (node.type === 'image') {
      const urls = node.imageUrls?.length ? node.imageUrls : node.imageUrl ? [node.imageUrl] : []
      urls.forEach((url, imageIndex) => {
        if (!url) return
        nodeAssets.push({
          key: `node:${node.id}:image:${imageIndex}`,
          kind: 'image',
          source: 'node',
          title: urls.length > 1 ? `${title} · ${imageIndex + 1}` : title,
          subtitle,
          url,
          nodeId: node.id,
          imageIndex,
          cacheKey: node.imageCacheKeys?.[imageIndex] ?? (imageIndex === 0 ? node.imageCacheKey : undefined),
        })
      })
    } else if (node.type === 'video' && node.videoUrl) {
      nodeAssets.push({ key: `node:${node.id}:video`, kind: 'video', source: 'node', title, subtitle, url: node.videoUrl, nodeId: node.id })
    } else if (node.type === 'audio' && node.audioUrl) {
      nodeAssets.push({ key: `node:${node.id}:audio`, kind: 'audio', source: 'node', title, subtitle, url: node.audioUrl, nodeId: node.id })
    } else if (node.type === 'text') {
      const text = (node.textContent || node.prompt || '').trim()
      if (text) nodeAssets.push({ key: `node:${node.id}:text`, kind: 'text', source: 'node', title, subtitle, text, nodeId: node.id })
    }
  }
  const galleryAssets: CanvasAssetItem[] = assetPickerImages.value.map((asset) => ({
    key: `gallery:${asset.key}`,
    kind: 'image',
    source: 'gallery',
    title: asset.prompt || t('playground.generatedImage'),
    subtitle: asset.model || asset.size,
    url: asset.url,
    cacheKey: asset.key,
  }))
  const savedAssets: CanvasAssetItem[] = savedCanvasAssets.value.map((asset) => ({
    key: `saved:${asset.id}`,
    kind: asset.kind,
    source: 'saved',
    title: asset.title,
    subtitle: t('playground.canvasSavedAsset'),
    text: asset.text,
    url: savedCanvasAssetUrls.get(asset.id) || asset.url,
    nodeId: asset.sourceNodeId,
    savedAssetId: asset.id,
    imageIndex: asset.imageIndex,
    cacheKey: asset.cacheKey,
  }))
  return [...savedAssets, ...nodeAssets, ...galleryAssets]
})
const filteredCanvasAssets = computed<CanvasAssetItem[]>(() => {
  const query = canvasAssetSearch.value.trim().toLowerCase()
  if (!query) return canvasAssets.value
  return canvasAssets.value.filter((asset) => `${asset.title} ${asset.subtitle || ''} ${asset.text || ''} ${asset.kind}`.toLowerCase().includes(query))
})
const filteredCanvasPrompts = computed(() => {
  const query = canvasPromptSearch.value.trim().toLowerCase()
  if (!query) return promptLibraryEntries.value
  return promptLibraryEntries.value.filter((prompt) => `${prompt.title} ${prompt.content} ${prompt.tags.join(' ')} ${prompt.source}`.toLowerCase().includes(query))
})
const selectedNode = computed(() => activeProject.value?.nodes.find((node) => node.id === selectedNodeId.value) ?? null)
const canvasNodeRows = computed(() => {
  const project = activeProject.value
  if (!project) return [] as Array<{ node: CanvasNodeData; depth: number; hasChildren: boolean }>
  const query = canvasNodeSearch.value.trim().toLowerCase()
  const matches = (node: CanvasNodeData): boolean => {
    const typeMatches = canvasNodeFilter.value === 'all' || node.type === canvasNodeFilter.value
    if (!typeMatches) return false
    if (!query) return true
    return `${canvasNodeLabel(node)} ${canvasNodePreview(node)}`.toLowerCase().includes(query)
  }
  const matchingNodes = project.nodes.filter(matches)
  const groups = new Set(project.nodes.filter((node) => node.type === 'group').map((node) => node.id))
  const children = new Map<string, CanvasNodeData[]>()
  for (const node of matchingNodes) {
    if (!node.groupId || !groups.has(node.groupId)) continue
    const groupChildren = children.get(node.groupId) ?? []
    groupChildren.push(node)
    children.set(node.groupId, groupChildren)
  }
  const rows: Array<{ node: CanvasNodeData; depth: number; hasChildren: boolean }> = []
  for (const node of project.nodes) {
    if (node.groupId && groups.has(node.groupId)) continue
    if (node.type !== 'group') {
      if (matches(node)) rows.push({ node, depth: 0, hasChildren: false })
      continue
    }
    const groupChildren = children.get(node.id) ?? []
    if (!matches(node) && !groupChildren.length) continue
    rows.push({ node, depth: 0, hasChildren: groupChildren.length > 0 })
    if (!collapsedCanvasGroups.value.has(node.id)) {
      rows.push(...groupChildren.map((child) => ({ node: child, depth: 1, hasChildren: false })))
    }
  }
  return rows
})
const selectedIncomingReferences = computed(() => {
  const project = activeProject.value
  const node = selectedNode.value
  return project && node ? buildCanvasReferenceItems(project, node.id) : []
})
const groupChildCount = (id: string): number => activeProject.value?.nodes.filter((node) => node.groupId === id).length ?? 0
function canvasNodeIcon(node: CanvasNodeData): 'grid' | 'document' | 'play' | 'chatBubble' | 'cog' | 'sparkles' {
  if (node.type === 'group') return 'grid'
  if (node.type === 'text') return 'document'
  if (node.type === 'video') return 'play'
  if (node.type === 'audio') return 'chatBubble'
  if (node.type === 'config') return 'cog'
  if (node.kind === 'result') return 'sparkles'
  return 'sparkles'
}

function canvasAssetKindLabel(kind: CanvasAssetItem['kind']): string {
  if (kind === 'audio') return t('playground.canvasNodeType.audio')
  return t(`playground.canvasNodeType.${kind}`)
}

function canvasNodeLabel(node: CanvasNodeData): string {
  if (node.title?.trim()) return node.title.trim()
  if (node.type === 'group') return node.prompt || t('playground.canvasGroupNode')
  if (node.type === 'text') return (node.textContent || node.prompt || t('playground.canvasTextNode')).trim()
  if (node.prompt.trim()) return node.prompt.trim()
  if (node.kind === 'result') return t('playground.canvasResult', { index: (node.resultIndex ?? 0) + 1 })
  return t(`playground.canvasNodeType.${node.type}`)
}

function canvasNodeHasPromptInput(node: CanvasNodeData): boolean {
  const project = activeProject.value
  if (!project || (node.type !== 'video' && node.type !== 'audio')) return false
  if (node.prompt.trim()) return true
  return collectCanvasUpstreamNodes(project, node.id)
    .flatMap((candidate) => candidate.type === 'group' ? expandCanvasGroupResourceNodes(candidate, project) : [candidate])
    .some((candidate) => candidate.type === 'text' && Boolean((candidate.textContent || candidate.prompt).trim()))
}

function canvasNodePreview(node: CanvasNodeData): string {
  if (node.kind === 'result') return node.model || t('playground.canvasResult', { index: (node.resultIndex ?? 0) + 1 })
  if (node.type === 'group') return t('playground.canvasGroupCount', { count: groupChildCount(node.id) })
  return node.status === 'generating' ? t('playground.canvasGenerating') : node.status === 'error' ? (node.errorMessage || t('playground.canvasGenerationFailed')) : node.type
}

function toggleCanvasGroupCollapse(id: string): void {
  const next = new Set(collapsedCanvasGroups.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  collapsedCanvasGroups.value = next
}

function toggleCanvasElementSelectMode(): void {
  canvasElementSelectMode.value = !canvasElementSelectMode.value
  if (!canvasElementSelectMode.value) canvasElementSelection.value = new Set()
}

function toggleCanvasElementSelection(id: string): void {
  const next = new Set(canvasElementSelection.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  canvasElementSelection.value = next
}

function focusCanvasNode(id: string): void {
  const project = activeProject.value
  const node = project?.nodes.find((candidate) => candidate.id === id)
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !node || !rect) return
  selectedConnectionId.value = null
  selectedNodeId.value = id
  selectedNodeIds.value = new Set([id])
  const scale = Math.min(1.2, Math.max(0.35, project.viewport.scale))
  canvasStore.updateViewport({
    x: rect.width / 2 - (node.x + node.width / 2) * scale,
    y: rect.height / 2 - (node.y + node.height / 2) * scale,
    scale,
  })
}

function refreshCanvasViewportSize(): void {
  const element = canvasRef.value
  if (!element) return
  const width = Math.max(0, element.clientWidth)
  const height = Math.max(0, element.clientHeight)
  if (canvasViewportSize.value.width === width && canvasViewportSize.value.height === height) return
  canvasViewportSize.value = { width, height }
}

const dropTargetGroupId = ref<string | null>(null)
const textReferenceNodes = computed(() => {
  const selected = selectedNode.value
  if (!selected || selected.type !== 'text') return []
  return (activeProject.value?.connections ?? [])
    .filter((connection) => connection.to === selected.id)
    .map((connection) => activeProject.value?.nodes.find((node) => node.id === connection.from))
    .filter((node): node is CanvasNodeData => Boolean(node))
})
const canvasMentionReferencesByNodeId = computed<Record<string, CanvasMentionReference[]>>(() => {
  const project = activeProject.value
  if (!project) return {}
  return Object.fromEntries(project.nodes.map((target) => [target.id, buildCanvasMentionReferences(project, target.id)]))
})
const canvasReferenceItemsByNodeId = computed<Record<string, CanvasReferenceItem[]>>(() => {
  const project = activeProject.value
  if (!project) return {}
  return Object.fromEntries(project.nodes.map((target) => [target.id, buildCanvasReferenceItems(project, target.id)]))
})
const activeRelatedNodeId = computed(() => {
  if (selectedNodeIds.value.size > 1) return null
  return hoveredNodeId.value || (selectedNodeIds.value.size === 1 ? [...selectedNodeIds.value][0] : null)
})
const configBusyNodeIds = computed<Set<string>>(() => {
  const project = activeProject.value
  const busy = new Set<string>()
  if (!project) return busy
  for (const config of project.nodes.filter((node) => node.type === 'config')) {
    const outputs = project.nodes.filter((node) => node.sourceNodeId === config.id && node.kind !== 'result')
    if (outputs.some((output) => output.status === 'generating' || project.nodes.some((result) => result.sourceNodeId === output.id && result.status === 'generating'))) {
      busy.add(config.id)
    }
  }
  return busy
})
const relatedCanvasNodeIds = computed<Set<string>>(() => {
  const project = activeProject.value
  const activeId = activeRelatedNodeId.value
  const ids = new Set<string>()
  if (!project || !activeId) return ids
  const addNode = (id: string) => {
    ids.add(id)
    const node = project.nodes.find((item) => item.id === id)
    if (node?.type === 'group') {
      for (const child of project.nodes) if (child.groupId === id) ids.add(child.id)
    }
  }
  addNode(activeId)
  for (const connection of project.connections) {
    if (connection.from !== activeId && connection.to !== activeId) continue
    addNode(connection.from)
    addNode(connection.to)
  }
  return ids
})
const relatedCanvasConnectionIds = computed<Set<string>>(() => {
  const project = activeProject.value
  const activeId = activeRelatedNodeId.value
  const ids = new Set<string>()
  if (!project || !activeId) return ids
  for (const connection of project.connections) {
    if (connection.from === activeId || connection.to === activeId) ids.add(connection.id)
  }
  return ids
})
const referenceConnectedNodeIds = computed<Set<string>>(() => {
  const project = activeProject.value
  const targetId = pluginReferenceTargetId.value
  const ids = new Set<string>()
  if (!project || !targetId) return ids
  ids.add(targetId)
  for (const connection of project.connections) {
    if (connection.to !== targetId) continue
    const source = project.nodes.find((node) => node.id === connection.from)
    if (!source) continue
    ids.add(source.id)
    if (source.type === 'group') {
      for (const child of project.nodes) if (child.groupId === source.id) ids.add(child.id)
    }
  }
  return ids
})
function canvasReferenceSelectionState(node: CanvasNodeData): 'target' | 'disabled' | 'available' | undefined {
  const targetId = pluginReferenceTargetId.value
  if (!targetId) return undefined
  if (node.id === targetId) return 'target'
  if (referenceConnectedNodeIds.value.has(node.id) || !isCanvasResourceNode(node, activeProject.value ?? undefined)) return 'disabled'
  return 'available'
}
const assistantContextNodes = computed(() => {
  const project = activeProject.value
  if (!project) return []
  const selected = selectedNode.value
  if (!selected) return []
  const incoming = project.connections
    .filter((connection) => connection.to === selected.id)
    .flatMap((connection) => {
      const source = project.nodes.find((node) => node.id === connection.from)
      return source?.type === 'group' ? expandCanvasGroupResourceNodes(source, project) : source ? [source] : []
    })
  return [selected, ...incoming.filter((node) => node.id !== selected.id)]
})
const assistantMentionReferences = computed<CanvasMentionReference[]>(() => {
  const seen = new Set<string>()
  return assistantContextNodes.value.flatMap((node) => {
    if (seen.has(node.id)) return []
    seen.add(node.id)
    return [{
      id: node.id,
      nodeId: node.id,
      kind: node.type === 'video' ? 'video' : node.type === 'audio' ? 'audio' : node.type === 'text' ? 'text' : node.type === 'group' ? 'group' : 'image',
      title: getCanvasNodeLabel(node),
      label: `@ ${getCanvasNodeLabel(node)}`,
      previewUrl: getCanvasNodePreviewUrl(node),
      text: getCanvasNodeText(node) || undefined,
    }]
  })
})
const builtInPrompts = computed<CanvasPromptEntry[]>(() => [
  { id: 'builtin-cinematic', title: t('playground.canvasPromptCinematic'), content: 'Create a cinematic still with intentional composition, soft directional light, realistic materials, and a clear focal subject.', tags: ['cinematic', 'lighting'], source: t('playground.canvasPromptBuiltIn') },
  { id: 'builtin-product', title: t('playground.canvasPromptProduct'), content: 'Create an editorial product photograph with controlled studio lighting, premium materials, subtle shadows, and a clean background.', tags: ['product', 'editorial'], source: t('playground.canvasPromptBuiltIn') },
  { id: 'builtin-character', title: t('playground.canvasPromptCharacter'), content: 'Design a production-ready character concept with a distinctive silhouette, expressive pose, costume details, and a neutral presentation.', tags: ['character', 'concept'], source: t('playground.canvasPromptBuiltIn') },
  { id: 'builtin-storyboard', title: t('playground.canvasPromptStoryboard'), content: 'Compose a storyboard frame that communicates the action, camera angle, subject placement, and environment at a glance.', tags: ['storyboard', 'composition'], source: t('playground.canvasPromptBuiltIn') },
])
const promptLibraryEntries = computed(() => [...customPrompts.value, ...remotePrompts.value, ...builtInPrompts.value])
const contextMenuNode = computed(() => contextMenu.value ? activeProject.value?.nodes.find((node) => node.id === contextMenu.value?.nodeId) ?? null : null)
const draftPath = computed(() => {
  const source = activeProject.value?.nodes.find((node) => node.id === connectingFrom.value)
  if (!source) return ''
  const target = activeProject.value?.nodes.find((node) => node.id === connectionTargetNodeId.value)
  const pointerX = (pointerPosition.value.x - (canvasRef.value?.getBoundingClientRect().left ?? 0) - (activeProject.value?.viewport.x ?? 0)) / (activeProject.value?.viewport.scale ?? 1)
  const pointerY = (pointerPosition.value.y - (canvasRef.value?.getBoundingClientRect().top ?? 0) - (activeProject.value?.viewport.y ?? 0)) / (activeProject.value?.viewport.scale ?? 1)
  const isSourceHandle = connectingHandleType.value === 'source'
  const startX = isSourceHandle ? source.x + source.width : target ? target.x + target.width : pointerX
  const startY = isSourceHandle ? source.y + source.height / 2 : target ? target.y + target.height / 2 : pointerY
  const endX = isSourceHandle ? target ? target.x : pointerX : source.x
  const endY = isSourceHandle ? target ? target.y + target.height / 2 : pointerY : source.y + source.height / 2
  const bend = Math.max(80, Math.abs(endX - startX) * 0.45)
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
})
// 与 canvasStore 的 MAX_REFERENCE_IMAGES 保持一致。
const MAX_REFERENCE_IMAGES = 4
// cacheKey -> blob object URL。参考图本体在 IndexedDB，这里只缓存当前会话的预览地址。
const referencePreviews = ref<Record<string, string>>({})
watch(selectedNode, (node) => {
  void loadReferencePreviews(node)
  textInstruction.value = ''
}, { immediate: true })
watch(() => activeProject.value?.id, () => {
  cancelAssistant()
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
  void publishAgentState()
})
watch(() => ({
  project: activeProject.value,
  selection: [...selectedNodeIds.value],
}), () => { void publishAgentState() }, { deep: true })
const canGenerate = computed(() => {
  const platform = playgroundKeys.value.find((key) => key.id === selectedKeyId.value)?.platform
  return !platform || platform === 'openai' || platform === 'grok'
})
const activeCanvasTool = computed<CanvasTool>(() => temporaryPanBaseTool.value ? 'pan' : tool.value)
const canvasCursor = computed(() => {
  if (dragging.value || panning.value || resizing.value) return 'cursor-grabbing'
  if (activeCanvasTool.value === 'pan') return 'cursor-grab'
  if (activeCanvasTool.value === 'connect') return 'cursor-crosshair'
  return 'cursor-default'
})
const selectionBoxStyle = computed(() => {
  const box = selectionBox.value
  if (!box) return {}
  return {
    left: `${Math.min(box.startX, box.currentX)}px`,
    top: `${Math.min(box.startY, box.currentY)}px`,
    width: `${Math.abs(box.currentX - box.startX)}px`,
    height: `${Math.abs(box.currentY - box.startY)}px`,
  }
})
const worldStyle = computed(() => {
  const viewport = activeProject.value?.viewport ?? { x: 0, y: 0, scale: 1 }
  return { transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.scale})`, width: '1px', height: '1px' }
})
const canvasSurfaceStyle = computed(() => {
  const dark = document.documentElement.classList.contains('dark')
  const background = activeProject.value?.backgroundMode ?? 'dots'
  const color = dark ? '#4b5563' : '#c5cbd5'
  const line = dark ? '#263244' : '#e4e7ec'
  if (background === 'blank') return { background: dark ? '#111827' : '#f7f8fa' }
  const size = 48 * (activeProject.value?.viewport.scale ?? 1)
  const image = background === 'dots'
    ? `radial-gradient(circle, ${color} 1px, transparent 1.4px)`
    : `linear-gradient(${line} 1px, transparent 1px), linear-gradient(90deg, ${line} 1px, transparent 1px)`
  return {
    backgroundColor: dark ? '#111827' : '#f7f8fa',
    backgroundImage: image,
    backgroundSize: `${size}px ${size}px`,
    backgroundPosition: `${(activeProject.value?.viewport.x ?? 0) % size}px ${(activeProject.value?.viewport.y ?? 0) % size}px`,
  }
})

function playgroundKeyLabel(key: PlaygroundKeySummary): string {
  return formatPlaygroundKeyLabel(key, t('playground.autoGroupKey', { strategy: key.autoGroupStrategy }))
}

function canvasSelectedKeyStorageKey(mode: 'chat' | 'image'): string {
  return userId.value === null ? '' : `sub2api.playground.v2.user.${userId.value}.selected-key.${mode}`
}

function canvasSelectedModelStorageKey(mode: 'chat' | 'image', keyId: number): string {
  return userId.value === null ? '' : `sub2api.playground.v2.user.${userId.value}.selected-model.${mode}.${keyId}`
}

function loadRememberedCanvasKeyId(mode: 'chat' | 'image'): number | null {
  if (userId.value === null) return null
  const value = Number(localStorage.getItem(canvasSelectedKeyStorageKey(mode)))
  return Number.isInteger(value) && value > 0 ? value : null
}

function rememberSelectedCanvasKeyId(mode: 'chat' | 'image', keyId: number | null): void {
  if (userId.value === null || keyId === null) return
  localStorage.setItem(canvasSelectedKeyStorageKey(mode), String(keyId))
}

function loadRememberedCanvasModel(mode: 'chat' | 'image', keyId: number): string {
  if (userId.value === null) return ''
  return localStorage.getItem(canvasSelectedModelStorageKey(mode, keyId))?.trim() ?? ''
}

function rememberSelectedCanvasModel(mode: 'chat' | 'image', keyId: number | null, model: string): void {
  if (userId.value === null || keyId === null) return
  const normalized = model.trim()
  if (!normalized) return
  localStorage.setItem(canvasSelectedModelStorageKey(mode, keyId), normalized)
}

function selectNode(id: string, event: PointerEvent): void {
  const referenceTargetId = pluginReferenceTargetId.value
  if (referenceTargetId && referenceTargetId !== id) {
    const project = activeProject.value
    const target = project?.nodes.find((node) => node.id === referenceTargetId)
    if (project && target && target.kind !== 'result' && !project.connections.some((connection) => connection.from === id && connection.to === referenceTargetId)) {
      canvasStore.checkpoint()
      canvasStore.addConnection({
        id: `connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        from: id,
        to: referenceTargetId,
        kind: 'reference',
      })
    }
    pluginReferenceTargetId.value = null
    selectedNodeId.value = referenceTargetId
    selectedNodeIds.value = new Set([referenceTargetId])
    return
  }
  selectedConnectionId.value = null
  selectedNodeId.value = id
  const additive = event.shiftKey || event.metaKey || event.ctrlKey
  if (additive) {
    const next = new Set(selectedNodeIds.value)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    selectedNodeIds.value = next
  } else {
    selectedNodeIds.value = new Set([id])
  }
  const selected = activeProject.value?.nodes.find((node) => node.id === id)
  if (selected?.kind !== 'result') lastReferenceTargetId.value = id
  if (tool.value !== 'select' && tool.value !== 'pan') return
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('input,textarea,select,[contenteditable="true"]')) return
  // Action controls inside a node should select it without stealing the
  // gesture as a node drag. This matters for config selectors, generation
  // buttons, media controls, and plugin embeds whose click handlers stop only
  // the bubbling click event.
  if (target?.closest('button,video,audio,iframe')) return
  const node = selected
  if (!node) return
  const runtimeDefinition = getCanvasPluginDefinition(node.type)
  if (!additive && (node.pluginUseBuiltinPanel || (runtimeDefinition?.autoOpenPanel && runtimeDefinition.Panel))) pluginPanelNodeId.value = node.id
  if (additive && !selectedNodeIds.value.has(id)) return
  const projectNodes = activeProject.value?.nodes ?? []
  const ids = expandDraggedNodeIds(Array.from(selectedNodeIds.value), projectNodes)
  const positions = Object.fromEntries(projectNodes.filter((item) => ids.includes(item.id)).map((item) => [item.id, { x: item.x, y: item.y }]))
  dragging.value = { nodeIds: ids, pointerX: event.clientX, pointerY: event.clientY, positions, moved: false }
  window.addEventListener('pointermove', handlePointerMove)
  window.addEventListener('pointerup', stopPointerAction, { once: true })
  window.addEventListener('pointercancel', stopPointerAction, { once: true })
}

function handleCanvasNodeDoubleClick(id: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || !isPluginNodeType(node.type)) return
  const definition = getCanvasPluginDefinition(node.type)
  try {
    if (definition?.onDoubleClick?.(createCanvasPluginContext(node))) return
  } catch (error) {
    console.warn('[canvas-plugin] double-click handler failed', error)
  }
  if (definition?.Panel && !definition.hidePanel) pluginPanelNodeId.value = id
}

function connectionDropTargetAtPointer(clientX: number, clientY: number): { nodeId: string | null; isNearNode: boolean } {
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  const anchorId = connectingFrom.value
  if (!project || !rect || !anchorId) return { nodeId: null, isNearNode: false }
  const scale = Math.max(project.viewport.scale, 0.05)
  const worldX = (clientX - rect.left - project.viewport.x) / scale
  const worldY = (clientY - rect.top - project.viewport.y) / scale
  const padding = 24 / scale
  const handleRadius = 24 / scale
  let bestNodeId: string | null = null
  let bestPriority = Number.POSITIVE_INFINITY
  let isNearNode = false
  const handleType = connectingHandleType.value
  for (const node of [...project.nodes].reverse()) {
    if (node.id === anchorId || node.kind === 'result' || (node.type === 'group' && handleType === 'source')) continue
    const valid = handleType === 'source'
      ? !(node.type === 'group')
      : node.type !== 'config'
    if (!valid) continue
    const anchorX = handleType === 'source' ? node.x : node.x + node.width
    const anchorY = node.y + node.height / 2
    const dx = worldX - anchorX
    const dy = worldY - anchorY
    const hitsHandle = dx * dx + dy * dy <= handleRadius * handleRadius
    const hitsInside = worldX >= node.x && worldX <= node.x + node.width && worldY >= node.y && worldY <= node.y + node.height
    const hitsExpanded = worldX >= node.x - padding && worldX <= node.x + node.width + padding && worldY >= node.y - padding && worldY <= node.y + node.height + padding
    if (!hitsHandle && !hitsInside && !hitsExpanded) continue
    isNearNode = true
    const from = handleType === 'source' ? anchorId : node.id
    const to = handleType === 'source' ? node.id : anchorId
    if (from === to || project.connections.some((connection) => connection.from === from && connection.to === to)) continue
    const priority = hitsInside ? 0 : hitsHandle ? 1 : 2
    if (priority < bestPriority) {
      bestPriority = priority
      bestNodeId = node.id
    }
  }
  return { nodeId: bestNodeId, isNearNode }
}

function setCanvasTool(value: CanvasTool): void {
  temporaryPanBaseTool.value = null
  tool.value = value
}

function readProjectsPanelCollapsed(): boolean {
  if (typeof localStorage === 'undefined') return false
  return localStorage.getItem(PROJECTS_PANEL_COLLAPSED_STORAGE_KEY) === 'true'
}

function readCanvasPanelWidth(): number {
  if (typeof localStorage === 'undefined') return 224
  const value = Number(localStorage.getItem(CANVAS_PANEL_WIDTH_STORAGE_KEY))
  return Number.isFinite(value) ? Math.min(CANVAS_PANEL_MAX_WIDTH, Math.max(CANVAS_PANEL_MIN_WIDTH, value)) : 224
}

function startCanvasPanelResize(event: PointerEvent): void {
  event.preventDefault()
  canvasPanelResize.value = { startX: event.clientX, startWidth: canvasPanelWidth.value }
  window.addEventListener('pointermove', handleCanvasPanelResize)
  window.addEventListener('pointerup', stopCanvasPanelResize, { once: true })
  window.addEventListener('pointercancel', stopCanvasPanelResize, { once: true })
}

function handleCanvasPanelResize(event: PointerEvent): void {
  const resize = canvasPanelResize.value
  if (!resize) return
  canvasPanelWidth.value = Math.min(CANVAS_PANEL_MAX_WIDTH, Math.max(CANVAS_PANEL_MIN_WIDTH, resize.startWidth + event.clientX - resize.startX))
}

function stopCanvasPanelResize(): void {
  if (!canvasPanelResize.value) return
  if (typeof localStorage !== 'undefined') localStorage.setItem(CANVAS_PANEL_WIDTH_STORAGE_KEY, String(Math.round(canvasPanelWidth.value)))
  canvasPanelResize.value = null
  window.removeEventListener('pointermove', handleCanvasPanelResize)
  window.removeEventListener('pointerup', stopCanvasPanelResize)
  window.removeEventListener('pointercancel', stopCanvasPanelResize)
}

function rememberProjectsPanelCollapsed(value: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(PROJECTS_PANEL_COLLAPSED_STORAGE_KEY, value ? 'true' : 'false')
}

function toggleProjectsPanel(): void {
  projectsPanelCollapsed.value = !projectsPanelCollapsed.value
  rememberProjectsPanelCollapsed(projectsPanelCollapsed.value)
}

function closeCanvasMenu(): void {
  canvasMenuOpen.value = false
}

function openCanvasProjectsFromMenu(): void {
  projectsPanelCollapsed.value = false
  rememberProjectsPanelCollapsed(false)
  closeCanvasMenu()
}

function newProjectFromMenu(): void {
  newProject()
  closeCanvasMenu()
}

function duplicateActiveProjectFromMenu(): void {
  duplicateActiveProject()
  closeCanvasMenu()
}

function importCanvasFromMenu(): void {
  importCanvas()
  closeCanvasMenu()
}

function exportCanvasFromMenu(): void {
  exportCanvas()
  closeCanvasMenu()
}

function updateCanvasBackgroundMode(mode: CanvasBackgroundMode): void {
  canvasStore.checkpoint()
  canvasStore.updateProject({ backgroundMode: mode })
}

function resetViewportCheckpointSession(): void {
  viewportCheckpointArmed = false
  if (viewportCheckpointResetTimer !== null) {
    window.clearTimeout(viewportCheckpointResetTimer)
    viewportCheckpointResetTimer = null
  }
}

function scheduleViewportCheckpointReset(): void {
  if (viewportCheckpointResetTimer !== null) window.clearTimeout(viewportCheckpointResetTimer)
  viewportCheckpointResetTimer = window.setTimeout(() => {
    viewportCheckpointArmed = false
    viewportCheckpointResetTimer = null
  }, VIEWPORT_CHECKPOINT_IDLE_MS)
}

function updateCanvasViewport(viewport: CanvasViewport, options?: { checkpoint?: boolean; continuous?: boolean }): void {
  if (options?.checkpoint) {
    if (options.continuous) {
      if (!viewportCheckpointArmed) {
        canvasStore.checkpoint()
        viewportCheckpointArmed = true
      }
      scheduleViewportCheckpointReset()
    } else {
      resetViewportCheckpointSession()
      canvasStore.checkpoint()
    }
  }
  canvasStore.updateViewport(viewport)
}

function updateCanvasViewportFromMiniMap(viewport: CanvasViewport): void {
  updateCanvasViewport(viewport, { checkpoint: true, continuous: true })
}

function applyPendingPan(): void {
  panningFrame = null
  const state = panning.value
  const pointer = pendingPanPointer
  if (!state || !pointer) return
  pendingPanPointer = null
  canvasStore.updateViewport({
    x: state.x + pointer.clientX - state.pointerX,
    y: state.y + pointer.clientY - state.pointerY,
    scale: activeProject.value?.viewport.scale ?? 1,
  })
}

function queuePan(pointer: { clientX: number; clientY: number }): void {
  pendingPanPointer = pointer
  if (panningFrame !== null) return
  if (typeof window.requestAnimationFrame === 'function') {
    panningFrame = window.requestAnimationFrame(applyPendingPan)
  } else {
    applyPendingPan()
  }
}

function cancelPendingPan(): void {
  if (panningFrame !== null && typeof window.cancelAnimationFrame === 'function') {
    window.cancelAnimationFrame(panningFrame)
  }
  panningFrame = null
  pendingPanPointer = null
}

function deleteProjectFromMenu(): void {
  const id = activeProject.value?.id
  if (id) deleteProject(id)
  closeCanvasMenu()
}

function handleCanvasPointerDown(event: PointerEvent): void {
  if ((event.target as Element | null)?.closest('[data-canvas-no-zoom]')) return
  closeContextMenu()
  canvasMenuOpen.value = false
  const target = event.target instanceof Element ? event.target : null
  const nodeElement = target?.closest<HTMLElement>('[data-node-id]')
  const connectionElement = target?.closest<HTMLElement>('[data-canvas-connection-hit]')
  // Match upstream's temporary-tool gesture: holding Ctrl/⌘ swaps select and
  // pan for this pointer gesture, while Space/V is already reflected by the
  // active tool computed above.
  const pointerTemporaryTool = (event.ctrlKey || event.metaKey) && !temporaryPanBaseTool.value
  const currentTool = pointerTemporaryTool
    ? (tool.value === 'select' ? 'pan' : 'select')
    : activeCanvasTool.value
  // Match the reference canvas gesture: the middle mouse button pans from
  // anywhere, including over nodes and embedded controls.
  if (event.button === 1) {
    const viewport = activeProject.value?.viewport
    if (!viewport) return
    event.preventDefault()
    panning.value = {
      pointerX: event.clientX,
      pointerY: event.clientY,
      x: viewport.x,
      y: viewport.y,
      moved: false,
      startedOnBackground: !nodeElement && !connectionElement,
    }
    capturedPointerId.value = event.pointerId
    canvasRef.value?.setPointerCapture?.(event.pointerId)
    window.addEventListener('pointermove', handlePointerMove)
    window.addEventListener('pointerup', stopPointerAction, { once: true })
    window.addEventListener('pointercancel', stopPointerAction, { once: true })
    return
  }
  if (nodeElement && currentTool !== 'connect' && !pointerTemporaryTool) {
    const nodeId = nodeElement.dataset.nodeId
    const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
    if (nodeId && node) {
      selectNode(nodeId, event)
    }
    return
  }
  const isBackgroundClick = !nodeElement && !connectionElement
  if ((currentTool === 'select' || currentTool === 'pan') && isBackgroundClick && !event.shiftKey && !event.metaKey && !event.ctrlKey) {
    selectedNodeIds.value = new Set()
    selectedNodeId.value = null
    selectedConnectionId.value = null
  }
  // In select mode, dragging the background creates a marquee selection just
  // like the reference app. Shift adds to the existing selection; a plain
  // click still clears it on pointer-up when the marquee has no area.
  if (event.button === 0 && currentTool === 'select' && isBackgroundClick) {
    const rect = canvasRef.value?.getBoundingClientRect()
    if (rect) {
      selectionBox.value = {
        startX: event.clientX - rect.left,
        startY: event.clientY - rect.top,
        currentX: event.clientX - rect.left,
        currentY: event.clientY - rect.top,
        additive: event.shiftKey,
        initialSelectedNodeIds: event.shiftKey ? new Set(selectedNodeIds.value) : new Set(),
      }
      if (!event.shiftKey) {
        selectedNodeIds.value = new Set()
        selectedNodeId.value = null
      }
      selectedConnectionId.value = null
      window.addEventListener('pointermove', handlePointerMove)
      window.addEventListener('pointerup', stopPointerAction, { once: true })
      window.addEventListener('pointercancel', stopPointerAction, { once: true })
      event.preventDefault()
      return
    }
  }
  if (event.button !== 0 && event.button !== 1) return
  const viewport = activeProject.value?.viewport
  if (!viewport) return
  if (event.button === 0 && (currentTool === 'connect' || !isBackgroundClick)) return
  if (event.button === 0 && currentTool !== 'pan') return
  panning.value = {
    pointerX: event.clientX,
    pointerY: event.clientY,
    x: viewport.x,
    y: viewport.y,
    moved: false,
    startedOnBackground: isBackgroundClick,
  }
  capturedPointerId.value = event.pointerId
  canvasRef.value?.setPointerCapture?.(event.pointerId)
  window.addEventListener('pointermove', handlePointerMove)
  window.addEventListener('pointerup', stopPointerAction, { once: true })
  window.addEventListener('pointercancel', stopPointerAction, { once: true })
}

function handleCanvasPointerCapture(event: PointerEvent): void {
  // Child nodes stop propagation for their own selection gesture. Capture the
  // middle-button gesture first so panning still works when the pointer starts
  // on a node, image, video, or editor control.
  if (event.button !== 1) return
  handleCanvasPointerDown(event)
  event.stopPropagation()
}

function handlePointerMove(event: PointerEvent): void {
  pointerPosition.value = { x: event.clientX, y: event.clientY }
  if (connectingFrom.value) {
    connectionTargetNodeId.value = connectionDropTargetAtPointer(event.clientX, event.clientY).nodeId
    return
  }
  if (resizing.value) {
    const scale = activeProject.value?.viewport.scale ?? 1
    const dx = (event.clientX - resizing.value.pointerX) / scale
    const dy = (event.clientY - resizing.value.pointerY) / scale
    const rightEdge = resizing.value.x + resizing.value.width
    const bottomEdge = resizing.value.y + resizing.value.height
    const rawWidth = Math.max(200, resizing.value.corner.includes('right') ? resizing.value.width + dx : resizing.value.width - dx)
    const rawHeight = Math.max(180, resizing.value.corner.includes('bottom') ? resizing.value.height + dy : resizing.value.height - dy)
    let nextWidth = rawWidth
    let nextHeight = rawHeight
    if (resizing.value.keepRatio) {
      if (Math.abs(dx) >= Math.abs(dy)) {
        nextHeight = Math.max(180, nextWidth / resizing.value.ratio)
        nextWidth = Math.max(200, nextHeight * resizing.value.ratio)
      } else {
        nextWidth = Math.max(200, nextHeight * resizing.value.ratio)
        nextHeight = Math.max(180, nextWidth / resizing.value.ratio)
      }
    }
    const nextX = resizing.value.corner.includes('left') ? rightEdge - nextWidth : resizing.value.x
    const nextY = resizing.value.corner.includes('top') ? bottomEdge - nextHeight : resizing.value.y
    canvasStore.updateNode(resizing.value.id, { x: nextX, y: nextY, width: nextWidth, height: nextHeight })
    return
  }
  if (selectionBox.value) {
    const rect = canvasRef.value?.getBoundingClientRect()
    if (rect) selectionBox.value = { ...selectionBox.value, currentX: event.clientX - rect.left, currentY: event.clientY - rect.top }
    return
  }
  if (dragging.value) {
    const deltaX = (event.clientX - dragging.value.pointerX) / (activeProject.value?.viewport.scale ?? 1)
    const deltaY = (event.clientY - dragging.value.pointerY) / (activeProject.value?.viewport.scale ?? 1)
    if (!dragging.value.moved && Math.hypot(event.clientX - dragging.value.pointerX, event.clientY - dragging.value.pointerY) < 3) return
    if (!dragging.value.moved) {
      canvasStore.checkpoint()
      dragging.value.moved = true
    }
    const project = activeProject.value
    dropTargetGroupId.value = project ? detectGroupDropTarget(dragging.value.nodeIds, deltaX, deltaY, project) : null
    for (const id of dragging.value.nodeIds) {
      const position = dragging.value.positions[id]
      if (position) canvasStore.updateNode(id, { x: position.x + deltaX, y: position.y + deltaY })
    }
    return
  }
  if (panning.value) {
    const deltaX = event.clientX - panning.value.pointerX
    const deltaY = event.clientY - panning.value.pointerY
    if (!panning.value.moved && Math.hypot(deltaX, deltaY) < 3) return
    if (!panning.value.moved) {
      canvasStore.checkpoint()
      panning.value.moved = true
    }
    queuePan({ clientX: event.clientX, clientY: event.clientY })
  }
}

function stopPointerAction(): void {
  if (selectionBox.value) finalizeSelectionBox()
  if (dragging.value?.moved && activeProject.value) finalizeDraggedNodeGroups(dragging.value.nodeIds, activeProject.value)
  if (panning.value) applyPendingPan()
  cancelPendingPan()
  dropTargetGroupId.value = null
  dragging.value = null
  panning.value = null
  resizing.value = null
  window.removeEventListener('pointermove', handlePointerMove)
  window.removeEventListener('pointerup', stopPointerAction)
  window.removeEventListener('pointercancel', stopPointerAction)
  if (capturedPointerId.value !== null) {
    try { canvasRef.value?.releasePointerCapture?.(capturedPointerId.value) } catch { /* Pointer capture may already be released. */ }
    capturedPointerId.value = null
  }
}

function startNodeResize(id: string, corner: 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right', event: PointerEvent): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.checkpoint()
  const keepRatio = node.type === 'video' || (node.type === 'image' && !node.freeResize)
  resizing.value = {
    id,
    corner,
    pointerX: event.clientX,
    pointerY: event.clientY,
    x: node.x,
    y: node.y,
    width: node.width,
    height: node.height,
    keepRatio,
    ratio: Math.max(0.05, node.width / Math.max(1, node.height)),
  }
  window.addEventListener('pointermove', handlePointerMove)
  window.addEventListener('pointerup', stopPointerAction, { once: true })
  window.addEventListener('pointercancel', stopPointerAction, { once: true })
}

function finalizeSelectionBox(): void {
  const box = selectionBox.value
  const project = activeProject.value
  if (!box || !project) {
    selectionBox.value = null
    return
  }
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!rect) {
    selectionBox.value = null
    return
  }
  const left = Math.min(box.startX, box.currentX)
  const right = Math.max(box.startX, box.currentX)
  const top = Math.min(box.startY, box.currentY)
  const bottom = Math.max(box.startY, box.currentY)
  const hits = project.nodes.filter((node) => {
    const nodeLeft = project.viewport.x + node.x * project.viewport.scale
    const nodeTop = project.viewport.y + node.y * project.viewport.scale
    const nodeRight = nodeLeft + node.width * project.viewport.scale
    const nodeBottom = nodeTop + node.height * project.viewport.scale
    return nodeRight >= left && nodeLeft <= right && nodeBottom >= top && nodeTop <= bottom
  }).map((node) => node.id)
  const nextSelection = box.additive
    ? new Set([...box.initialSelectedNodeIds, ...hits])
    : new Set(hits)
  selectedNodeIds.value = nextSelection
  selectedNodeId.value = nextSelection.values().next().value ?? null
  selectionBox.value = null
}

function startConnection(id: string, handleType: 'source' | 'target'): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  connectingFrom.value = id
  connectingHandleType.value = handleType
  connectionTargetNodeId.value = null
  selectedConnectionId.value = null
  window.addEventListener('pointermove', handlePointerMove)
  window.addEventListener('pointerup', finishConnectionAtPointer, { once: true })
  window.addEventListener('pointercancel', cancelConnection, { once: true })
}

function cancelConnection(): void {
  connectingFrom.value = null
  connectingHandleType.value = 'source'
  connectionTargetNodeId.value = null
  window.removeEventListener('pointermove', handlePointerMove)
  window.removeEventListener('pointerup', finishConnectionAtPointer)
  window.removeEventListener('pointercancel', cancelConnection)
}

function finishConnectionAtPointer(event: PointerEvent): void {
  const sourceId = connectingFrom.value
  if (!sourceId) return
  const dropTarget = connectionDropTargetAtPointer(event.clientX, event.clientY)
  const targetId = dropTarget.nodeId
  if (targetId) {
    finishConnection(targetId)
    return
  }
  const rect = canvasRef.value?.getBoundingClientRect()
  cancelConnection()
  if (dropTarget.isNearNode) {
    appStore.showError(t('playground.canvasConnectionFailed'))
    return
  }
  if (!rect) return
  pendingConnectionSource.value = sourceId
  pendingConnectionHandleType.value = connectingHandleType.value
  nodeCreatePosition.value = { x: event.clientX - rect.left, y: event.clientY - rect.top }
}

function finishConnection(id: string): void {
  const sourceId = connectingFrom.value
  const handleType = connectingHandleType.value
  cancelConnection()
  if (!sourceId || sourceId === id) {
    appStore.showError(t('playground.canvasConnectionFailed'))
    return
  }
  const anchor = activeProject.value?.nodes.find((node) => node.id === sourceId)
  const other = activeProject.value?.nodes.find((node) => node.id === id)
  if (!anchor || !other || other.kind === 'result' || anchor.kind === 'result' || (anchor.type === 'config' && other.type === 'config')) {
    appStore.showError(t('playground.canvasConnectionFailed'))
    return
  }
  const from = handleType === 'source' ? sourceId : id
  const to = handleType === 'source' ? id : sourceId
  if (activeProject.value?.connections.some((connection) => connection.from === from && connection.to === to)) {
    return
  }
  canvasStore.checkpoint()
  canvasStore.addConnection({
    id: `connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    from,
    to,
    kind: 'reference',
  })
}

function expandDraggedNodeIds(ids: string[], nodes: CanvasNodeData[]): string[] {
  const result = new Set(ids)
  for (const id of ids) {
    const node = nodes.find((candidate) => candidate.id === id)
    if (node?.type !== 'group') continue
    for (const child of nodes) {
      if (child.groupId === node.id) result.add(child.id)
    }
  }
  return Array.from(result)
}

function canvasNodeBounds(nodes: CanvasNodeData[]): { left: number; top: number; right: number; bottom: number } {
  return nodes.reduce((bounds, node) => ({
    left: Math.min(bounds.left, node.x),
    top: Math.min(bounds.top, node.y),
    right: Math.max(bounds.right, node.x + node.width),
    bottom: Math.max(bounds.bottom, node.y + node.height),
  }), { left: Infinity, top: Infinity, right: -Infinity, bottom: -Infinity })
}

function detectGroupDropTarget(nodeIds: string[], deltaX: number, deltaY: number, project: CanvasProject): string | null {
  const moved = new Set(nodeIds)
  if (project.nodes.some((node) => moved.has(node.id) && node.type === 'group')) return null
  const groups = project.nodes.filter((node) => node.type === 'group' && !moved.has(node.id))
  if (!groups.length) return null
  const draggedNodes = project.nodes.filter((node) => moved.has(node.id) && node.type !== 'group')
  const candidates = groups.filter((group) => draggedNodes.some((node) => {
    const base = dragging.value?.positions[node.id] ?? { x: node.x, y: node.y }
    const centerX = base.x + deltaX + node.width / 2
    const centerY = base.y + deltaY + node.height / 2
    return centerX >= group.x && centerX <= group.x + group.width && centerY >= group.y && centerY <= group.y + group.height
  }))
  return candidates.sort((a, b) => a.width * a.height - b.width * b.height)[0]?.id ?? null
}

function finalizeDraggedNodeGroups(nodeIds: string[], project: CanvasProject): void {
  const moved = new Set(nodeIds)
  const groups = project.nodes.filter((node) => node.type === 'group' && !moved.has(node.id))
  const movingNodes = project.nodes.filter((item) => moved.has(item.id) && item.type !== 'group')
  if (!movingNodes.length || project.nodes.some((node) => moved.has(node.id) && node.type === 'group')) return
  const targetGroup = groups
    .filter((group) => movingNodes.some((node) => {
      const centerX = node.x + node.width / 2
      const centerY = node.y + node.height / 2
      return centerX >= group.x && centerX <= group.x + group.width && centerY >= group.y && centerY <= group.y + group.height
    }))
    .sort((a, b) => a.width * a.height - b.width * b.height)[0]
  if (targetGroup) {
    const bounds = canvasNodeBounds(movingNodes)
    const pad = 24
    const left = targetGroup.x + pad
    const top = targetGroup.y + pad
    const right = targetGroup.x + targetGroup.width - pad
    const bottom = targetGroup.y + targetGroup.height - pad
    const dx = bounds.right - bounds.left > right - left ? left - bounds.left : bounds.left < left ? left - bounds.left : bounds.right > right ? right - bounds.right : 0
    const dy = bounds.bottom - bounds.top > bottom - top ? top - bounds.top : bounds.top < top ? top - bounds.top : bounds.bottom > bottom ? bottom - bounds.bottom : 0
    for (const node of movingNodes) canvasStore.updateNode(node.id, { x: node.x + dx, y: node.y + dy, groupId: targetGroup.id })
    return
  }
  for (const node of movingNodes) {
    const centerX = node.x + node.width / 2
    const centerY = node.y + node.height / 2
    const parent = groups
      .filter((group) => centerX >= group.x && centerX <= group.x + group.width && centerY >= group.y && centerY <= group.y + group.height)
      .sort((a, b) => a.width * a.height - b.width * b.height)[0]
    if ((node.groupId ?? '') !== (parent?.id ?? '')) canvasStore.updateNode(node.id, { groupId: parent?.id })
  }
}

function deleteConnection(id: string): void {
  canvasStore.checkpoint()
  canvasStore.removeConnection(id)
  if (selectedConnectionId.value === id) selectedConnectionId.value = null
  if (connectionContextMenu.value?.connectionId === id) connectionContextMenu.value = null
}

function selectConnection(id: string): void {
  if (!activeProject.value?.connections.some((connection) => connection.id === id)) return
  contextMenu.value = null
  connectionContextMenu.value = null
  selectedConnectionId.value = id
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
}

function startPluginReferenceSelection(targetId: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === targetId)
  if (!node || node.kind === 'result') return
  pluginReferenceTargetId.value = targetId
  selectedNodeId.value = targetId
  selectedNodeIds.value = new Set([targetId])
  tool.value = 'select'
}

function cancelPluginReferenceSelection(): void {
  pluginReferenceTargetId.value = null
  hoveredNodeId.value = null
}

function selectCanvasReference(sourceId: string): void {
  const project = activeProject.value
  const targetId = pluginReferenceTargetId.value
  if (!project || !targetId || sourceId === targetId || referenceConnectedNodeIds.value.has(sourceId)) return
  const source = project.nodes.find((node) => node.id === sourceId)
  const target = project.nodes.find((node) => node.id === targetId)
  if (!source || !target || target.kind === 'result' || !isCanvasResourceNode(source, project)) return
  canvasStore.checkpoint()
  canvasStore.addConnection({
    id: `connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    from: sourceId,
    to: targetId,
    kind: 'reference',
  })
  pluginReferenceTargetId.value = null
  selectedNodeId.value = targetId
  selectedNodeIds.value = new Set([targetId])
}

function disconnectPluginReference(targetId: string, sourceId: string): void {
  const connections = activeProject.value?.connections.filter((connection) => connection.from === sourceId && connection.to === targetId) ?? []
  if (!connections.length) return
  canvasStore.checkpoint()
  for (const connection of connections) canvasStore.removeConnection(connection.id)
}

async function handleCanvasDrop(event: DragEvent): Promise<void> {
  const files = Array.from(event.dataTransfer?.files ?? []).filter((file) => file.type.startsWith('image/') || file.type.startsWith('video/') || file.type.startsWith('audio/')).slice(0, 8)
  return importCanvasFiles(files, event.clientX, event.clientY, 'drop')
}

async function importCanvasFiles(files: File[], clientX: number, clientY: number, source: 'drop' | 'paste'): Promise<void> {
  const rect = canvasRef.value?.getBoundingClientRect()
  const project = activeProject.value
  if (!files.length || !rect || !project) return
  canvasStore.checkpoint()
  const baseX = (clientX - rect.left - project.viewport.x) / project.viewport.scale - 130
  const baseY = (clientY - rect.top - project.viewport.y) / project.viewport.scale - 160
  const created: string[] = []
  for (const [index, file] of files.entries()) {
    const isVideo = file.type.startsWith('video/')
    const isAudio = file.type.startsWith('audio/')
    const dataUrl = isVideo || isAudio ? '' : await readFileAsDataUrl(file)
    const now = Date.now() + index
    let imageUrl = dataUrl
    let imageCacheKey: string | undefined
    let videoUrl: string | undefined
    let audioUrl: string | undefined
    let audioCacheKey: string | undefined
    if (isVideo || isAudio) {
      const mediaUrl = URL.createObjectURL(file)
      imageObjectUrls.add(mediaUrl)
      if (isAudio) {
        audioUrl = mediaUrl
        if (userId.value && selectedKeyId.value) {
          audioCacheKey = createCanvasMediaCacheKey(userId.value, selectedKeyId.value, `${source}-audio-${now}`)
          await cacheCanvasMedia(audioCacheKey, file)
        }
      } else {
        videoUrl = mediaUrl
      }
      imageUrl = ''
      if (isAudio) videoUrl = undefined
    } else if (userId.value && selectedKeyId.value) {
      imageCacheKey = createPlaygroundImageCacheKey(userId.value, selectedKeyId.value, `${source}-${now}`, 0)
      const cached = await cachePlaygroundImage(imageCacheKey, userId.value, selectedKeyId.value, dataUrl, { prompt: file.name, model: 'canvas-upload' })
      if (cached) imageUrl = cached
    }
    const node: CanvasNodeData = {
      id: `upload-${now}-${Math.random().toString(36).slice(2, 8)}`,
      type: isVideo ? 'video' : isAudio ? 'audio' : 'image', kind: 'result', x: baseX + (index % 3) * 300, y: baseY + Math.floor(index / 3) * 340,
      width: 260, height: isAudio ? 220 : 320, prompt: file.name, model: 'canvas-upload', imageUrl, imageCacheKey,
      videoUrl, audioUrl, audioCacheKey, imageUrls: imageUrl ? [imageUrl] : undefined, status: 'success', resultIndex: 0, createdAt: now, updatedAt: now,
    }
    canvasStore.addNode(node)
    created.push(node.id)
  }
  selectedNodeIds.value = new Set(created)
  selectedNodeId.value = created[0] ?? null
}

async function uploadImageToNode(id: string, file: File): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.type !== 'image' || node.kind === 'result' || !file.type.startsWith('image/')) return
  try {
    const dataUrl = await readFileAsDataUrl(file)
    const owner = userId.value
    const keyId = selectedKeyId.value
    const now = Date.now()
    const cacheKey = owner && keyId
      ? createPlaygroundImageCacheKey(owner, keyId, `${node.id}-upload-${now}`, 0)
      : undefined
    const cachedUrl = cacheKey && owner && keyId
      ? await cachePlaygroundImage(cacheKey, owner, keyId, dataUrl, { prompt: node.prompt || file.name, model: node.model || 'canvas-upload' })
      : undefined
    const imageUrl = cachedUrl || dataUrl
    if (imageUrl.startsWith('blob:')) imageObjectUrls.add(imageUrl)
    canvasStore.checkpoint()
    canvasStore.updateNode(id, {
      prompt: node.prompt.trim() || file.name,
      model: node.model || 'canvas-upload',
      imageUrl,
      imageUrls: [imageUrl],
      imageCacheKey: cacheKey,
      imageCacheKeys: cacheKey ? [cacheKey] : undefined,
      imageVariants: [{ id: `${id}-image-${now}`, status: 'success', url: imageUrl, cacheKey }],
      primaryImageIndex: 0,
      imageCount: 1,
      status: 'success',
      errorMessage: undefined,
    })
    selectedNodeId.value = id
    selectedNodeIds.value = new Set([id])
  } catch {
    appStore.showError(t('playground.canvasUploadFailed'))
  }
}

async function uploadMediaToNode(id: string, file: File, type: 'video' | 'audio'): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.type !== type || !file.type.startsWith(`${type}/`)) return
  try {
    const owner = userId.value
    const keyId = selectedKeyId.value
    const now = Date.now()
    const cacheKey = owner && keyId
      ? createCanvasMediaCacheKey(owner, keyId, `${node.id}-${type}-upload-${now}`)
      : undefined
    const cachedUrl = cacheKey
      ? await cacheCanvasMedia(cacheKey, file)
      : null
    const mediaUrl = cachedUrl || URL.createObjectURL(file)
    const persistedCacheKey = cachedUrl ? cacheKey : undefined
    const previousUrl = type === 'video' ? node.videoUrl : node.audioUrl
    if (previousUrl && imageObjectUrls.has(previousUrl)) {
      URL.revokeObjectURL(previousUrl)
      imageObjectUrls.delete(previousUrl)
    }
    imageObjectUrls.add(mediaUrl)
    canvasStore.checkpoint()
    canvasStore.updateNode(id, {
      prompt: node.prompt.trim() || file.name,
      model: node.model || 'canvas-upload',
      videoUrl: type === 'video' ? mediaUrl : undefined,
      videoCacheKey: type === 'video' ? persistedCacheKey : undefined,
      audioUrl: type === 'audio' ? mediaUrl : undefined,
      audioCacheKey: type === 'audio' ? persistedCacheKey : undefined,
      status: 'success',
      errorMessage: undefined,
    })
    selectedNodeId.value = id
    selectedNodeIds.value = new Set([id])
  } catch {
    appStore.showError(t('playground.canvasMediaUploadFailed'))
  }
}

async function uploadVideoToNode(id: string, file: File): Promise<void> {
  return uploadMediaToNode(id, file, 'video')
}

async function uploadAudioToNode(id: string, file: File): Promise<void> {
  return uploadMediaToNode(id, file, 'audio')
}

function serializeSelectedNodes(): string | null {
  const nodes = (activeProject.value?.nodes ?? []).filter((node) => selectedNodeIds.value.has(node.id))
  if (!nodes.length) return null
  const ids = new Set(nodes.map((node) => node.id))
  const connections = (activeProject.value?.connections ?? []).filter((connection) => ids.has(connection.from) && ids.has(connection.to))
  return JSON.stringify({ nodes, connections })
}

function buildCanvasExportState(selection: Set<string> = selectedNodeIds.value): CanvasPersistedState {
  const state = JSON.parse(canvasStore.exportState()) as CanvasPersistedState
  if (!selection.size) return state
  const project = activeProject.value
  if (!project) return state
  const ids = new Set(selection)
  const selectedConnection = selectedConnectionId.value ? project.connections.find((connection) => connection.id === selectedConnectionId.value) : null
  if (selectedConnection) {
    ids.add(selectedConnection.from)
    ids.add(selectedConnection.to)
  }
  const nodes = project.nodes.filter((node) => ids.has(node.id))
  if (!nodes.length) return state
  const connections = project.connections.filter((connection) => ids.has(connection.from) && ids.has(connection.to))
  const selectedProject: CanvasProject = { ...project, nodes, connections }
  return { ...state, projects: [selectedProject], activeProjectId: selectedProject.id }
}

function copySelectedNodes(): void {
  const serialized = serializeSelectedNodes()
  if (!serialized) return
  clipboard.value = serialized
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    void navigator.clipboard.writeText(serialized).catch(() => undefined)
  }
}

function parseCanvasClipboard(value: string): { nodes: CanvasNodeData[]; connections?: CanvasProject['connections'] } | null {
  try {
    const parsed = JSON.parse(value) as { nodes?: CanvasNodeData[]; connections?: CanvasProject['connections'] }
    if (!Array.isArray(parsed.nodes) || !parsed.nodes.length) return null
    return { nodes: parsed.nodes, connections: parsed.connections }
  } catch {
    return null
  }
}

async function pasteNodes(externalText?: string): Promise<void> {
  const candidates = clipboard.value ? [clipboard.value] : []
  if (externalText && externalText !== clipboard.value) candidates.push(externalText)
  if (!externalText && typeof navigator !== 'undefined' && navigator.clipboard?.readText) {
    try {
      const external = await navigator.clipboard.readText()
      if (external && external !== clipboard.value) candidates.push(external)
    } catch { /* Browser permissions may reject clipboard reads. */ }
  }
  const parsed = candidates.map((value) => value ? parseCanvasClipboard(value) : null).find(Boolean)
  if (!parsed) {
    const plainText = candidates.map((value) => value?.trim() ?? '').find(Boolean)
    if (!plainText) return
    const rect = canvasRef.value?.getBoundingClientRect()
    const project = activeProject.value
    if (!rect || !project) return
    const { width, height } = nodeDimensions('text')
    const clientX = pointerPosition.value.x || (rect.left + rect.width / 2)
    const clientY = pointerPosition.value.y || (rect.top + rect.height / 2)
    const nodeId = addNode((clientX - rect.left - project.viewport.x) / project.viewport.scale - width / 2, (clientY - rect.top - project.viewport.y) / project.viewport.scale - height / 2, 'text')
    canvasStore.updateNode(nodeId, { prompt: plainText, textContent: plainText, status: 'idle' })
    return
  }
  try {
    canvasStore.checkpoint()
    const idMap = new Map<string, string>()
    const newIds: string[] = []
    for (const source of parsed.nodes) {
      const id = `paste-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
      idMap.set(source.id, id)
      const node = { ...source, id, x: source.x + 48, y: source.y + 48, imageUrls: source.imageUrls ? [...source.imageUrls] : undefined, imageVariants: source.imageVariants ? source.imageVariants.map((item) => ({ ...item })) : undefined, referenceImages: source.referenceImages ? source.referenceImages.map((item) => ({ ...item })) : undefined, createdAt: Date.now(), updatedAt: Date.now() }
      canvasStore.addNode(node)
      newIds.push(id)
    }
    for (const connection of parsed.connections ?? []) {
      const from = idMap.get(connection.from)
      const to = idMap.get(connection.to)
      if (from && to) canvasStore.addConnection({ ...connection, id: `connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, from, to })
    }
    selectedNodeIds.value = new Set(newIds)
    selectedNodeId.value = newIds[0] ?? null
  } catch { /* Clipboard is an internal, best-effort payload. */ }
}

async function handlePaste(event: ClipboardEvent): Promise<void> {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('input,textarea,select,[contenteditable="true"]')) return
  const files = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith('image/') || file.type.startsWith('video/') || file.type.startsWith('audio/')).slice(0, 8)
  const text = event.clipboardData?.getData('text/plain') ?? ''
  if (!files.length && !text) return
  event.preventDefault()
  if (files.length) {
    const rect = canvasRef.value?.getBoundingClientRect()
    const clientX = pointerPosition.value.x || (rect ? rect.left + rect.width / 2 : 0)
    const clientY = pointerPosition.value.y || (rect ? rect.top + rect.height / 2 : 0)
    await importCanvasFiles(files, clientX, clientY, 'paste')
    return
  }
  await pasteNodes(text)
}

function handleWheel(event: WheelEvent): void {
  if ((event.target as Element | null)?.closest('[data-canvas-no-zoom]')) return
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !rect) return
  const factor = Math.pow(1.1, -event.deltaY / 100)
  const nextScale = Math.min(3, Math.max(0.2, project.viewport.scale * factor))
  const mouseX = event.clientX - rect.left
  const mouseY = event.clientY - rect.top
  const worldX = (mouseX - project.viewport.x) / project.viewport.scale
  const worldY = (mouseY - project.viewport.y) / project.viewport.scale
  updateCanvasViewport({ x: mouseX - worldX * nextScale, y: mouseY - worldY * nextScale, scale: nextScale }, { checkpoint: true, continuous: true })
}

function zoomBy(factor: number): void {
  const project = activeProject.value
  if (!project) return
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!rect) return
  const centerX = rect.width / 2
  const centerY = rect.height / 2
  const nextScale = Math.min(3, Math.max(0.2, project.viewport.scale * factor))
  const worldX = (centerX - project.viewport.x) / project.viewport.scale
  const worldY = (centerY - project.viewport.y) / project.viewport.scale
  updateCanvasViewport({ x: centerX - worldX * nextScale, y: centerY - worldY * nextScale, scale: nextScale }, { checkpoint: true })
}

function setZoomPercent(percent: number): void {
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !rect || !Number.isFinite(percent)) return
  const centerX = rect.width / 2
  const centerY = rect.height / 2
  const nextScale = Math.min(3, Math.max(0.2, percent / 100))
  const worldX = (centerX - project.viewport.x) / project.viewport.scale
  const worldY = (centerY - project.viewport.y) / project.viewport.scale
  updateCanvasViewport({ x: centerX - worldX * nextScale, y: centerY - worldY * nextScale, scale: nextScale }, { checkpoint: true, continuous: true })
}

function handleCanvasDoubleClick(event: MouseEvent): void {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('[data-node-id],[data-canvas-no-zoom]')) return
  const rect = canvasRef.value?.getBoundingClientRect()
  const project = activeProject.value
  if (!rect || !project) return
  pendingConnectionSource.value = null
  pendingConnectionHandleType.value = 'source'
  nodeCreatePosition.value = { x: event.clientX - rect.left, y: event.clientY - rect.top }
}

const nodeCreateOptions = computed(() => [
  { type: 'text' as const, icon: 'document' as const, label: t('playground.canvasAddText'), description: t('playground.canvasTextPlaceholder') },
  { type: 'image' as const, icon: 'sparkles' as const, label: t('playground.canvasAddNode'), description: t('playground.canvasTextToImage') },
  { type: 'video' as const, icon: 'play' as const, label: t('playground.canvasAddVideo'), description: t('playground.canvasVideoNode') },
  { type: 'audio' as const, icon: 'chatBubble' as const, label: t('playground.canvasAddAudio'), description: t('playground.canvasAudioNode') },
  { type: 'group' as const, icon: 'grid' as const, label: t('playground.canvasAddGroup'), description: t('playground.canvasGroupHint') },
  { type: 'config' as const, icon: 'cog' as const, label: t('playground.canvasAddConfig'), description: t('playground.canvasConfigNode') },
  ...plugins.value.filter((plugin) => plugin.enabled).flatMap((plugin) => plugin.nodes.filter((node) => node.showInCreateMenu !== false).map((node) => ({ type: node.type as CanvasNodeData['type'], icon: 'cube' as const, label: node.title, description: node.description || plugin.name }))),
])

const canvasShortcuts = computed(() => [
  { key: 'Space / V', label: t('playground.canvasShortcutTemporaryPan') },
  { key: 'Mouse Wheel', label: t('playground.canvasShortcutZoom') },
  { key: 'Drag', label: t('playground.canvasShortcutBoxSelect') },
  { key: 'Ctrl / ⌘', label: t('playground.canvasShortcutTemporaryPan') },
  { key: 'Ctrl / ⌘ + A', label: t('playground.canvasShortcutSelectAll') },
  { key: 'Ctrl / ⌘ + C / V', label: t('playground.canvasShortcutCopyPaste') },
  { key: 'Ctrl / ⌘ + D', label: t('playground.canvasShortcutDuplicate') },
  { key: 'Delete / Backspace', label: t('playground.canvasShortcutDelete') },
  { key: 'Esc', label: t('playground.canvasShortcutEscape') },
])

function pluginDefinition(type: CanvasNodeData['type']) {
  for (const plugin of plugins.value) {
    const definition = plugin.nodes.find((node) => node.type === type)
    if (definition) return { plugin, definition }
  }
  return null
}

async function pluginReferenceAttachments(references?: string[]): Promise<PlaygroundAttachment[]> {
  const attachments: PlaygroundAttachment[] = []
  for (const [index, source] of (references ?? []).slice(0, 8).entries()) {
    const value = source.trim()
    if (!value) continue
    try {
      const response = await fetch(value)
      if (!response.ok) continue
      const blob = await response.blob()
      if (!blob.type.startsWith('image/')) continue
      const objectUrl = URL.createObjectURL(blob)
      const dataUrl = await objectUrlToDataUrl(objectUrl)
      URL.revokeObjectURL(objectUrl)
      if (!dataUrl) continue
      attachments.push({ id: `plugin-reference-${index}`, name: `plugin-reference-${index + 1}`, mimeType: blob.type, size: blob.size, dataUrl })
    } catch {
      // Ignore unavailable plugin references; generation can still use the prompt.
    }
  }
  return attachments
}

function createCanvasPluginContext(node: CanvasNodeData): CanvasPluginRuntimeContext {
  const project = activeProject.value
  const connections = project?.connections ?? []
  const upstream = () => (project?.connections ?? [])
    .filter((edge) => edge.to === node.id)
    .flatMap((edge) => {
      const source = project?.nodes.find((candidate) => candidate.id === edge.from)
      return source?.type === 'group' && project ? expandCanvasGroupResourceNodes(source, project) : source ? [source] : []
    })
  const downstream = () => (project?.nodes ?? []).filter((candidate) => connections.some((edge) => edge.from === node.id && edge.to === candidate.id))
  const metadata = { ...(node.pluginMetadata ?? {}), content: node.pluginMetadata?.content ?? node.prompt }
  const pluginModelOptions = (capability?: 'image' | 'video' | 'text' | 'audio') => {
    const source = capability === 'image' ? models.value : capability === 'video' ? videoModels.value : capability === 'text' ? textModels.value : models.value
    return source.map((model) => ({ value: model.id, label: model.id }))
  }
  const pluginDefaultModel = (capability?: 'image' | 'video' | 'text' | 'audio') => {
    if (capability === 'image') return selectedModel.value
    if (capability === 'video') return videoModels.value[0]?.id ?? ''
    return selectedTextModel.value
  }
  const pluginImageGeneration = async (prompt: string, options?: { signal?: AbortSignal; references?: string[]; model?: string; count?: number; size?: string; quality?: string; background?: string }): Promise<{ images: string[] }> => {
    const keyId = selectedKeyId.value
    const model = options?.model?.trim() || selectedModel.value
    if (!keyId || !model) throw new Error('未配置可用的图片模型或 API Key。')
    const referenceImages = await pluginReferenceAttachments(options?.references)
    const request = buildPlaygroundImageRequest({ model, prompt, size: options?.size || '1024x1024', quality: options?.quality || 'auto', imageCount: options?.count ?? 1, outputFormat: 'png', outputCompression: 90, background: options?.background || 'auto', moderation: 'auto', style: 'auto', inputFidelity: 'auto', hasReferenceImages: referenceImages.length > 0 })
    const images: string[] = []
    for (let index = 0; index < request.requestedImageCount; index += 1) {
      const response = await sendPlaygroundImageGeneration(keyId, request.body, { signal: options?.signal, referenceImages })
      response.data.forEach((result) => {
        const url = playgroundImageUrl(result, 'png')
        if (url && images.length < request.requestedImageCount) images.push(url)
      })
    }
    return { images }
  }
  const pluginVideoGeneration = async (prompt: string, options?: { signal?: AbortSignal; references?: string[]; model?: string; size?: string; seconds?: string; aspectRatio?: string }): Promise<{ url: string; mimeType: string }> => {
    const keyId = selectedKeyId.value
    const model = options?.model?.trim() || videoModels.value[0]?.id
    if (!keyId || !model) throw new Error('未配置可用的视频模型或 API Key。')
    const referenceImage = (await pluginReferenceAttachments(options?.references))[0]?.dataUrl
    const response = await sendPlaygroundVideoGeneration(keyId, {
      model,
      prompt,
      resolution: options?.size || '720p',
      duration: Number(options?.seconds || 8),
      aspect_ratio: options?.aspectRatio || '16:9',
      ...(referenceImage ? { image: { url: referenceImage, type: 'image_url' } } : {}),
    }, { signal: options?.signal })
    if (response.video_url || response.url) return { url: response.video_url || response.url || '', mimeType: 'video/mp4' }
    for (let attempt = 0; attempt < 90; attempt += 1) {
      if (options?.signal?.aborted) throw new DOMException('Aborted', 'AbortError')
      await new Promise((resolve) => window.setTimeout(resolve, 2000))
      const status = await fetchPlaygroundVideo(keyId, response.id, { signal: options?.signal })
      const statusValue = (status.status || '').toLowerCase()
      if (status.video_url || status.url || ['completed', 'done', 'succeeded', 'success'].includes(statusValue)) {
        const content = await fetchPlaygroundVideoContent(keyId, response.id, { signal: options?.signal })
        return { url: URL.createObjectURL(content), mimeType: content.type || 'video/mp4' }
      }
      if (['failed', 'error', 'cancelled', 'canceled'].includes(statusValue)) throw new Error('视频生成失败。')
    }
    throw new Error('视频生成超时。')
  }
  const pluginTextGeneration = async (prompt: string, options?: { signal?: AbortSignal; model?: string; system?: string; onDelta?: (text: string) => void }): Promise<{ text: string }> => {
    const keyId = selectedTextKeyId.value
    const model = options?.model?.trim() || selectedTextModel.value
    if (!keyId || !model) throw new Error('未配置可用的文本模型或 API Key。')
    const messages = [...(options?.system?.trim() ? [{ role: 'system', content: options.system.trim() }] : []), { role: 'user', content: prompt }]
    const result = await sendPlaygroundChat(keyId, { model, messages, stream: true }, { signal: options?.signal ?? new AbortController().signal, onDelta: (delta) => options?.onDelta?.(delta.contentDelta) })
    return { text: result.content }
  }
  const pluginAudioGeneration = async (prompt: string, options?: { signal?: AbortSignal; model?: string; voice?: string; language?: string }): Promise<{ url: string; mimeType: string }> => {
    const keyId = selectedTextKeyId.value
    if (!keyId) throw new Error('未配置可用的 API Key。')
    const blob = await sendPlaygroundSpeech(keyId, { model: options?.model?.trim() || selectedTextModel.value || 'grok-voice-latest', input: prompt, voice: options?.voice || 'Ara', response_format: 'mp3', language: options?.language || 'en' }, { signal: options?.signal })
    return { url: URL.createObjectURL(blob), mimeType: blob.type || 'audio/mpeg' }
  }
  return {
    node: { id: node.id, type: node.type, title: node.pluginTitle || node.prompt || node.type, metadata },
    theme: { node: { fill: '#ffffff', text: '#1f2937', stroke: '#d1d5db', placeholder: '#9ca3af' }, toolbar: { panel: '#ffffff', activeBg: '#ccfbf1', activeText: '#0f766e' } },
    scale: project?.viewport.scale ?? 1,
    isSelected: selectedNodeIds.value.has(node.id),
    updateMetadata: (patch) => updateNodePlugin(node.id, patch),
    updateNode: (patch) => canvasStore.updateNode(node.id, { ...(patch.title !== undefined ? { pluginTitle: patch.title, prompt: patch.title } : {}), ...(patch.width !== undefined ? { width: patch.width } : {}), ...(patch.height !== undefined ? { height: patch.height } : {}) }),
    getNode: (id) => activeProject.value?.nodes.find((candidate) => candidate.id === id) ?? null,
    getNodes: () => activeProject.value?.nodes ?? [],
    getConnections: () => activeProject.value?.connections ?? [],
    getUpstream: upstream,
    getDownstream: downstream,
    applyOps: (ops) => applyCanvasAgentOperations(ops),
    emit: (event, payload) => window.dispatchEvent(new CustomEvent(`canvas-plugin:${event}`, { detail: payload })),
    on: (event, handler) => {
      const listener = (value: Event) => handler((value as CustomEvent).detail)
      window.addEventListener(`canvas-plugin:${event}`, listener)
      return () => window.removeEventListener(`canvas-plugin:${event}`, listener)
    },
    ai: { generateImage: pluginImageGeneration, generateVideo: pluginVideoGeneration, generateText: pluginTextGeneration, generateAudio: pluginAudioGeneration, listModels: pluginModelOptions, defaultModel: pluginDefaultModel },
    openPanel: () => { selectedNodeId.value = node.id; selectedNodeIds.value = new Set([node.id]); pluginPanelNodeId.value = node.id },
    closePanel: () => { if (pluginPanelNodeId.value === node.id) pluginPanelNodeId.value = null },
    storage: createPluginStorage(node.pluginId || node.type.split(':')[0]),
  }
}

function nodeDimensions(type: CanvasNodeData['type']): { width: number; height: number } {
  const plugin = pluginDefinition(type)
  if (plugin) return { width: plugin.definition.defaultSize?.width ?? 360, height: plugin.definition.defaultSize?.height ?? 300 }
  return { width: type === 'text' ? 340 : type === 'config' ? 300 : type === 'group' ? 760 : 360, height: type === 'text' ? 300 : type === 'config' ? 260 : type === 'audio' ? 220 : type === 'group' ? 480 : 420 }
}

function createNodeFromMenu(type: CanvasNodeData['type']): void {
  const position = nodeCreatePosition.value
  const rect = canvasRef.value?.getBoundingClientRect()
  const project = activeProject.value
  if (!position || !rect || !project) return
  const { width, height } = nodeDimensions(type)
  const nodeId = addNode((position.x - project.viewport.x) / project.viewport.scale - width / 2, (position.y - project.viewport.y) / project.viewport.scale - height / 2, type)
  if (pendingConnectionSource.value) {
    const from = pendingConnectionHandleType.value === 'source' ? pendingConnectionSource.value : nodeId
    const to = pendingConnectionHandleType.value === 'source' ? nodeId : pendingConnectionSource.value
    if (from !== to && !(type === 'config' && project.nodes.find((node) => node.id === pendingConnectionSource.value)?.type === 'config')) {
      canvasStore.addConnection({ id: `connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, from, to, kind: 'reference' })
    }
    pendingConnectionSource.value = null
    pendingConnectionHandleType.value = 'source'
  }
  nodeCreatePosition.value = null
}

function addNode(x = 120, y = 120, type: CanvasNodeData['type'] = 'image'): string {
  canvasStore.checkpoint()
  const now = Date.now()
  const node: CanvasNodeData = {
    id: `${type}-${now}-${Math.random().toString(36).slice(2, 8)}`,
    type,
    kind: 'generator',
    x,
    y,
    width: nodeDimensions(type).width,
    height: nodeDimensions(type).height,
    prompt: type === 'group' ? t('playground.canvasGroupNode') : '',
    textContent: type === 'text' ? '' : undefined,
    textCount: type === 'text' ? 1 : undefined,
    model: type.includes(':') ? 'canvas-plugin' : type === 'text' || type === 'audio' ? selectedTextModel.value : type === 'video' ? (videoModels.value[0]?.id ?? '') : type === 'group' ? '' : selectedModel.value,
    ...(pluginDefinition(type) ? { pluginId: pluginDefinition(type)?.plugin.id, pluginTitle: pluginDefinition(type)?.definition.title, pluginDescription: pluginDefinition(type)?.definition.description, pluginRenderer: pluginDefinition(type)?.definition.renderer ?? 'generic', pluginMetadata: pluginDefinition(type)?.definition.defaultMetadata ? { ...pluginDefinition(type)?.definition.defaultMetadata } : undefined, pluginHidePanel: pluginDefinition(type)?.definition.hidePanel === true, pluginAutoOpenPanel: pluginDefinition(type)?.definition.autoOpenPanel === true, pluginTransparentBackground: pluginDefinition(type)?.definition.transparentBackground === true, pluginInteractionToggle: pluginDefinition(type)?.definition.interactionToggle === true, pluginUseBuiltinPanel: pluginDefinition(type)?.definition.useBuiltinPanel } : {}),
    imageCount: type === 'group' ? 1 : 4,
    config: type === 'config'
      ? {
          mode: 'image',
          size: '1024x1024',
          quality: 'auto',
          background: 'auto',
          count: '1',
          audioVoice: 'alloy',
          audioFormat: 'mp3',
          audioSpeed: '1',
          audioInstructions: '',
          reasoningEffort: 'medium',
        }
      : type === 'text'
        ? { mode: 'text', count: '1', reasoningEffort: 'medium' }
      : type === 'video' ? { resolution: '720p', duration: '8', aspectRatio: '16:9' } : undefined,
    status: 'idle',
    createdAt: now,
    updatedAt: now,
  }
  canvasStore.addNode(node)
  selectedNodeId.value = node.id
  selectedNodeIds.value = new Set([node.id])
  if (node.pluginUseBuiltinPanel) pluginPanelNodeId.value = node.id
  return node.id
}

function addNodeAtCenter(type: CanvasNodeData['type'] = 'image'): void {
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !rect) return
  nodeCreatePosition.value = null
  pendingConnectionSource.value = null
  pendingConnectionHandleType.value = 'source'
  const { width, height } = nodeDimensions(type)
  addNode((rect.width / 2 - project.viewport.x) / project.viewport.scale - width / 2, (rect.height / 2 - project.viewport.y) / project.viewport.scale - height / 2, type)
}

function updateNodePrompt(id: string, prompt: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (node?.pluginRenderer) {
    canvasStore.updateNode(id, { prompt, pluginMetadata: { ...(node.pluginMetadata ?? {}), content: prompt } })
  } else {
    canvasStore.updateNode(id, node?.type === 'text'
      ? {
          prompt,
          textContent: prompt,
          // A hand-edited source invalidates previously generated alternatives.
          // Streaming updates write directly through the store and therefore do
          // not pass through this helper.
          textVariants: undefined,
          primaryTextId: undefined,
        }
      : { prompt })
  }
}

function updateNodeTitle(id: string, title: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  const normalized = title.trim().slice(0, 64)
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { title: normalized || undefined })
}

function openCanvasNodeInfo(id: string): void {
  if (!activeProject.value?.nodes.some((node) => node.id === id)) return
  canvasInfoNodeId.value = id
}

function toggleFreeResize(id: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.type !== 'image') return
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { freeResize: !node.freeResize })
}

function updateTextNodeSize(id: string, size: number): void {
  const normalized = Number.isFinite(size) ? Math.min(48, Math.max(10, Math.round(size))) : 16
  canvasStore.updateNode(id, { textFontSize: normalized })
}

function updateTextNodeCount(id: string, count: number): void {
  const normalized = Number.isFinite(count) ? Math.min(10, Math.max(1, Math.floor(count))) : 1
  canvasStore.updateNode(id, { textCount: normalized })
}

function selectTextVariant(id: string, variantId: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const variant = node?.textVariants?.find((item) => item.id === variantId)
  if (!node || node.type !== 'text' || !variant || variant.status !== 'success') return
  canvasStore.checkpoint()
  canvasStore.updateNode(id, {
    primaryTextId: variant.id,
    textContent: variant.content,
    prompt: variant.content,
  })
}

function canvasImageSlots(node: CanvasNodeData): CanvasImageVariant[] {
  if (node.imageVariants?.length) return node.imageVariants
  const urls = node.imageUrls?.length ? node.imageUrls : node.imageUrl ? [node.imageUrl] : []
  return urls.map((url, index) => ({ id: `${node.id}-image-${index}`, status: 'success', url, cacheKey: node.imageCacheKeys?.[index] }))
}

function syncImageVariantFields(slots: CanvasImageVariant[], preferredIndex?: number): Pick<CanvasNodeData, 'imageUrl' | 'imageUrls' | 'imageCacheKey' | 'imageCacheKeys' | 'primaryImageIndex' | 'imageVariants'> {
  const successful = slots.filter((slot): slot is CanvasImageVariant & { status: 'success'; url: string } => slot.status === 'success' && Boolean(slot.url))
  const preferred = preferredIndex !== undefined && slots[preferredIndex]?.status === 'success' && slots[preferredIndex]?.url ? preferredIndex : slots.findIndex((slot) => slot.status === 'success' && Boolean(slot.url))
  const primaryImageIndex = Math.max(0, preferred)
  const primary = slots[primaryImageIndex]?.status === 'success' ? slots[primaryImageIndex] : undefined
  const first = primary?.url ? primary : successful[0]
  return {
    imageUrl: first?.url,
    imageUrls: successful.map((slot) => slot.url),
    imageCacheKey: first?.cacheKey,
    imageCacheKeys: successful.map((slot) => slot.cacheKey).filter((key): key is string => Boolean(key)),
    primaryImageIndex,
    imageVariants: slots,
  }
}

function commitImageVariants(id: string, slots: CanvasImageVariant[], status: CanvasNodeData['status'], errorMessage?: string, preferredIndex?: number): void {
  canvasStore.updateNode(id, {
    ...syncImageVariantFields(slots, preferredIndex),
    status,
    errorMessage,
  })
}

function selectImageVariant(id: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const slots = node ? canvasImageSlots(node) : []
  const slot = slots[imageIndex]
  if (!node || node.type !== 'image' || !slot || slot.status !== 'success' || !slot.url) return
  canvasStore.checkpoint()
  canvasStore.updateNode(id, {
    primaryImageIndex: imageIndex,
    imageUrl: slot.url,
    imageCacheKey: slot.cacheKey ?? node.imageCacheKey,
  })
}

function duplicateImageVariant(id: string, imageIndex: number): void {
  const project = activeProject.value
  const source = project?.nodes.find((item) => item.id === id)
  const slot = source ? canvasImageSlots(source)[imageIndex] : undefined
  const url = slot?.status === 'success' ? slot.url : undefined
  if (!project || !source || source.type !== 'image' || !url) return
  const now = Date.now()
  const resultId = `result-${source.id}-${now}-${Math.random().toString(36).slice(2, 8)}`
  const cacheKey = slot?.cacheKey ?? source.imageCacheKey
  canvasStore.checkpoint()
  canvasStore.addNode({
    id: resultId,
    type: 'image',
    kind: 'result',
    x: source.x + source.width + 120,
    y: source.y + Math.max(0, imageIndex) * 360,
    width: 260,
    height: 320,
    prompt: source.prompt,
    model: source.model,
    imageUrl: url,
    imageUrls: [url],
    imageVariants: [{ id: `${resultId}-image-0`, status: 'success', url, cacheKey }],
    imageCacheKey: cacheKey,
    imageCacheKeys: cacheKey ? [cacheKey] : undefined,
    resultIndex: imageIndex,
    sourceNodeId: source.id,
    status: 'success',
    createdAt: now,
    updatedAt: now,
  })
  canvasStore.addConnection({ id: `connection-${source.id}-${resultId}`, from: source.id, to: resultId, kind: 'result' })
  selectedNodeId.value = resultId
  selectedNodeIds.value = new Set([resultId])
}

function deleteImageVariant(id: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const slots = node ? canvasImageSlots(node) : []
  if (!node || node.type !== 'image' || slots.length <= 1 || imageIndex < 0 || imageIndex >= slots.length) return
  const nextSlots = slots.filter((_, index) => index !== imageIndex)
  canvasStore.checkpoint()
  const previousIndex = node.primaryImageIndex ?? 0
  const preferredIndex = Math.min(Math.max(0, previousIndex > imageIndex ? previousIndex - 1 : previousIndex), nextSlots.length - 1)
  canvasStore.updateNode(id, syncImageVariantFields(nextSlots, preferredIndex))
}

function retryImageVariant(id: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const slots = node ? canvasImageSlots(node) : []
  if (!node || node.type !== 'image' || node.kind === 'result' || !slots[imageIndex] || slots[imageIndex].status !== 'error') return
  const keyId = selectedKeyId.value
  if (!keyId || !node.model || !node.prompt.trim() || activeGenerationIds.value.has(id)) return
  void runImageVariantRetry(node, keyId, imageIndex)
}

function generateImageFromText(id: string): void {
  const project = activeProject.value
  const source = project?.nodes.find((node) => node.id === id)
  const prompt = source && (source.type === 'text' ? source.textContent || source.prompt : source.prompt).trim()
  if (!project || !source || source.type !== 'text' || source.kind === 'result' || !prompt) return
  if (!selectedKeyId.value || !selectedModel.value) {
    appStore.showError(t('playground.noModels'))
    return
  }
  const resolvedPrompt = resolveCanvasNodeMentions(prompt, project)
  const configId = addNode(source.x + source.width + 120, source.y, 'config')
  canvasStore.updateNode(configId, {
    prompt: `@[node:${source.id}]`,
    model: selectedModel.value,
    config: {
      size: '1024x1024',
      quality: 'auto',
      background: 'auto',
    },
  })
  const imageId = addNode(source.x + source.width + 520, source.y, 'image')
  canvasStore.updateNode(imageId, {
    prompt: resolvedPrompt,
    model: selectedModel.value,
    imageCount: 4,
  })
  canvasStore.addConnection({
    id: `text-config-${source.id}-${configId}`,
    from: source.id,
    to: configId,
    kind: 'reference',
  })
  canvasStore.addConnection({
    id: `config-image-${configId}-${imageId}`,
    from: configId,
    to: imageId,
    kind: 'reference',
  })
  generateNode(imageId)
}

function insertTextNodeReference(targetId: string, sourceId: string): void {
  const target = activeProject.value?.nodes.find((node) => node.id === targetId)
  if (!target) return
  const token = `@[node:${sourceId}]`
  const current = target.textContent ?? target.prompt ?? ''
  if (current.includes(token)) return
  updateNodePrompt(targetId, `${current}${current.trim() ? '\n\n' : ''}${token}`)
}

function resolveCanvasNodeMentions(value: string, project: CanvasProject): string {
  return value.replace(/@\[node:([^\]]+)\]/g, (token, nodeId: string) => {
    const source = project.nodes.find((node) => node.id === nodeId)
    if (!source) return token
    if (source.type === 'group') { const text = expandCanvasGroupResourceNodes(source, project).map((item) => getCanvasNodeText(item)).filter(Boolean).join('\n'); return text.trim() || token }
    return (source.type === 'text' ? source.textContent || source.prompt : source.pluginMetadata?.content || source.prompt || '').toString().trim() || token
  })
}

function updatePluginComposer(id: string, prompt: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.updateNode(id, {
    prompt,
    pluginMetadata: { ...(node.pluginMetadata ?? {}), composerContent: prompt },
  })
}

function updatePluginModel(id: string, model: string): void {
  const normalized = model.trim()
  if (normalized) canvasStore.updateNode(id, { model: normalized })
}

function builtinPluginReferences(node: CanvasNodeData): string[] {
  const project = activeProject.value
  if (!project) return []
  return project.connections
    .filter((connection) => connection.to === node.id)
    .flatMap((connection) => {
      const source = project.nodes.find((candidate) => candidate.id === connection.from)
      if (!source) return []
      const sourceNodes = source.type === 'group' ? expandCanvasGroupResourceNodes(source, project) : [source]
      return sourceNodes.flatMap((item) => { const pluginContent = typeof item.pluginMetadata?.content === 'string' ? item.pluginMetadata.content : ''; const isImageResource = /^data:image\//i.test(pluginContent) || (/^https?:\/\//i.test(pluginContent) && item.pluginRenderer === 'panorama'); return [item.imageUrl, ...(item.imageUrls ?? []), ...(isImageResource ? [pluginContent] : [])].filter((value): value is string => Boolean(value)) })
    })
    .filter((value, index, values) => values.indexOf(value) === index)
}

function builtinPluginTextReferences(node: CanvasNodeData): string[] {
  const project = activeProject.value
  if (!project) return []
  return project.connections
    .filter((connection) => connection.to === node.id)
    .flatMap((connection) => {
      const source = project.nodes.find((candidate) => candidate.id === connection.from)
      if (!source) return []
      if (source.type === 'group') return expandCanvasGroupResourceNodes(source, project).map((item) => getCanvasNodeText(item)).filter(Boolean)
      if (source.type === 'text') return [(source.textContent || source.prompt).trim()]
      const pluginContent = typeof source.pluginMetadata?.content === 'string' ? source.pluginMetadata.content.trim() : ''
      if (!pluginContent || /^data:|^https?:\/\//i.test(pluginContent)) return []
      return [pluginContent]
    })
    .filter(Boolean)
    .filter((value, index, values) => values.indexOf(value) === index)
    .slice(0, 8)
}

function updateNodePlugin(id: string, patch: Record<string, unknown>): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.updateNode(id, { pluginMetadata: { ...(node.pluginMetadata ?? {}), ...patch } })
}

async function generateBuiltinPluginNode(id: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const config = node?.pluginUseBuiltinPanel
  if (!node || !config || pluginGenerationBusy.value === id) return
  const composerPrompt = String(node.pluginMetadata?.composerContent ?? node.prompt ?? '').trim()
  const prompt = [config.promptPrefix?.trim(), ...builtinPluginTextReferences(node), composerPrompt].filter(Boolean).join('\n\n')
  if (!prompt) return
  const controller = new AbortController()
  pluginGenerationControllers.set(id, controller)
  pluginGenerationBusy.value = id
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { status: 'generating', errorMessage: undefined })
  try {
    const ai = createCanvasPluginContext(node).ai
    const options = node.pluginMetadata?.composerOptions
    const composerOptions = options && typeof options === 'object' && !Array.isArray(options) ? options as Record<string, unknown> : {}
    const model = node.model || ai.defaultModel(config.mode)
    if (config.mode === 'image') {
      const count = Math.max(1, Math.min(4, Number(composerOptions.count ?? 1) || 1))
      const result = await ai.generateImage(prompt, {
        signal: controller.signal,
        model,
        count,
        references: builtinPluginReferences(node),
        size: typeof composerOptions.size === 'string' ? composerOptions.size : '1536x1024',
        quality: typeof composerOptions.quality === 'string' ? composerOptions.quality : 'auto',
      })
      let content = result.images[0]
      let contentCacheKey: string | undefined
      if (config.writeBackToSelf && content && userId.value && selectedKeyId.value) {
        contentCacheKey = createPlaygroundImageCacheKey(userId.value, selectedKeyId.value, `plugin-${id}`, 0)
        const cachedContent = await cachePlaygroundImage(contentCacheKey, userId.value, selectedKeyId.value, content, {
          prompt,
          model,
          size: typeof composerOptions.size === 'string' ? composerOptions.size : '1536x1024',
          quality: typeof composerOptions.quality === 'string' ? composerOptions.quality : 'auto',
          sourceImageCount: builtinPluginReferences(node).length,
        })
        if (cachedContent) {
          content = cachedContent
          imageObjectUrls.add(cachedContent)
        }
      }
      updateNodePlugin(id, { images: result.images, ...(config.writeBackToSelf && content ? { content, ...(contentCacheKey ? { contentCacheKey } : {}) } : {}) })
    } else if (config.mode === 'video') {
      const result = await ai.generateVideo(prompt, { signal: controller.signal, model, references: builtinPluginReferences(node), size: typeof composerOptions.size === 'string' ? composerOptions.size : '720p', seconds: typeof composerOptions.seconds === 'string' ? composerOptions.seconds : '8', aspectRatio: typeof composerOptions.aspectRatio === 'string' ? composerOptions.aspectRatio : '16:9' })
      updateNodePlugin(id, { videoUrl: result.url, videoMimeType: result.mimeType, ...(config.writeBackToSelf ? { content: result.url } : {}) })
    } else if (config.mode === 'audio') {
      const result = await ai.generateAudio(prompt, { signal: controller.signal, model, voice: typeof composerOptions.voice === 'string' ? composerOptions.voice : 'Ara', language: typeof composerOptions.language === 'string' ? composerOptions.language : 'en' })
      updateNodePlugin(id, { audioUrl: result.url, audioMimeType: result.mimeType, ...(config.writeBackToSelf ? { content: result.url } : {}) })
    } else {
      const result = await ai.generateText(prompt, { signal: controller.signal, model, system: typeof composerOptions.system === 'string' ? composerOptions.system : undefined, onDelta: (text) => updateNodePlugin(id, { generatedText: text }) })
      updateNodePlugin(id, { generatedText: result.text, ...(config.writeBackToSelf ? { content: result.text } : {}) })
    }
    canvasStore.updateNode(id, { status: 'success' })
  } catch (error) {
    if (controller.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) {
      canvasStore.updateNode(id, { status: 'idle', errorMessage: undefined })
    } else {
      canvasStore.updateNode(id, { status: 'error', errorMessage: error instanceof Error ? error.message : '插件生成失败。' })
    }
  } finally {
    pluginGenerationControllers.delete(id)
    if (pluginGenerationBusy.value === id) pluginGenerationBusy.value = null
  }
}

function cancelBuiltinPluginGeneration(id: string): void {
  pluginGenerationControllers.get(id)?.abort()
}

function updateTextNodeModel(id: string, model: string): void {
  const normalized = model.trim()
  if (!normalized) return
  canvasStore.updateNode(id, { model: normalized })
}

function updateImageNodeModel(id: string, model: string): void {
  const normalized = model.trim()
  if (!normalized) return
  canvasStore.updateNode(id, { model: normalized })
}

function updateVideoNodeModel(id: string, model: string): void {
  if (model.trim()) canvasStore.updateNode(id, { model: model.trim() })
}

function canGenerateText(node: CanvasNodeData): boolean {
  return Boolean(
    selectedTextKeyId.value
    && (node.model || selectedTextModel.value)
    && node.status !== 'generating',
  ) || node.status === 'generating'
}

function cancelNodeGeneration(id: string): void {
  const controller = textGenerationControllers.get(id)
  if (controller) controller.abort()
}

function cancelVideoNode(id: string): void {
  videoControllers.get(id)?.abort()
}

function cancelAudioNode(id: string): void {
  audioControllers.get(id)?.abort()
}

function openVideoEdit(id: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.type !== 'video' || !node.videoUrl) return
  selectedNodeId.value = id
  selectedNodeIds.value = new Set([id])
  videoEditPrompt.value = node.prompt || ''
}

function readBlobAsDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('Unable to read video data.'))
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read video data.'))
    reader.readAsDataURL(blob)
  })
}

async function editVideoNode(id: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const project = activeProject.value
  const keyId = selectedKeyId.value
  const model = node?.model || videoModels.value[0]?.id
  const prompt = videoEditPrompt.value.trim()
  if (!node || !project || node.type !== 'video' || !node.videoUrl || !keyId || !model || !prompt || videoEditBusy.value) return

  videoEditBusy.value = true
  const targetId = `video-edit-${id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const targetIndex = project.nodes.filter((candidate) => candidate.sourceNodeId === id && candidate.type === 'video').length
  const targetSize = nodeDimensions('video')
  canvasStore.checkpoint()
  canvasStore.addNode({
    id: targetId,
    type: 'video',
    kind: 'result',
    x: node.x + node.width + 120,
    y: node.y,
    width: targetSize.width,
    height: targetSize.height,
    prompt,
    model,
    config: node.config ? { ...node.config } : undefined,
    status: 'generating',
    sourceNodeId: id,
    resultIndex: targetIndex,
    createdAt: Date.now(),
    updatedAt: Date.now(),
  })
  canvasStore.addConnection({ id: `connection-${id}-${targetId}`, from: id, to: targetId, kind: 'result' })
  selectedNodeId.value = targetId
  selectedNodeIds.value = new Set([targetId])

  try {
    const sourceBlob = node.videoCacheKey
      ? await readCachedCanvasMediaBlob(node.videoCacheKey)
      : null
    const videoBlob: Blob = sourceBlob ?? (await fetch(node.videoUrl).then((response) => {
      if (!response.ok) throw new Error(t('playground.canvasVideoEditSourceFailed'))
      return response.blob()
    }))
    const dataUrl = await readBlobAsDataUrl(videoBlob)
    const response = await sendPlaygroundVideoEdit(keyId, {
      model,
      prompt,
      video: { url: dataUrl },
      resolution: node.config?.resolution ?? '720p',
      duration: Number(node.config?.duration ?? 8),
      aspect_ratio: node.config?.aspectRatio ?? '16:9',
    })
    canvasStore.updateNode(targetId, { videoRequestId: response.id })
    let finished: typeof response | null = null
    for (let attempt = 0; attempt < 90; attempt += 1) {
      await new Promise((resolve) => window.setTimeout(resolve, 2000))
      const status = await fetchPlaygroundVideo(keyId, response.id)
      const normalized = (status.status ?? '').toLowerCase()
      if (status.video_url || status.url || ['completed', 'done', 'succeeded', 'success'].includes(normalized)) {
        finished = status
        break
      }
      if (['failed', 'error', 'cancelled', 'canceled'].includes(normalized)) throw new Error(t('playground.canvasVideoGenerationFailed'))
    }
    if (!finished) throw new Error(t('playground.canvasVideoTimeout'))
    const content = await fetchPlaygroundVideoContent(keyId, response.id)
    const cacheKey = userId.value ? createCanvasMediaCacheKey(userId.value, keyId, targetId) : `canvas-video-${targetId}`
    const url = await cacheCanvasMedia(cacheKey, content)
    if (!url) throw new Error(t('playground.canvasVideoGenerationFailed'))
    imageObjectUrls.add(url)
    canvasStore.updateNode(targetId, { videoUrl: url, videoCacheKey: cacheKey, prompt, model, status: 'success', errorMessage: undefined })
  } catch (error) {
    canvasStore.updateNode(targetId, { status: 'error', errorMessage: error instanceof Error ? error.message : t('playground.canvasVideoGenerationFailed') })
  } finally {
    videoEditBusy.value = false
  }
}

function retryCanvasGeneration(id: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.status !== 'error') return
  const source = node.kind === 'result' && node.sourceNodeId
    ? activeProject.value?.nodes.find((item) => item.id === node.sourceNodeId)
    : node
  if (!source || source.kind === 'result') return
  if (node.type === 'video') {
    void generateVideoNode(source.id, node.kind === 'result' ? node.id : undefined)
  } else if (node.type === 'audio') {
    void generateAudioNode(source.id, node.kind === 'result' ? node.id : undefined)
  } else if (node.type === 'text') {
    void generateTextNode(source.id)
  }
}

async function generateVideoNode(id: string, retryTargetId?: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const project = activeProject.value
  const keyId = selectedKeyId.value
  const model = node?.model || videoModels.value[0]?.id
  if (!node || !project || node.type !== 'video' || node.kind === 'result' || !keyId || !model) return
  if (node.status === 'generating') {
    cancelVideoNode(id)
    return
  }
  const controller = new AbortController()
  videoControllers.set(id, controller)
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { status: 'generating', errorMessage: undefined, model })
  const prompt = composeCanvasPrompt(node, project)
  if (!prompt) {
    canvasStore.updateNode(id, { status: 'idle' })
    videoControllers.delete(id)
    return
  }
  const isEmptyVideoNode = !node.videoUrl && !node.videoCacheKey
  const targetId = retryTargetId || (isEmptyVideoNode ? id : `video-result-${id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`)
  const targetIndex = project.nodes.filter((candidate) => candidate.sourceNodeId === id && candidate.type === 'video').length
  const targetSize = nodeDimensions('video')
  if (targetId !== id && retryTargetId && project.nodes.some((candidate) => candidate.id === targetId)) {
    canvasStore.updateNode(targetId, { status: 'generating', errorMessage: undefined, prompt, model, config: node.config ? { ...node.config } : undefined })
  } else if (targetId !== id) {
    canvasStore.addNode({
      id: targetId,
      type: 'video',
      kind: 'result',
      x: node.x + node.width + 120,
      y: node.y,
      width: targetSize.width,
      height: targetSize.height,
      prompt,
      model,
      config: node.config ? { ...node.config } : undefined,
      status: 'generating',
      sourceNodeId: id,
      resultIndex: targetIndex,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    })
    canvasStore.addConnection({ id: `connection-${id}-${targetId}`, from: id, to: targetId, kind: 'result' })
  }
  try {
    const referenceImages = await resolveReferenceAttachments(node)
    const response = await sendPlaygroundVideoGeneration(keyId, {
      model,
      prompt,
      resolution: node.config?.resolution ?? '720p',
      duration: Number(node.config?.duration ?? 8),
      aspect_ratio: node.config?.aspectRatio ?? '16:9',
      ...(referenceImages.length
        ? { reference_images: referenceImages.slice(0, 7).map((reference) => ({ url: reference.dataUrl })) }
        : {}),
    }, { signal: controller.signal })
    canvasStore.updateNode(targetId, { videoRequestId: response.id })
    let finished: typeof response | null = null
    for (let attempt = 0; attempt < 90; attempt += 1) {
      await new Promise((resolve) => window.setTimeout(resolve, 2000))
      if (controller.signal.aborted) {
        canvasStore.updateNode(targetId, { status: 'idle', errorMessage: undefined })
        if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
        return
      }
      const status = await fetchPlaygroundVideo(keyId, response.id, { signal: controller.signal })
      const normalized = (status.status ?? '').toLowerCase()
      if (status.video_url || status.url || ['completed', 'done', 'succeeded', 'success'].includes(normalized)) {
        finished = status
        break
      }
      if (['failed', 'error', 'cancelled', 'canceled'].includes(normalized)) throw new Error(t('playground.canvasVideoGenerationFailed'))
    }
    if (!finished) throw new Error(t('playground.canvasVideoTimeout'))
    const content = await fetchPlaygroundVideoContent(keyId, response.id, { signal: controller.signal })
    const cacheKey = userId.value ? createCanvasMediaCacheKey(userId.value, keyId, targetId) : `canvas-video-${targetId}`
    const url = await cacheCanvasMedia(cacheKey, content)
    if (!url) throw new Error(t('playground.canvasVideoGenerationFailed'))
    imageObjectUrls.add(url)
    canvasStore.updateNode(targetId, { videoUrl: url, videoCacheKey: cacheKey, prompt, model, status: 'success', errorMessage: undefined })
    if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
  } catch (error) {
    if (controller.signal.aborted) {
      canvasStore.updateNode(targetId, { status: 'idle', errorMessage: undefined })
      if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
    } else {
      canvasStore.updateNode(targetId, { status: 'error', errorMessage: error instanceof Error ? error.message : t('playground.canvasVideoGenerationFailed') })
      if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
    }
  } finally {
    if (videoControllers.get(id) === controller) videoControllers.delete(id)
  }
}

async function generateAudioNode(id: string, retryTargetId?: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const project = activeProject.value
  const keyId = selectedTextKeyId.value
  const model = node?.model || selectedTextModel.value || 'grok-voice-latest'
  if (!node || !project || node.type !== 'audio' || node.kind === 'result' || !keyId || !composeCanvasPrompt(node, project)) return
  if (node.status === 'generating') {
    cancelAudioNode(id)
    return
  }
  const prompt = composeCanvasPrompt(node, project)
  const isEmptyAudioNode = !node.audioUrl && !node.audioCacheKey
  const targetId = retryTargetId || (isEmptyAudioNode ? id : `audio-result-${id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`)
  const targetIndex = project.nodes.filter((candidate) => candidate.sourceNodeId === id && candidate.type === 'audio').length
  const targetSize = nodeDimensions('audio')
  const controller = new AbortController()
  audioControllers.set(id, controller)
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { status: 'generating', errorMessage: undefined, model })
  if (targetId !== id && retryTargetId && project.nodes.some((candidate) => candidate.id === targetId)) {
    canvasStore.updateNode(targetId, { status: 'generating', errorMessage: undefined, prompt, model, config: node.config ? { ...node.config } : undefined })
  } else if (targetId !== id) {
    canvasStore.addNode({
      id: targetId,
      type: 'audio',
      kind: 'result',
      x: node.x + node.width + 120,
      y: node.y + (node.height - targetSize.height) / 2,
      width: targetSize.width,
      height: targetSize.height,
      prompt,
      model,
      config: node.config ? { ...node.config } : undefined,
      status: 'generating',
      sourceNodeId: id,
      resultIndex: targetIndex,
      createdAt: Date.now(),
      updatedAt: Date.now(),
    })
    canvasStore.addConnection({ id: `connection-${id}-${targetId}`, from: id, to: targetId, kind: 'result' })
  }
  try {
    const audioVoice = node.config?.audioVoice || 'alloy'
    const audioFormat = node.config?.audioFormat || 'mp3'
    const audioSpeedRaw = Number(node.config?.audioSpeed ?? '1')
    const audioSpeed = Number.isFinite(audioSpeedRaw) ? Math.min(4, Math.max(0.25, audioSpeedRaw)) : 1
    const audioInstructions = node.config?.audioInstructions?.trim()
    const blob = await sendPlaygroundSpeech(keyId, {
      model,
      input: prompt,
      voice: audioVoice,
      response_format: audioFormat,
      speed: audioSpeed,
      ...(audioInstructions ? { instructions: audioInstructions } : {}),
      language: 'en',
    })
    if (controller.signal.aborted) {
      canvasStore.updateNode(targetId, { status: 'idle', errorMessage: undefined })
      if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
      return
    }
    if (!blob.size) throw new Error(t('playground.canvasAudioGenerationFailed'))
    const cacheKey = userId.value ? createCanvasMediaCacheKey(userId.value, keyId, targetId) : `canvas-audio-${targetId}`
    const url = await cacheCanvasMedia(cacheKey, blob)
    if (!url) throw new Error(t('playground.canvasAudioGenerationFailed'))
    imageObjectUrls.add(url)
    canvasStore.updateNode(targetId, { audioUrl: url, audioCacheKey: cacheKey, prompt, model, status: 'success', errorMessage: undefined })
    if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
  } catch (error) {
    if (controller.signal.aborted) {
      canvasStore.updateNode(targetId, { status: 'idle', errorMessage: undefined })
      if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
    } else {
      canvasStore.updateNode(targetId, { status: 'error', errorMessage: error instanceof Error ? error.message : t('playground.canvasAudioGenerationFailed') })
      if (targetId !== id) canvasStore.updateNode(id, { status: 'success', errorMessage: undefined })
    }
  } finally {
    if (audioControllers.get(id) === controller) audioControllers.delete(id)
  }
}

async function transcribeAudioNode(id: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const keyId = selectedTextKeyId.value
  if (!node || node.type !== 'audio' || !node.audioUrl || !keyId || transcribingNodeIds.value.has(id)) return
  transcribingNodeIds.value = new Set([...transcribingNodeIds.value, id])
  try {
    const cachedBlob = node.audioCacheKey
      ? await readCachedCanvasMediaBlob(node.audioCacheKey).catch(() => null)
      : null
    const blob = cachedBlob ?? await (await fetch(node.audioUrl)).blob()
    const text = await sendPlaygroundTranscription(keyId, blob, `${node.id}.mp3`, { model: node.model || 'grok-voice-latest', language: 'en' })
    const now = Date.now()
    const resultId = `transcription-${id}-${now}-${Math.random().toString(36).slice(2, 8)}`
    canvasStore.checkpoint()
    canvasStore.addNode({
      id: resultId,
      type: 'text',
      kind: 'result',
      x: node.x + node.width + 120,
      y: node.y,
      width: 340,
      height: 300,
      prompt: text,
      textContent: text,
      model: node.model || 'grok-voice-latest',
      sourceNodeId: node.id,
      resultIndex: 0,
      status: 'success',
      createdAt: now,
      updatedAt: now,
    })
    canvasStore.addConnection({ id: `connection-${node.id}-${resultId}`, from: node.id, to: resultId, kind: 'result' })
    selectedNodeId.value = resultId
    selectedNodeIds.value = new Set([resultId])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasAudioTranscriptionFailed'))
  } finally {
    const next = new Set(transcribingNodeIds.value)
    next.delete(id)
    transcribingNodeIds.value = next
  }
}

async function generateTextNode(id: string): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const project = activeProject.value
  const keyId = selectedTextKeyId.value
  const model = node?.model || selectedTextModel.value
  if (!node || !project || node.type !== 'text' || node.kind === 'result' || !keyId || !model) {
    if (!model) appStore.showError(t('playground.canvasTextModelRequired'))
    return
  }
  if (node.status === 'generating') {
    cancelNodeGeneration(id)
    return
  }
  const textCount = Math.min(10, Math.max(1, Math.floor(node.textCount ?? 1)))
  const sourceText = resolveCanvasNodeMentions((node.textContent || node.prompt || '').trim(), project)
  const mentionAttachments = await resolveMentionAttachments(node.textContent || node.prompt || '', project)
  const upstreamAttachments = await resolveReferenceAttachments(node)
  const imageAttachments = [...mentionAttachments, ...upstreamAttachments.filter((attachment) => !mentionAttachments.some((item) => item.dataUrl === attachment.dataUrl))]
  const instruction = textInstruction.value.trim() || (sourceText ? t('playground.canvasTextDefaultInstruction') : t('playground.canvasTextInstructionPlaceholder'))
  const incoming = project.connections
    .filter((connection) => connection.to === id)
    .map((connection) => project.nodes.find((candidate) => candidate.id === connection.from))
    .filter((candidate): candidate is CanvasNodeData => Boolean(candidate))
  const upstreamText = incoming
    .filter((candidate) => candidate.type === 'text')
    .map((candidate) => (candidate.textContent || candidate.prompt).trim())
    .filter(Boolean)
  if (!sourceText && !instruction.trim()) {
    appStore.showError(t('playground.canvasTextInputRequired'))
    return
  }

  const context = [...upstreamText.filter((text) => text !== sourceText), sourceText].filter(Boolean).join('\n\n')
  const controller = new AbortController()
  textGenerationControllers.set(id, controller)
  canvasStore.checkpoint()
  canvasStore.updateNode(id, { status: 'generating', errorMessage: undefined, model })
  const existingResult = sourceText
    ? project.nodes.find((candidate) => candidate.type === 'text' && candidate.kind === 'result' && candidate.sourceNodeId === id)
    : null
  const resultNodeId = sourceText ? (existingResult?.id ?? `text-result-${id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`) : id
  if (sourceText && !existingResult) {
    const now = Date.now()
    canvasStore.addNode({
      id: resultNodeId,
      type: 'text',
      kind: 'result',
      x: node.x + node.width + 120,
      y: node.y,
      width: node.width,
      height: node.height,
      prompt: '',
      textContent: '',
      model,
      textCount,
      status: 'generating',
      sourceNodeId: id,
      resultIndex: 0,
      createdAt: now,
      updatedAt: now,
    })
    canvasStore.addConnection({ id: `connection-${id}-${resultNodeId}`, from: id, to: resultNodeId, kind: 'result' })
  } else {
    canvasStore.updateNode(resultNodeId, {
      status: 'generating',
      errorMessage: undefined,
      model,
      textCount,
      textVariants: undefined,
      primaryTextId: undefined,
      textContent: '',
      prompt: sourceText ? '' : node.prompt,
    })
  }
  const variants: CanvasTextVariant[] = []
  const updateVariants = (primaryTextId?: string): void => {
    const primary = variants.find((variant) => variant.id === primaryTextId) ?? variants.find((variant) => variant.status === 'success')
    canvasStore.updateNode(resultNodeId, {
      textVariants: variants.map((variant) => ({ ...variant })),
      primaryTextId: primary?.id,
      textContent: primary?.content || '',
      prompt: primary?.content || (sourceText ? '' : node.prompt),
    })
  }
  try {
    const userContent = imageAttachments.length
      ? [
          { type: 'text', text: `${instruction}${context ? `\n\nContext:\n${context}` : ''}` },
          ...imageAttachments.map((attachment) => ({ type: 'image_url' as const, image_url: { url: attachment.dataUrl } })),
        ]
      : `${instruction}${context ? `\n\nContext:\n${context}` : ''}`
    const generatedErrors: string[] = []
    for (let index = 0; index < textCount; index += 1) {
      if (controller.signal.aborted) break
      const variant: CanvasTextVariant = { id: `text-variant-${id}-${Date.now()}-${index}-${Math.random().toString(36).slice(2, 8)}`, status: 'generating', content: '' }
      variants.push(variant)
      updateVariants()
      try {
        const result = await sendPlaygroundChat(keyId, {
          model,
          stream: true,
          ...(node.config?.reasoningEffort && node.config.reasoningEffort !== 'none' ? { reasoning_effort: node.config.reasoningEffort } : {}),
          messages: [
            { role: 'system', content: t('playground.canvasTextSystemPrompt') },
            { role: 'user', content: userContent },
          ],
        }, {
          signal: controller.signal,
          onDelta: (delta) => {
            variant.content += delta.contentDelta
            updateVariants()
          },
        })
        const generated = result.content.trim()
        if (!generated) throw new Error(t('playground.imageGenerationNoResults'))
        variant.content = generated
        variant.status = 'success'
        updateVariants()
      } catch (error) {
        if (controller.signal.aborted) break
        variant.status = 'error'
        variant.errorMessage = error instanceof Error ? error.message : t('playground.sendFailed')
        generatedErrors.push(variant.errorMessage)
        updateVariants()
      }
    }
    const successful = variants.filter((variant) => variant.status === 'success' && variant.content.trim())
    const current = activeProject.value?.nodes.find((item) => item.id === id)
    if (!current) return
    if (!successful.length) {
      if (controller.signal.aborted) {
        canvasStore.updateNode(id, { status: 'idle' })
        if (resultNodeId !== id) canvasStore.updateNode(resultNodeId, { status: 'idle' })
      } else {
        const message = generatedErrors.join('; ') || t('playground.imageGenerationNoResults')
        canvasStore.updateNode(id, { status: 'error', errorMessage: message })
        if (resultNodeId !== id) canvasStore.updateNode(resultNodeId, { status: 'error', errorMessage: message })
      }
      return
    }
    const primary = successful[0]
    updateVariants(primary.id)
    canvasStore.updateNode(resultNodeId, {
      status: 'success',
      errorMessage: generatedErrors.length ? generatedErrors.join('; ') : undefined,
      primaryTextId: primary.id,
      textContent: primary.content,
      prompt: primary.content,
    })
    if (resultNodeId !== id) {
      canvasStore.updateNode(id, { status: 'success' })
      selectedNodeId.value = resultNodeId
      selectedNodeIds.value = new Set([resultNodeId])
    }
  } catch (error) {
    if (controller.signal.aborted) {
      canvasStore.updateNode(id, { status: 'idle' })
    } else {
      canvasStore.updateNode(id, { status: 'error', errorMessage: error instanceof Error ? error.message : t('playground.sendFailed') })
    }
  } finally {
    if (textGenerationControllers.get(id) === controller) textGenerationControllers.delete(id)
  }
}

function assistantContextText(): string {
  if (!assistantContextNodes.value.length) return t('playground.canvasAssistantNoContext')
  return assistantContextNodes.value.map((node, index) => {
    const content = node.type === 'text' ? node.textContent || node.prompt : node.prompt
    return `[${index + 1}] ${t(`playground.canvasNodeType.${node.type}`)}\n${content || '(empty)'}`
  }).join('\n\n')
}

function setAssistantMessages(messages: NonNullable<CanvasProject['assistantMessages']>): void {
  canvasStore.updateProject({ assistantMessages: messages.slice(-100) })
}

function cancelAssistant(): void {
  assistantController.value?.abort()
}

async function sendAssistantMessage(content: string): Promise<void> {
  const project = activeProject.value
  const keyId = selectedTextKeyId.value
  const model = assistantModel.value || selectedTextModel.value
  if (!project || !keyId || !model || assistantLoading.value) return
  const now = Date.now()
  const userMessage = { id: `assistant-user-${now}`, role: 'user' as const, content, createdAt: now }
  const responseMessage = { id: `assistant-response-${now}`, role: 'assistant' as const, content: '', createdAt: now }
  const previous = project.assistantMessages ?? []
  canvasStore.checkpoint()
  setAssistantMessages([...previous, userMessage, responseMessage])
  const controller = new AbortController()
  assistantController.value = controller
  assistantLoading.value = true
  try {
    const history = [...previous, userMessage].slice(-20).map((message) => ({
      role: message.role,
      // Keep the persisted message tokenized for rendering, but send the
      // referenced node contents to the model so @ mentions are meaningful.
      content: message.role === 'user' ? resolveCanvasNodeMentions(message.content, project) : message.content,
    }))
    const finalUser = history.at(-1)
    if (finalUser) finalUser.content = `${finalUser.content}\n\n${t('playground.canvasAssistantContextHeader')}\n${assistantContextText()}`
    const result = await sendPlaygroundChat(keyId, {
      model,
      stream: true,
      messages: [
        { role: 'system', content: t('playground.canvasAssistantSystemPrompt') },
        ...history,
      ],
    }, {
      signal: controller.signal,
      onDelta: (delta) => {
        const messages = activeProject.value?.assistantMessages ?? []
        setAssistantMessages(messages.map((message) => message.id === responseMessage.id
          ? { ...message, content: `${message.content}${delta.contentDelta}` }
          : message))
      },
    })
    const messages = activeProject.value?.assistantMessages ?? []
    setAssistantMessages(messages.map((message) => message.id === responseMessage.id
      ? { ...message, content: result.content || message.content }
      : message))
  } catch (error) {
    const messages = activeProject.value?.assistantMessages ?? []
    if (controller.signal.aborted) {
      setAssistantMessages(messages.filter((message) => message.id !== responseMessage.id || message.content))
    } else {
      setAssistantMessages(messages.map((message) => message.id === responseMessage.id
        ? { ...message, errorMessage: error instanceof Error ? error.message : t('playground.sendFailed') }
        : message))
    }
  } finally {
    if (assistantController.value === controller) assistantController.value = null
    assistantLoading.value = false
  }
}

function insertAssistantMessage(id: string): void {
  const project = activeProject.value
  const message = project?.assistantMessages?.find((item) => item.id === id && item.role === 'assistant')
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !message?.content.trim() || !rect) return
  const source = selectedNode.value
  const now = Date.now()
  const width = 340
  const height = 300
  const node: CanvasNodeData = {
    id: `assistant-text-${now}-${Math.random().toString(36).slice(2, 8)}`,
    type: 'text',
    kind: source ? 'result' : 'generator',
    x: source ? source.x + source.width + 120 : (rect.width / 2 - project.viewport.x) / project.viewport.scale - width / 2,
    y: source ? source.y : (rect.height / 2 - project.viewport.y) / project.viewport.scale - height / 2,
    width,
    height,
    prompt: message.content.trim(),
    textContent: message.content.trim(),
    model: assistantModel.value || selectedTextModel.value,
    sourceNodeId: source?.id,
    status: 'success',
    createdAt: now,
    updatedAt: now,
  }
  canvasStore.checkpoint()
  canvasStore.addNode(node)
  if (source) canvasStore.addConnection({ id: `connection-${source.id}-${node.id}`, from: source.id, to: node.id, kind: 'result' })
  selectedNodeId.value = node.id
  selectedNodeIds.value = new Set([node.id])
  assistantOpen.value = false
}

function clearAssistantMessages(): void {
  if (!activeProject.value?.assistantMessages?.length || !window.confirm(t('playground.canvasAssistantClearConfirm'))) return
  cancelAssistant()
  canvasStore.checkpoint()
  setAssistantMessages([])
}

function connectCanvasAgent(config: { endpoint: string; token: string }): void {
  agentBusy.value = true
  agentError.value = ''
  agentEndpoint.value = config.endpoint.trim().replace(/\/+$/, '')
  agentToken.value = config.token.trim()
  void agentBridge.connect(agentEndpoint.value, agentToken.value, handleCanvasAgentEvent, handleCanvasAgentToolCall)
    .then(async () => {
      agentConnected.value = true
      localStorage.setItem('sub2api.playground.canvas.agent', JSON.stringify({ endpoint: agentEndpoint.value, token: agentToken.value }))
      await agentBridge.activate()
      await loadCanvasAgentThreads()
      await loadCanvasAgentSkills()
      await startCanvasAgentThread()
      await publishAgentState()
    })
    .catch((error: unknown) => {
      agentConnected.value = false
      agentError.value = error instanceof Error ? error.message : String(error)
      agentBridge.disconnect()
    })
    .finally(() => { agentBusy.value = false })
}

function disconnectCanvasAgent(): void {
  agentBridge.disconnect()
  agentConnected.value = false
  agentConversation.value = { revision: 0, conversationId: '', threadId: '', status: 'idle', mcpStatuses: {} }
  agentMessages.value = []
  agentThreads.value = []
  agentSkills.value = []
  agentApproval.value = null
  agentError.value = ''
}

function handleCanvasAgentEvent(event: CanvasAgentEvent): void {
  if (event.type === 'hello' || event.type === 'connected') {
    agentConnected.value = true
    const conversation = event.payload?.conversation
    if (conversation && typeof conversation === 'object') agentConversation.value = normalizeAgentConversation(conversation)
    return
  }
  if (event.type === 'codex_approval') {
    agentApproval.value = {
      requestId: String(event.payload?.requestId || ''),
      reason: typeof event.payload?.reason === 'string' ? event.payload.reason : undefined,
      method: typeof event.payload?.method === 'string' ? event.payload.method : undefined,
      command: event.payload?.command,
    }
    return
  }
  if (event.type === 'codex_approval_resolved') {
    agentApproval.value = null
    return
  }
  if (event.type === 'conversation_changed' && event.payload) {
    agentConversation.value = normalizeAgentConversation(event.payload)
    return
  }
  if (event.type === 'skills_changed') {
    void loadCanvasAgentSkills(true)
    return
  }
  if (event.type === 'chat_message' && event.payload) {
    const message = event.payload.message && typeof event.payload.message === 'object' ? event.payload.message as Record<string, unknown> : event.payload
    const text = typeof message.text === 'string' ? message.text : ''
    const id = typeof message.id === 'string' ? message.id : `agent-message-${Date.now()}`
    if (text) upsertCanvasAgentMessage({ id, role: message.role === 'user' ? 'user' : 'assistant', text })
    return
  }
  if (event.type === 'agent_event' && event.payload) {
    const item = event.payload.item && typeof event.payload.item === 'object' ? event.payload.item as Record<string, unknown> : {}
    const itemType = String(event.payload.type || item.type || '')
    if (itemType === 'item.updated' || itemType === 'item.completed') {
      const text = typeof item.text === 'string' ? item.text : typeof item.delta === 'string' ? item.delta : ''
      if (text) upsertCanvasAgentMessage({ id: String(item.id || `agent-event-${Date.now()}`), role: 'assistant', text, append: itemType === 'item.updated' && typeof item.delta === 'string' })
    }
    return
  }
  if (event.type === 'disconnected') agentConnected.value = false
  if (event.type === 'error' || event.type === 'agent_error') agentError.value = event.message || String(event.payload?.error || t('playground.canvasAgentError'))
}

async function startCanvasAgentThread(): Promise<void> {
  if (!agentConnected.value || agentConversation.value.status === 'ready' || agentConversation.value.status === 'preparing') return
  const response = await agentBridge.startThread('request')
  if (response.conversation && typeof response.conversation === 'object') agentConversation.value = normalizeAgentConversation(response.conversation)
  if (response.thread && typeof response.thread === 'object') agentConversation.value.threadId = String((response.thread as Record<string, unknown>).id || agentConversation.value.threadId)
  if (Array.isArray(response.messages)) agentMessages.value = normalizeAgentMessages(response.messages)
}

async function loadCanvasAgentThreads(): Promise<void> {
  const response = await agentBridge.listThreads().catch(() => null)
  if (!response) return
  const items = Array.isArray(response.data) ? response.data : Array.isArray(response.threads) ? response.threads : []
  agentThreads.value = items.flatMap((item) => {
    if (!item || typeof item !== 'object') return []
    const source = item as Record<string, unknown>
    if (typeof source.id !== 'string' || !source.id) return []
    return [{ id: source.id, preview: typeof source.preview === 'string' ? source.preview : '', name: typeof source.name === 'string' ? source.name : null, updatedAt: typeof source.updatedAt === 'number' ? source.updatedAt : undefined }]
  }).slice(0, 50)
  if (response.conversation && typeof response.conversation === 'object') agentConversation.value = normalizeAgentConversation(response.conversation)
}

async function loadCanvasAgentSkills(forceReload = false): Promise<void> {
  const response = await agentBridge.listSkills(forceReload).catch(() => null)
  if (!response) return
  const items = Array.isArray(response.data) ? response.data : Array.isArray(response.skills) ? response.skills : []
  agentSkills.value = items.flatMap((item) => {
    if (!item || typeof item !== 'object') return []
    const source = item as Record<string, unknown>
    if (typeof source.name !== 'string' || !source.name) return []
    return [{ name: source.name, description: typeof source.description === 'string' ? source.description : '', path: typeof source.path === 'string' ? source.path : '', scope: typeof source.scope === 'string' ? source.scope : undefined, enabled: source.enabled !== false, managed: source.managed === true }]
  }).slice(0, 100)
}

async function resumeCanvasAgentThread(threadId: string): Promise<void> {
  if (!agentConnected.value || agentBusy.value) return
  if (!threadId) {
    agentMessages.value = []
    await startCanvasAgentThread()
    return
  }
  agentBusy.value = true
  agentError.value = ''
  try {
    const response = await agentBridge.resumeThread(threadId, 'request')
    if (response.conversation && typeof response.conversation === 'object') agentConversation.value = normalizeAgentConversation(response.conversation)
    if (Array.isArray(response.messages)) agentMessages.value = normalizeAgentMessages(response.messages)
  } catch (error) {
    agentError.value = error instanceof Error ? error.message : String(error)
  } finally {
    agentBusy.value = false
  }
}

function resolveCanvasAgentApproval(value: { requestId: string; decision: 'accept' | 'decline' | 'acceptForSession' }): void {
  if (!value.requestId) return
  void agentBridge.resolveApproval(value.requestId, value.decision).then(() => { agentApproval.value = null }).catch((error: unknown) => { agentError.value = error instanceof Error ? error.message : String(error) })
}

function toggleCanvasAgentSkill(value: { name: string; path: string; enabled: boolean }): void {
  if (!value.name || !agentConnected.value) return
  agentBusy.value = true
  void agentBridge.setSkillEnabled({ name: value.name, path: value.path }, value.enabled)
    .then(() => loadCanvasAgentSkills())
    .catch((error: unknown) => { agentError.value = error instanceof Error ? error.message : String(error) })
    .finally(() => { agentBusy.value = false })
}

function normalizeAgentMessages(value: unknown): Array<{ id: string; role: 'user' | 'assistant' | 'system' | 'error'; text: string }> {
  if (!Array.isArray(value)) return []
  return value.flatMap((item) => {
    if (!item || typeof item !== 'object') return []
    const source = item as Record<string, unknown>
    const text = typeof source.text === 'string' ? source.text : ''
    if (!text) return []
    const role: 'user' | 'assistant' | 'system' | 'error' = source.role === 'user' || source.role === 'system' || source.role === 'error' ? source.role : 'assistant'
    return [{ id: typeof source.id === 'string' ? source.id : `agent-message-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, role, text }]
  }).slice(-100)
}

function sendCanvasAgentTurn(prompt: string): void {
  if (!agentConnected.value || agentSending.value || !prompt.trim()) return
  const messageId = `agent-user-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  upsertCanvasAgentMessage({ id: messageId, role: 'user', text: prompt })
  agentSending.value = true
  agentError.value = ''
  void (async () => {
    if (!agentConversation.value.threadId || !['ready', 'warning'].includes(agentConversation.value.status)) await startCanvasAgentThread()
    const threadId = agentConversation.value.threadId
    if (!threadId) throw new Error(t('playground.canvasAgentConversationUnavailable'))
    await agentBridge.sendTurn({ prompt, threadId, conversationId: agentConversation.value.conversationId, messageId, messageText: prompt, expectedRevision: agentConversation.value.revision, permissionMode: 'request' })
  })().catch((error: unknown) => {
    agentError.value = error instanceof Error ? error.message : String(error)
  }).finally(() => { agentSending.value = false })
}

function cancelCanvasAgentTurn(): void {
  if (!agentSending.value) return
  void agentBridge.interrupt(agentConversation.value.threadId).catch(() => undefined)
  agentSending.value = false
}

function upsertCanvasAgentMessage(message: { id: string; role: 'user' | 'assistant' | 'system' | 'error'; text: string; append?: boolean }): void {
  const existing = agentMessages.value.find((item) => item.id === message.id)
  if (!existing) { agentMessages.value = [...agentMessages.value, { id: message.id, role: message.role, text: message.text }].slice(-100); return }
  existing.text = message.append ? `${existing.text}${message.text}` : message.text
}

function normalizeAgentConversation(value: unknown): CanvasAgentConversation {
  const source = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  const allowed = ['idle', 'preparing', 'ready', 'warning', 'running', 'failed']
  return {
    revision: numberValue(source.revision, 0),
    conversationId: typeof source.conversationId === 'string' ? source.conversationId : '',
    threadId: typeof source.threadId === 'string' ? source.threadId : '',
    status: allowed.includes(String(source.status)) ? String(source.status) as CanvasAgentConversation['status'] : 'idle',
    mcpStatuses: source.mcpStatuses && typeof source.mcpStatuses === 'object' ? source.mcpStatuses as CanvasAgentConversation['mcpStatuses'] : {},
    error: typeof source.error === 'string' ? source.error : undefined,
  }
}

function publishAgentState(): Promise<boolean> {
  if (!agentConnected.value) return Promise.resolve(false)
  if (agentPublishTimer !== null) window.clearTimeout(agentPublishTimer)
  return new Promise((resolve) => {
    agentPublishTimer = window.setTimeout(() => {
      agentPublishTimer = null
      void agentBridge.publish(createCanvasAgentSnapshot(activeProject.value, selectedNodeIds.value)).then(resolve)
    }, 80)
  })
}

async function handleCanvasAgentToolCall(call: CanvasAgentToolCall): Promise<unknown> {
  if (!call.name) throw new Error(t('playground.canvasAgentInvalidTool'))
  const project = activeProject.value
  if (!project) throw new Error(t('playground.canvasAgentNoProject'))
  if (call.name === 'canvas_get_state' || call.name === 'canvas_export_snapshot') return createCanvasAgentSnapshot(project, selectedNodeIds.value)
  if (call.name === 'canvas_get_selection') {
    const selected = new Set(selectedNodeIds.value)
    return { nodes: project.nodes.filter((node) => selected.has(node.id)).map((node) => ({ id: node.id, type: node.type, title: node.prompt, position: { x: node.x, y: node.y }, width: node.width, height: node.height })) }
  }
  const operations = agentToolToOperations(call.name, call.input, project)
  if (!operations.length) throw new Error(t('playground.canvasAgentUnsupportedTool', { name: call.name }))
  applyCanvasAgentOperations(operations)
  await publishAgentState()
  return createCanvasAgentSnapshot(activeProject.value, selectedNodeIds.value)
}

function applyCanvasAgentOperations(operations: unknown[]): void {
  const project = activeProject.value
  if (!project) return
  const safeOperations = operations.filter((operation): operation is Record<string, unknown> => Boolean(operation) && typeof operation === 'object')
  if (!safeOperations.length) return
  canvasStore.checkpoint()
  const generations: Array<{ nodeId: string; mode?: string; prompt?: string }> = []
  for (const operation of safeOperations) {
    const type = String(operation.type || '')
    if (type === 'add_node') {
      const candidateType = typeof operation.nodeType === 'string' ? operation.nodeType : ''
      const nodeType = candidateType === 'image' || candidateType === 'video' || candidateType === 'audio' || candidateType === 'config' || candidateType === 'text' || isPluginNodeType(candidateType as CanvasNodeData['type']) ? candidateType as CanvasNodeData['type'] : 'text'
      const node = agentNodeToCanvasNode({ id: operation.id, type: nodeType, title: operation.title, position: operation.position, width: operation.width, height: operation.height, metadata: operation.metadata }, nodeType)
      if (!project.nodes.some((item) => item.id === node.id)) canvasStore.addNode(node)
    } else if (type === 'update_node') {
      const id = String(operation.id || '')
      const patch = operation.patch && typeof operation.patch === 'object' ? operation.patch as Record<string, unknown> : {}
      const metadata = operation.metadata && typeof operation.metadata === 'object' ? operation.metadata as Record<string, unknown> : {}
      const localPatch: Partial<CanvasNodeData> = {}
      const position = patch.position && typeof patch.position === 'object' ? patch.position as Record<string, unknown> : undefined
      if (position) { if (typeof position.x === 'number') localPatch.x = position.x; if (typeof position.y === 'number') localPatch.y = position.y }
      if (typeof patch.width === 'number') localPatch.width = patch.width
      if (typeof patch.height === 'number') localPatch.height = patch.height
      if (typeof patch.title === 'string') localPatch.prompt = patch.title
      if (typeof metadata.prompt === 'string') localPatch.prompt = metadata.prompt
      if (typeof metadata.content === 'string') { localPatch.prompt = metadata.content; localPatch.textContent = metadata.content }
      if (typeof metadata.model === 'string') localPatch.model = metadata.model
      if (typeof metadata.pluginRenderer === 'string') localPatch.pluginRenderer = metadata.pluginRenderer as CanvasNodeData['pluginRenderer']
      if (metadata.pluginMetadata && typeof metadata.pluginMetadata === 'object' && !Array.isArray(metadata.pluginMetadata)) localPatch.pluginMetadata = metadata.pluginMetadata as Record<string, unknown>
      if (metadata.status === 'idle' || metadata.status === 'generating' || metadata.status === 'success' || metadata.status === 'error') localPatch.status = metadata.status
      if (Object.keys(localPatch).length) canvasStore.updateNode(id, localPatch)
    } else if (type === 'delete_node') {
      const ids = Array.isArray(operation.ids) ? operation.ids : [operation.id]
      ids.filter((id): id is string => typeof id === 'string').forEach((id) => canvasStore.removeNode(id))
      selectedNodeIds.value = new Set([...selectedNodeIds.value].filter((id) => !ids.includes(id)))
      if (selectedNodeId.value && ids.includes(selectedNodeId.value)) selectedNodeId.value = null
    } else if (type === 'delete_connections') {
      if (operation.all) project.connections.slice().forEach((connection) => canvasStore.removeConnection(connection.id))
      else (Array.isArray(operation.ids) ? operation.ids : [operation.id]).filter((id): id is string => typeof id === 'string').forEach((id) => canvasStore.removeConnection(id))
    } else if (type === 'connect_nodes') {
      const from = String(operation.fromNodeId || '')
      const to = String(operation.toNodeId || '')
      canvasStore.addConnection({ id: typeof operation.id === 'string' ? operation.id : `agent-connection-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, from, to, kind: 'reference' })
    } else if (type === 'set_viewport') {
      const viewport = operation.viewport && typeof operation.viewport === 'object' ? operation.viewport as Record<string, unknown> : {}
      canvasStore.updateViewport({ x: numberValue(viewport.x, project.viewport.x), y: numberValue(viewport.y, project.viewport.y), scale: Math.min(3, Math.max(0.2, numberValue(viewport.k, project.viewport.scale))) })
    } else if (type === 'select_nodes') {
      const ids = Array.isArray(operation.ids) ? operation.ids.filter((id): id is string => typeof id === 'string' && project.nodes.some((node) => node.id === id)) : []
      selectedNodeIds.value = new Set(ids)
      selectedNodeId.value = ids[0] ?? null
    } else if (type === 'run_generation') {
      generations.push({ nodeId: String(operation.nodeId || ''), mode: typeof operation.mode === 'string' ? operation.mode : undefined, prompt: typeof operation.prompt === 'string' ? operation.prompt : undefined })
    }
  }
  generations.forEach((generation) => {
    if (generation.prompt) updateNodePrompt(generation.nodeId, generation.prompt)
    let node = activeProject.value?.nodes.find((item) => item.id === generation.nodeId)
    if (!node) return
    if (node.type === 'config') {
      const mode = generation.mode === 'video' || generation.mode === 'audio' || generation.mode === 'text' ? generation.mode : 'image'
      const incomingPrompt = activeProject.value?.connections
        .filter((connection) => connection.to === node?.id)
        .map((connection) => activeProject.value?.nodes.find((candidate) => candidate.id === connection.from))
        .find((candidate) => candidate?.type === 'text')
      const generatedId = addNode(node.x + node.width + 120, node.y, mode)
      const requestedPrompt = generation.prompt && !generation.prompt.includes('@[node:') ? generation.prompt : ''
      const generatedPatch: Partial<CanvasNodeData> = { prompt: requestedPrompt || incomingPrompt?.textContent || incomingPrompt?.prompt || node.prompt }
      if (node.model) generatedPatch.model = node.model
      canvasStore.updateNode(generatedId, generatedPatch)
      canvasStore.addConnection({ id: `agent-generation-${node.id}-${generatedId}`, from: node.id, to: generatedId, kind: 'reference' })
      node = activeProject.value?.nodes.find((item) => item.id === generatedId)
    }
    if (!node) return
    if ((generation.mode || node.type) === 'video') void generateVideoNode(node.id)
    else if ((generation.mode || node.type) === 'audio') void generateAudioNode(node.id)
    else if ((generation.mode || node.type) === 'text') void generateTextNode(node.id)
    else void generateNode(node.id)
  })
}

function agentToolToOperations(name: string, input: Record<string, unknown>, project: CanvasProject): Record<string, unknown>[] {
  if (name === 'canvas_apply_ops') return Array.isArray(input.ops) ? input.ops as Record<string, unknown>[] : []
  if (name === 'canvas_create_node') return [{ type: 'add_node', nodeType: input.nodeType, title: input.title, position: { x: numberValue(input.x, nextAgentX(project)), y: numberValue(input.y, 0) }, width: input.width, height: input.height, metadata: input.metadata }]
  if (name === 'canvas_create_text_node') return [{ type: 'add_node', nodeType: 'text', title: input.title, position: { x: numberValue(input.x, nextAgentX(project)), y: numberValue(input.y, 0) }, width: input.width, height: input.height, metadata: { content: String(input.text || ''), status: 'success' } }]
  if (name === 'canvas_create_text_nodes') {
    const items = Array.isArray(input.items) ? input.items as Record<string, unknown>[] : []
    const row = input.direction !== 'column'
    const x = numberValue(input.x, nextAgentX(project)); const y = numberValue(input.y, 0); const gap = numberValue(input.gap, 40)
    return items.map((item, index) => ({ type: 'add_node', nodeType: 'text', title: item.title, position: { x: numberValue(item.x, row ? x + index * (340 + gap) : x), y: numberValue(item.y, row ? y : y + index * (240 + gap)) }, width: item.width, height: item.height, metadata: { content: String(item.text || ''), status: 'success' } }))
  }
  if (name === 'canvas_update_node' || name === 'canvas_update_node_text') return [{ type: 'update_node', id: input.id, patch: name === 'canvas_update_node_text' ? { title: input.title } : input.patch, metadata: name === 'canvas_update_node_text' ? { content: input.text, status: 'success' } : input.metadata }]
  if (name === 'canvas_move_nodes') return (Array.isArray(input.items) ? input.items : []).map((item) => {
    const value = item as Record<string, unknown>
    const current = project.nodes.find((node) => node.id === value.id)
    return { type: 'update_node', id: value.id, patch: { position: { x: numberValue(value.x, (current?.x ?? 0) + numberValue(value.dx, 0)), y: numberValue(value.y, (current?.y ?? 0) + numberValue(value.dy, 0)) } } }
  })
  if (name === 'canvas_resize_node') return [{ type: 'update_node', id: input.id, patch: { width: input.width, height: input.height } }]
  if (name === 'canvas_delete_nodes') return [{ type: 'delete_node', ids: input.ids }]
  if (name === 'canvas_connect_nodes') return (Array.isArray(input.connections) ? input.connections : []).map((item) => ({ type: 'connect_nodes', fromNodeId: (item as Record<string, unknown>).fromNodeId, toNodeId: (item as Record<string, unknown>).toNodeId }))
  if (name === 'canvas_select_nodes') return [{ type: 'select_nodes', ids: input.ids }]
  if (name === 'canvas_set_viewport') return [{ type: 'set_viewport', viewport: input.viewport }]
  if (name === 'canvas_run_generation') return [{ type: 'run_generation', nodeId: input.nodeId, mode: input.mode, prompt: input.prompt }]
  if (name === 'canvas_create_config_node') return [{ type: 'add_node', nodeType: 'config', title: input.title, position: { x: numberValue(input.x, nextAgentX(project)), y: numberValue(input.y, 0) }, width: input.width, height: input.height, metadata: { ...input, prompt: input.prompt, status: 'idle' } }]
  if (name === 'canvas_create_image_prompt_flow' || name === 'canvas_create_generation_flow' || name === 'canvas_generate_text' || name === 'canvas_generate_image' || name === 'canvas_generate_video' || name === 'canvas_generate_audio') {
    const mode = name === 'canvas_create_image_prompt_flow' ? 'image' : name.startsWith('canvas_generate_') ? name.replace('canvas_generate_', '') : input.mode || 'image'
    const textId = `agent-text-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const configId = `agent-config-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const x = numberValue(input.x, nextAgentX(project)); const y = numberValue(input.y, 0); const prompt = String(input.prompt || '')
    const ops: Record<string, unknown>[] = [{ type: 'add_node', id: textId, nodeType: 'text', title: input.title || '提示词', position: { x, y }, metadata: { content: prompt, status: 'success' } }, { type: 'add_node', id: configId, nodeType: 'config', title: `${mode} generation`, position: { x: x + 420, y }, metadata: { ...input, generationMode: mode, prompt: `@[node:${textId}]`, composerContent: prompt, status: 'idle' } }, { type: 'connect_nodes', fromNodeId: textId, toNodeId: configId }, { type: 'select_nodes', ids: [configId] }]
    if (Array.isArray(input.referenceNodeIds)) input.referenceNodeIds.filter((id): id is string => typeof id === 'string').forEach((id) => ops.push({ type: 'connect_nodes', fromNodeId: id, toNodeId: configId }))
    if (input.autoRun !== false || name.startsWith('canvas_generate_')) ops.push({ type: 'run_generation', nodeId: configId, mode, prompt: `@[node:${textId}]` })
    return ops
  }
  return []
}

function nextAgentX(project: CanvasProject): number {
  return project.nodes.length ? Math.max(...project.nodes.map((node) => node.x + node.width)) + 80 : 80
}

function numberValue(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function promptStorageKey(): string {
  return `sub2api.playground.canvas.prompts.user.${userId.value ?? 0}`
}

function loadCustomPrompts(): void {
  if (typeof localStorage === 'undefined') return
  try {
    const parsed = JSON.parse(localStorage.getItem(promptStorageKey()) || '[]') as unknown
    customPrompts.value = Array.isArray(parsed) ? parsed.filter((entry): entry is CanvasPromptEntry => (
      Boolean(entry) && typeof entry === 'object'
      && typeof (entry as CanvasPromptEntry).id === 'string'
      && typeof (entry as CanvasPromptEntry).title === 'string'
      && typeof (entry as CanvasPromptEntry).content === 'string'
      && (entry as CanvasPromptEntry).custom === true
    )).slice(0, 100) : []
  } catch {
    customPrompts.value = []
  }
}

function persistCustomPrompts(): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(promptStorageKey(), JSON.stringify(customPrompts.value.slice(0, 100)))
  } catch {
    // Prompt library remains usable in memory when storage is unavailable.
  }
}

function addCustomPrompt(prompt: CanvasPromptEntry): void {
  customPrompts.value = [prompt, ...customPrompts.value].slice(0, 100)
  persistCustomPrompts()
}

async function loadPromptLibraryData(): Promise<void> {
  if (promptRegistryLoaded.value) return
  promptRegistryLoaded.value = true
  const cached = await loadCachedCanvasPrompts().catch(() => [])
  if (cached.length) remotePrompts.value = cached
  void refreshPromptRegistry()
}

async function openPromptLibrary(): Promise<void> {
  promptLibraryOpen.value = true
  await loadPromptLibraryData()
}

async function refreshPromptRegistry(): Promise<void> {
  if (promptRegistryLoading.value) return
  promptRegistryLoading.value = true
  try {
    remotePrompts.value = await refreshCanvasPromptRegistry()
  } catch {
    // Keep the last valid cache and built-in prompts when the registry is offline.
  } finally {
    promptRegistryLoading.value = false
  }
}

function removeCustomPrompt(id: string): void {
  customPrompts.value = customPrompts.value.filter((prompt) => prompt.id !== id)
  persistCustomPrompts()
}

function insertPromptFromLibrary(content: string, close = true): void {
  const value = content.trim()
  const current = selectedNode.value
  if (!value) return
  if (current?.type === 'text' && current.kind !== 'result') {
    updateNodePrompt(current.id, value)
  } else {
    addNodeAtCenter('text')
    if (selectedNode.value) updateNodePrompt(selectedNode.value.id, value)
  }
  if (close) promptLibraryOpen.value = false
}

function updateSelectedPrompt(prompt: string): void {
  if (selectedNode.value) updateNodePrompt(selectedNode.value.id, prompt)
}

function updateNodeConfig(id: string, key: 'mode' | 'model' | 'count' | 'size' | 'quality' | 'background' | 'resolution' | 'duration' | 'aspectRatio' | 'audioVoice' | 'audioFormat' | 'audioSpeed' | 'audioInstructions' | 'reasoningEffort', value: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.checkpoint()
  if (key === 'model') {
    canvasStore.updateNode(id, { model: value, config: { ...(node.config ?? {}), model: value } })
    return
  }
  if (key === 'mode') {
    const mode = value === 'text' || value === 'video' || value === 'audio' ? value : 'image'
    const fallbackModel = mode === 'image' ? models.value[0]?.id : mode === 'video' ? videoModels.value[0]?.id : textModels.value[0]?.id
    canvasStore.updateNode(id, { model: fallbackModel || node.model, config: { ...(node.config ?? {}), mode, ...(fallbackModel ? { model: fallbackModel } : {}) } })
    return
  }
  const nextValue = key === 'count'
    ? String(Math.min(10, Math.max(1, Math.floor(Number(value) || 1))))
    : key === 'audioSpeed'
      ? String(Math.min(4, Math.max(0.25, Number.isFinite(Number(value)) ? Number(value) : 1)))
      : key === 'audioInstructions'
        ? value.slice(0, 4000)
        : value
  canvasStore.updateNode(id, { config: { ...(node.config ?? {}), [key]: nextValue } })
}

function generateConfigNode(id: string): void {
  const project = activeProject.value
  const configNode = project?.nodes.find((item) => item.id === id)
  if (!project || !configNode || configNode.type !== 'config' || configNode.status === 'generating') return
  const mode = configNode.config?.mode ?? 'image'
  const existingOutput = project.nodes.find((node) => node.sourceNodeId === id && node.kind !== 'result' && node.type === mode)
  if (existingOutput?.status === 'generating' || (existingOutput && project.nodes.some((node) => node.sourceNodeId === existingOutput.id && node.status === 'generating'))) return
  const outputId = existingOutput?.id ?? addNode(configNode.x + configNode.width + 120, configNode.y, mode)
  const incoming = project.connections
    .filter((connection) => connection.to === id)
    .map((connection) => project.nodes.find((node) => node.id === connection.from))
    .filter((node): node is CanvasNodeData => Boolean(node))
  const incomingText = incoming
    .filter((node) => node.type === 'text')
    .map((node) => node.textContent || node.prompt)
    .filter(Boolean)
  const prompt = resolveCanvasNodeMentions([...incomingText, configNode.prompt].filter(Boolean).join('\n\n'), project).trim()
  const selectedModel = configNode.config?.model || configNode.model
  canvasStore.updateNode(outputId, {
    kind: 'generator',
    prompt,
    sourceNodeId: id,
    ...(mode === 'text' ? { textContent: prompt, textCount: Math.min(10, Math.max(1, Number(configNode.config?.count ?? 1) || 1)) } : {}),
    ...(mode === 'image' ? { imageCount: Math.min(10, Math.max(1, Number(configNode.config?.count ?? 1) || 1)) } : {}),
    ...(selectedModel ? { model: selectedModel } : {}),
    config: configNode.config ? { ...configNode.config } : undefined,
  })
  if (!project.connections.some((connection) => connection.from === id && connection.to === outputId)) {
    canvasStore.addConnection({ id: `config-generation-${id}-${outputId}`, from: id, to: outputId, kind: 'reference' })
  }
  for (const source of incoming) {
    if (source.id === outputId || project.connections.some((connection) => connection.from === source.id && connection.to === outputId)) continue
    canvasStore.addConnection({ id: `config-input-${source.id}-${outputId}`, from: source.id, to: outputId, kind: 'reference' })
  }
  selectedNodeId.value = outputId
  selectedNodeIds.value = new Set([outputId])
  if (mode === 'text') void generateTextNode(outputId)
  else if (mode === 'video') void generateVideoNode(outputId)
  else if (mode === 'audio') void generateAudioNode(outputId)
  else generateNode(outputId)
}

function startProjectTitleEdit(): void {
  titleDraft.value = activeProject.value?.title ?? ''
  titleEditing.value = true
}

function commitProjectTitle(): void {
  if (!titleEditing.value) return
  const title = titleDraft.value.trim().slice(0, 80)
  titleEditing.value = false
  if (!title || title === activeProject.value?.title) return
  canvasStore.checkpoint()
  canvasStore.updateProject({ title })
}

function cancelProjectTitle(): void {
  titleEditing.value = false
}

function openNodeContextMenu(id: string, event: MouseEvent): void {
  connectionContextMenu.value = null
  selectedNodeId.value = id
  selectedNodeIds.value = new Set([id])
  contextMenu.value = { nodeId: id, x: event.clientX, y: event.clientY }
}

function openConnectionContextMenu(id: string, event: MouseEvent): void {
  if (!activeProject.value?.connections.some((connection) => connection.id === id)) return
  contextMenu.value = null
  selectedConnectionId.value = id
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
  connectionContextMenu.value = { connectionId: id, x: event.clientX, y: event.clientY }
}

function closeContextMenu(): void {
  contextMenu.value = null
  connectionContextMenu.value = null
}

function deleteConnectionFromContextMenu(): void {
  const id = connectionContextMenu.value?.connectionId
  if (!id) return
  deleteConnection(id)
  connectionContextMenu.value = null
}

function copyContextNode(): void {
  if (!contextMenu.value) return
  selectedNodeIds.value = new Set([contextMenu.value.nodeId])
  copySelectedNodes()
  closeContextMenu()
}

function duplicateContextNode(): void {
  if (!contextMenu.value) return
  selectedNodeIds.value = new Set([contextMenu.value.nodeId])
  copySelectedNodes()
  void pasteNodes()
  closeContextMenu()
}

function bringContextNodeFront(): void {
  const project = activeProject.value
  const id = contextMenu.value?.nodeId
  if (!project || !id) return
  const node = project.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.checkpoint()
  project.nodes = [...project.nodes.filter((item) => item.id !== id), node]
  canvasStore.persist()
  closeContextMenu()
}

function sendContextNodeBack(): void {
  const project = activeProject.value
  const id = contextMenu.value?.nodeId
  if (!project || !id) return
  const node = project.nodes.find((item) => item.id === id)
  if (!node) return
  canvasStore.checkpoint()
  project.nodes = [node, ...project.nodes.filter((item) => item.id !== id)]
  canvasStore.persist()
  closeContextMenu()
}

async function loadCanvasAssets(): Promise<void> {
  if (!userId.value) return
  try {
    // Asset previews are object URLs. Release only URLs that are not already
    // referenced by a canvas node, otherwise inserted images would break.
    for (const asset of assetPickerImages.value) {
      if (!imageObjectUrls.has(asset.url)) URL.revokeObjectURL(asset.url)
    }
    assetPickerImages.value = []
    assetPickerImages.value = await listPlaygroundGalleryImages(userId.value)
    for (const url of savedCanvasAssetUrls.values()) {
      if (!imageObjectUrls.has(url)) URL.revokeObjectURL(url)
    }
    savedCanvasAssetUrls.clear()
    savedCanvasAssets.value = listCanvasSavedAssets(userId.value)
    await Promise.all(savedCanvasAssets.value.map(async (asset) => {
      if (!asset.cacheKey) return
      const url = asset.kind === 'image'
        ? await restoreCachedPlaygroundImage(asset.cacheKey).catch(() => null)
        : await restoreCanvasMedia(asset.cacheKey).catch(() => null)
      if (url) {
        savedCanvasAssetUrls.set(asset.id, url)
        imageObjectUrls.add(url)
      }
    }))
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasAssetsLoadFailed'))
  }
}

async function openAssetPicker(): Promise<void> {
  await loadCanvasAssets()
  assetPickerOpen.value = true
}

function closeAssetPicker(): void {
  assetPickerOpen.value = false
  for (const asset of assetPickerImages.value) {
    if (!imageObjectUrls.has(asset.url)) URL.revokeObjectURL(asset.url)
  }
  assetPickerImages.value = []
}

function saveNodeAsCanvasAsset(id: string, imageIndex?: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || !userId.value) return
  const normalizedIndex = Math.max(0, Math.floor(imageIndex ?? node.primaryImageIndex ?? 0))
  const urls = node.imageUrls?.length ? node.imageUrls : node.imageUrl ? [node.imageUrl] : []
  const kind = node.type === 'image' ? 'image' : node.type === 'video' ? 'video' : node.type === 'audio' ? 'audio' : node.type === 'text' ? 'text' : null
  if (!kind) return
  const url = kind === 'image' ? urls[normalizedIndex] : kind === 'video' ? node.videoUrl : kind === 'audio' ? node.audioUrl : undefined
  const cacheKey = kind === 'image'
    ? node.imageCacheKeys?.[normalizedIndex] ?? (normalizedIndex === 0 ? node.imageCacheKey : undefined)
    : kind === 'video' ? node.videoCacheKey : kind === 'audio' ? node.audioCacheKey : undefined
  const text = kind === 'text' ? (node.textContent || node.prompt).trim() : undefined
  if (!url && !text) return
  const saved = persistCanvasAsset(userId.value, {
    kind,
    title: canvasNodeLabel(node),
    text,
    url,
    cacheKey,
    sourceNodeId: node.id,
    imageIndex: kind === 'image' ? normalizedIndex : undefined,
  })
  savedCanvasAssets.value = [saved, ...savedCanvasAssets.value.filter((asset) => asset.id !== saved.id)]
  appStore.showSuccess(t('playground.canvasAssetSaved'))
}

function removeSavedCanvasAsset(id: string): void {
  if (!userId.value) return
  const url = savedCanvasAssetUrls.get(id)
  if (url) {
    URL.revokeObjectURL(url)
    imageObjectUrls.delete(url)
  }
  savedCanvasAssetUrls.delete(id)
  removeCanvasSavedAsset(userId.value, id)
  savedCanvasAssets.value = savedCanvasAssets.value.filter((asset) => asset.id !== id)
}

function insertGalleryAsset(key: string, close = true): void {
  const asset = assetPickerImages.value.find((item) => item.key === key)
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!asset || !project || !rect) return
  canvasStore.checkpoint()
  const now = Date.now()
  const x = (rect.width / 2 - project.viewport.x) / project.viewport.scale - 130
  const y = (rect.height / 2 - project.viewport.y) / project.viewport.scale - 160
  const node: CanvasNodeData = {
    id: `asset-${now}-${Math.random().toString(36).slice(2, 8)}`, type: 'image', kind: 'result',
    x, y, width: 260, height: 320, prompt: asset.prompt, model: asset.model, imageUrl: asset.url, imageCacheKey: asset.key,
    imageUrls: [asset.url], status: 'success', resultIndex: 0, createdAt: now, updatedAt: now,
  }
  canvasStore.addNode(node)
  imageObjectUrls.add(asset.url)
  selectedNodeId.value = node.id
  selectedNodeIds.value = new Set([node.id])
  if (close) closeAssetPicker()
}

function insertAssetFromPicker(key: string): void {
  const asset = canvasAssets.value.find((item) => item.key === key)
  if (!asset) return
  if (asset.source === 'gallery') insertGalleryAsset(asset.key.slice('gallery:'.length), true)
  else {
    insertCanvasAsset(asset)
    closeAssetPicker()
  }
}

function insertCanvasAsset(asset: CanvasAssetItem): void {
  if (asset.source === 'gallery') {
    const galleryKey = asset.key.slice('gallery:'.length)
    insertGalleryAsset(galleryKey, false)
    return
  }
  const source = asset.nodeId ? activeProject.value?.nodes.find((node) => node.id === asset.nodeId) : null
  const project = activeProject.value
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!project || !rect) return
  canvasStore.checkpoint()
  const now = Date.now()
  const type = source?.type ?? asset.kind
  const dimensions = source ? { width: source.width, height: source.height } : nodeDimensions(type)
  const width = dimensions.width || nodeDimensions(type).width
  const height = dimensions.height || nodeDimensions(type).height
  const x = (rect.width / 2 - project.viewport.x) / project.viewport.scale - width / 2
  const y = (rect.height / 2 - project.viewport.y) / project.viewport.scale - height / 2
  const clone: CanvasNodeData = {
    ...(source ?? {
      type,
      kind: 'result',
      prompt: asset.kind === 'text' ? asset.text || asset.title : asset.title,
      model: asset.kind === 'image' ? selectedModel.value : selectedTextModel.value,
      status: 'success',
      imageCount: 1,
    }),
    id: `asset-node-${now}-${Math.random().toString(36).slice(2, 8)}`,
    x,
    y,
    width,
    height,
    kind: source?.kind ?? 'result',
    groupId: undefined,
    sourceNodeId: undefined,
    status: source?.status === 'error' ? 'success' : source?.status ?? 'success',
    errorMessage: undefined,
    videoRequestId: undefined,
    referenceImages: source?.referenceImages ? source.referenceImages.map((item) => ({ ...item })) : undefined,
    textVariants: source?.textVariants ? source.textVariants.map((item) => ({ ...item })) : undefined,
    imageUrls: source?.imageUrls ? [...source.imageUrls] : undefined,
    imageVariants: source?.imageVariants ? source.imageVariants.map((item) => ({ ...item })) : undefined,
    imageCacheKeys: source?.imageCacheKeys ? [...source.imageCacheKeys] : undefined,
    createdAt: now,
    updatedAt: now,
  }
  if (asset.kind === 'image' && asset.url) {
    clone.imageUrl = asset.url
    clone.imageUrls = [asset.url]
    clone.imageVariants = [{ id: `${clone.id}-image-0`, status: 'success', url: asset.url, cacheKey: asset.cacheKey }]
    clone.primaryImageIndex = 0
    clone.imageCacheKey = asset.cacheKey
    clone.imageCacheKeys = asset.cacheKey ? [asset.cacheKey] : undefined
  } else if (asset.kind === 'video') {
    clone.videoUrl = asset.url
  } else if (asset.kind === 'audio') {
    clone.audioUrl = asset.url
  } else if (asset.kind === 'text') {
    clone.prompt = asset.text || clone.prompt
    clone.textContent = asset.text || clone.textContent
  }
  canvasStore.addNode(clone)
  if (clone.imageUrl) imageObjectUrls.add(clone.imageUrl)
  if (clone.videoUrl) imageObjectUrls.add(clone.videoUrl)
  if (clone.audioUrl) imageObjectUrls.add(clone.audioUrl)
  selectedNodeId.value = clone.id
  selectedNodeIds.value = new Set([clone.id])
}

function deleteNode(id: string): void {
  canvasStore.checkpoint()
  cancelNodeGeneration(id)
  cancelVideoNode(id)
  generationTokens.delete(id)
  updateGenerationSet(activeGenerationIds, id, false)
  updateGenerationSet(queuedGenerationIds, id, false)
  canvasStore.removeNode(id)
  selectedNodeIds.value = new Set([...selectedNodeIds.value].filter((item) => item !== id))
  if (selectedNodeId.value === id) selectedNodeId.value = selectedNodeIds.value.values().next().value ?? null
}

function deleteSelectedNodes(): void {
  if (!selectedNodeIds.value.size) return
  canvasStore.checkpoint()
  for (const id of [...selectedNodeIds.value]) {
    cancelNodeGeneration(id)
    cancelVideoNode(id)
    generationTokens.delete(id)
    updateGenerationSet(activeGenerationIds, id, false)
    updateGenerationSet(queuedGenerationIds, id, false)
    canvasStore.removeNode(id)
  }
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
}

function deleteSelected(): void {
  if (selectedNodeIds.value.size) {
    deleteSelectedNodes()
    return
  }
  if (selectedConnectionId.value) deleteConnection(selectedConnectionId.value)
}

function updateNodeImageCount(id: string, value: number): void {
  const count = Number.isFinite(value) ? Math.min(10, Math.max(1, Math.floor(value))) : 4
  canvasStore.updateNode(id, { imageCount: count })
}

function newProject(): void {
  const title = window.prompt(t('playground.canvasProjectName'), t('playground.canvasProjectDefault'))
  if (title?.trim()) canvasStore.addProject(title.trim())
}

function duplicateActiveProject(): void {
  const project = activeProject.value
  if (!project) return
  canvasStore.checkpoint()
  canvasStore.duplicateProject(project.id)
}

function renameProject(id: string): void {
  const project = canvasStore.state.projects.find((item) => item.id === id)
  if (!project) return
  const title = window.prompt(t('playground.canvasProjectName'), project.title)
  if (title?.trim()) {
    canvasStore.state.activeProjectId = id
    canvasStore.checkpoint()
    canvasStore.updateProject({ title: title.trim() })
  }
}

function deleteProject(id: string): void {
  if (!window.confirm(t('common.deleteProjectConfirm'))) return
  canvasStore.checkpoint()
  canvasStore.deleteProject(id)
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
}

function fitCanvas(): void {
  const nodes = activeProject.value?.nodes ?? []
  const rect = canvasRef.value?.getBoundingClientRect()
  if (!nodes.length || !rect) {
    updateCanvasViewport({ x: 40, y: 40, scale: 1 }, { checkpoint: true })
    return
  }
  const minX = Math.min(...nodes.map((node) => node.x))
  const minY = Math.min(...nodes.map((node) => node.y))
  const maxX = Math.max(...nodes.map((node) => node.x + node.width))
  const maxY = Math.max(...nodes.map((node) => node.y + node.height))
  const scale = Math.min(1.2, Math.max(0.35, Math.min((rect.width - 100) / (maxX - minX), (rect.height - 100) / (maxY - minY))))
  updateCanvasViewport({ x: (rect.width - (maxX - minX) * scale) / 2 - minX * scale, y: (rect.height - (maxY - minY) * scale) / 2 - minY * scale, scale }, { checkpoint: true })
}

function resetCanvasView(): void {
  updateCanvasViewport({ x: 0, y: 0, scale: 1 }, { checkpoint: true })
}

function closeImageEditor(): void {
  imageEditorOpen.value = false
  imageEditorTarget.value = null
  imageEditorUrl.value = ''
}

function closeSplitDialog(): void {
  splitDialogOpen.value = false
  splitDialogTarget.value = null
  splitDialogUrl.value = ''
}

async function openImageSplit(nodeId: string, imageIndex: number): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const url = node?.imageUrls?.[imageIndex] ?? node?.imageUrl
  if (!node || node.type !== 'image' || !url) return
  splitDialogTarget.value = { nodeId, imageIndex }
  splitDialogUrl.value = url
  splitDialogOpen.value = true
}

function closeUpscaleDialog(): void {
  upscaleDialogOpen.value = false
  upscaleDialogTarget.value = null
  upscaleDialogUrl.value = ''
}

function closeImageViewer(): void {
  imageViewerOpen.value = false
  imageViewerUrl.value = ''
  imageViewerTitle.value = ''
}

function openImageViewer(nodeId: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const url = node?.imageUrls?.[imageIndex] ?? node?.imageUrl
  if (!node || node.type !== 'image' || !url) return
  imageViewerUrl.value = url
  imageViewerTitle.value = node.prompt
  imageViewerOpen.value = true
}

function closeMaskDialog(): void {
  maskDialogOpen.value = false
  maskDialogTarget.value = null
  maskDialogUrl.value = ''
}

function openImageMask(nodeId: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const url = node?.imageUrls?.[imageIndex] ?? node?.imageUrl
  if (!node || node.type !== 'image' || !url) return
  maskDialogTarget.value = { nodeId, imageIndex }
  maskDialogUrl.value = url
  maskDialogOpen.value = true
}

async function applyImageMask(maskDataUrl: string): Promise<void> {
  const target = maskDialogTarget.value
  const node = target ? activeProject.value?.nodes.find((item) => item.id === target.nodeId) : null
  const keyId = selectedKeyId.value
  const model = node?.model || selectedModel.value
  if (!node || !target || !keyId || !model || !node.prompt.trim()) {
    appStore.showError(t('playground.canvasMaskRequiresPrompt'))
    return
  }
  const blob = await resolveImageBlob(node, target.imageIndex)
  if (!blob) {
    appStore.showError(t('playground.canvasMaskFailed'))
    return
  }
  const toDataUrl = (value: Blob): Promise<string> => new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(value)
  })
  const sourceDataUrl = await toDataUrl(blob).catch(() => '')
  if (!sourceDataUrl) {
    appStore.showError(t('playground.canvasMaskFailed'))
    return
  }
  const now = Date.now()
  const resultNodeId = `mask-${node.id}-${now}-${Math.random().toString(36).slice(2, 8)}`
  maskDialogBusy.value = true
  canvasStore.checkpoint()
  canvasStore.addNode({
    id: resultNodeId,
    type: 'image',
    kind: 'result',
    x: node.x + node.width + 120,
    y: node.y,
    width: node.width,
    height: node.height,
    prompt: node.prompt,
    model,
    status: 'generating',
    sourceNodeId: node.id,
    resultIndex: 0,
    createdAt: now,
    updatedAt: now,
  })
  canvasStore.addConnection({ id: `connection-${node.id}-${resultNodeId}`, from: node.id, to: resultNodeId, kind: 'result' })
  try {
    const editBody: Record<string, unknown> = {
      model,
      prompt: node.prompt,
      size: node.config?.size ?? '1024x1024',
      quality: node.config?.quality ?? 'auto',
      n: 1,
      output_format: 'png',
      ...(node.config?.background && node.config.background !== 'auto' ? { background: node.config.background } : {}),
      // GPT Image always returns base64 and rejects response_format.
      ...(/^gpt-image-/i.test(model) ? {} : { response_format: 'b64_json' }),
    }
    const response = await sendPlaygroundImageGeneration(keyId, editBody, {
      referenceImages: [{ id: `mask-source-${now}`, name: 'source.png', mimeType: blob.type || 'image/png', size: blob.size, dataUrl: sourceDataUrl }],
      mask: { id: `mask-${now}`, name: 'mask.png', mimeType: 'image/png', size: maskDataUrl.length, dataUrl: maskDataUrl },
    })
    const sourceUrl = playgroundImageUrl(response.data[0], 'png')
    if (!sourceUrl) throw new Error(t('playground.canvasMaskFailed'))
    const cacheKey = userId.value ? createPlaygroundImageCacheKey(userId.value, keyId, resultNodeId, 0) : ''
    const imageUrl = cacheKey && userId.value ? await cachePlaygroundImage(cacheKey, userId.value, keyId, sourceUrl, { prompt: node.prompt, model }) : sourceUrl
    if (!imageUrl) throw new Error(t('playground.canvasMaskFailed'))
    if (imageUrl.startsWith('blob:')) imageObjectUrls.add(imageUrl)
    canvasStore.updateNode(resultNodeId, { imageUrl, imageUrls: [imageUrl], imageCacheKey: cacheKey || undefined, imageCacheKeys: cacheKey ? [cacheKey] : undefined, status: 'success' })
    selectedNodeId.value = resultNodeId
    selectedNodeIds.value = new Set([resultNodeId])
    closeMaskDialog()
  } catch (error) {
    canvasStore.removeNode(resultNodeId)
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasMaskFailed'))
  } finally {
    maskDialogBusy.value = false
  }
}

function closeAngleDialog(): void {
  angleDialogOpen.value = false
  angleDialogTarget.value = null
  angleDialogUrl.value = ''
}

function openImageAngle(nodeId: string, imageIndex: number): void {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const url = node?.imageUrls?.[imageIndex] ?? node?.imageUrl
  if (!node || node.type !== 'image' || !url) return
  angleDialogTarget.value = { nodeId, imageIndex }
  angleDialogUrl.value = url
  angleDialogOpen.value = true
}

async function applyImageAngle(params: CanvasImageAngleParams): Promise<void> {
  const target = angleDialogTarget.value
  const node = target ? activeProject.value?.nodes.find((item) => item.id === target.nodeId) : null
  const blob = node && target ? await resolveImageBlob(node, target.imageIndex) : null
  if (!node || !target || !blob) return
  angleDialogBusy.value = true
  try {
    const result = await transformCanvasImageAngle(blob, params)
    const encoded = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(result.blob)
    })
    const owner = userId.value
    const keyId = selectedKeyId.value
    const cacheKey = owner && keyId ? createPlaygroundImageCacheKey(owner, keyId, `${node.id}-angle-${Date.now()}`, 0) : ''
    const url = cacheKey && owner && keyId ? await cachePlaygroundImage(cacheKey, owner, keyId, encoded, { prompt: node.prompt, model: node.model }) : URL.createObjectURL(result.blob)
    if (!url) throw new Error(t('playground.canvasAngleFailed'))
    imageObjectUrls.add(url)
    const now = Date.now()
    const resultNode: CanvasNodeData = {
      id: `angle-${node.id}-${now}-${Math.random().toString(36).slice(2, 8)}`,
      type: 'image', kind: 'result', x: node.x + node.width + 120, y: node.y,
      width: Math.max(260, Math.min(640, result.width / 2)), height: Math.max(320, Math.min(760, result.height / 2)),
      prompt: node.prompt, model: node.model, imageUrl: url, imageUrls: [url], imageCacheKey: cacheKey || undefined, imageCacheKeys: cacheKey ? [cacheKey] : undefined,
      status: 'success', sourceNodeId: node.id, resultIndex: 0, createdAt: now, updatedAt: now,
    }
    canvasStore.checkpoint()
    canvasStore.addNode(resultNode)
    canvasStore.addConnection({ id: `connection-${node.id}-${resultNode.id}`, from: node.id, to: resultNode.id, kind: 'result' })
    selectedNodeId.value = resultNode.id
    selectedNodeIds.value = new Set([resultNode.id])
    closeAngleDialog()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasAngleFailed'))
  } finally {
    angleDialogBusy.value = false
  }
}

async function openImageUpscale(nodeId: string, imageIndex: number): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const url = node?.imageUrls?.[imageIndex] ?? node?.imageUrl
  if (!node || node.type !== 'image' || !url) return
  upscaleDialogTarget.value = { nodeId, imageIndex }
  upscaleDialogUrl.value = url
  upscaleDialogOpen.value = true
}

async function applyImageUpscale(params: CanvasImageUpscaleParams): Promise<void> {
  const target = upscaleDialogTarget.value
  const node = target ? activeProject.value?.nodes.find((item) => item.id === target.nodeId) : null
  const blob = node && target ? await resolveImageBlob(node, target.imageIndex) : null
  if (!node || !target || !blob) return
  upscaleDialogBusy.value = true
  upscaleNodeIds.value = new Set([...upscaleNodeIds.value, node.id])
  try {
    const result = await upscaleCanvasImage(blob, params)
    const encoded = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsDataURL(result.blob)
    })
    const owner = userId.value
    const keyId = selectedKeyId.value
    const cacheKey = owner && keyId ? createPlaygroundImageCacheKey(owner, keyId, `${node.id}-upscale-${Date.now()}`, 0) : ''
    const url = cacheKey && owner && keyId
      ? await cachePlaygroundImage(cacheKey, owner, keyId, encoded, { prompt: `${node.prompt} · ${params.targetLongEdge}px`, model: node.model })
      : URL.createObjectURL(result.blob)
    if (!url) throw new Error(t('playground.canvasUpscaleFailed'))
    imageObjectUrls.add(url)
    const now = Date.now()
    const upscaleNode: CanvasNodeData = {
      id: `upscale-${node.id}-${now}-${Math.random().toString(36).slice(2, 8)}`,
      type: 'image', kind: 'result', x: node.x + node.width + 120, y: node.y,
      width: Math.max(260, Math.min(640, result.width / 2)), height: Math.max(320, Math.min(760, result.height / 2)),
      prompt: `${node.prompt} · ${params.targetLongEdge}px`, model: node.model,
      imageUrl: url, imageUrls: [url], imageCacheKey: cacheKey || undefined, imageCacheKeys: cacheKey ? [cacheKey] : undefined,
      status: 'success', sourceNodeId: node.id, resultIndex: 0, createdAt: now, updatedAt: now,
    }
    canvasStore.checkpoint()
    canvasStore.addNode(upscaleNode)
    canvasStore.addConnection({ id: `connection-${node.id}-${upscaleNode.id}`, from: node.id, to: upscaleNode.id, kind: 'result' })
    selectedNodeId.value = upscaleNode.id
    selectedNodeIds.value = new Set([upscaleNode.id])
    closeUpscaleDialog()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasUpscaleFailed'))
  } finally {
    upscaleDialogBusy.value = false
    const next = new Set(upscaleNodeIds.value)
    next.delete(node.id)
    upscaleNodeIds.value = next
  }
}

async function applyImageSplit(grid: CanvasImageGrid): Promise<void> {
  const target = splitDialogTarget.value
  const node = target ? activeProject.value?.nodes.find((item) => item.id === target.nodeId) : null
  const blob = node && target ? await resolveImageBlob(node, target.imageIndex) : null
  const project = activeProject.value
  if (!node || !target || !blob || !project) return
  splitDialogBusy.value = true
  try {
    const pieces = await splitCanvasImage(blob, grid)
    const baseX = node.x + node.width + 120
    const baseY = node.y
    const splitNodes = await Promise.all(pieces.map(async (piece, index) => {
      const now = Date.now() + index
      const cacheKey = userId.value && selectedKeyId.value ? createPlaygroundImageCacheKey(userId.value, selectedKeyId.value, `${node.id}-split-${now}`, index) : ''
      const encoded = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result))
        reader.onerror = () => reject(reader.error)
        reader.readAsDataURL(piece.blob)
      })
      const url = cacheKey && userId.value && selectedKeyId.value
        ? await cachePlaygroundImage(cacheKey, userId.value, selectedKeyId.value, encoded, { prompt: `${node.prompt} (${piece.row + 1},${piece.column + 1})`, model: node.model })
        : URL.createObjectURL(piece.blob)
      if (!url) throw new Error(t('playground.canvasSplitFailed'))
      imageObjectUrls.add(url)
      return {
        id: `split-${node.id}-${now}-${Math.random().toString(36).slice(2, 8)}`,
        type: 'image', kind: 'result', x: baseX + piece.column * 300, y: baseY + piece.row * 340,
        width: 260, height: 320, prompt: `${node.prompt} (${piece.row + 1}, ${piece.column + 1})`, model: node.model,
        imageUrl: url, imageUrls: [url], imageCacheKey: cacheKey || undefined, imageCacheKeys: cacheKey ? [cacheKey] : undefined,
        status: 'success', sourceNodeId: node.id, resultIndex: index, createdAt: now, updatedAt: now,
      } as CanvasNodeData
    }))
    canvasStore.checkpoint()
    splitNodes.forEach((splitNode) => {
      canvasStore.addNode(splitNode)
      canvasStore.addConnection({ id: `connection-${node.id}-${splitNode.id}`, from: node.id, to: splitNode.id, kind: 'result' })
    })
    const lastNode = splitNodes.at(-1)
    if (lastNode) {
      selectedNodeId.value = lastNode.id
      selectedNodeIds.value = new Set([lastNode.id])
    }
    closeSplitDialog()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasSplitFailed'))
  } finally {
    splitDialogBusy.value = false
  }
}

async function resolveImageBlob(node: CanvasNodeData, imageIndex: number): Promise<Blob | null> {
  const variant = node.imageVariants?.[imageIndex]
  const cacheKey = variant?.cacheKey ?? node.imageCacheKeys?.[imageIndex] ?? node.imageCacheKey
  if (cacheKey) {
    const cached = await readCachedPlaygroundImageBlob(cacheKey).catch(() => null)
    if (cached) return cached
  }
  const url = variant?.url ?? node.imageUrls?.[imageIndex] ?? node.imageUrl
  if (!url) return null
  if (url.startsWith('data:')) {
    try {
      const match = url.match(/^data:([^;,]+)?(;base64)?,(.*)$/s)
      if (!match) return null
      const mimeType = match[1] || 'application/octet-stream'
      const payload = match[3] || ''
      if (match[2]) {
        const binary = atob(payload)
        const bytes = new Uint8Array(binary.length)
        for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
        return new Blob([bytes], { type: mimeType })
      }
      return new Blob([decodeURIComponent(payload)], { type: mimeType })
    } catch {
      return null
    }
  }
  try {
    const response = await fetch(url)
    return response.ok ? await response.blob() : null
  } catch {
    return null
  }
}

async function reversePromptImage(nodeId: string, imageIndex: number): Promise<void> {
  const project = activeProject.value
  const node = project?.nodes.find((item) => item.id === nodeId)
  const keyId = selectedTextKeyId.value
  const model = selectedTextModel.value || assistantModel.value
  if (!project || !node || node.type !== 'image' || !keyId || !model || reversePromptIds.value.has(nodeId)) return
  const blob = await resolveImageBlob(node, imageIndex)
  if (!blob) {
    appStore.showError(t('playground.canvasReversePromptFailed'))
    return
  }
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error ?? new Error(t('playground.canvasReversePromptFailed')))
    reader.readAsDataURL(blob)
  }).catch(() => '')
  if (!dataUrl) {
    appStore.showError(t('playground.canvasReversePromptFailed'))
    return
  }
  reversePromptIds.value = new Set([...reversePromptIds.value, nodeId])
  const resultNodeId = `reverse-prompt-${nodeId}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const now = Date.now()
  canvasStore.checkpoint()
  canvasStore.addNode({
    id: resultNodeId,
    type: 'text',
    kind: 'result',
    x: node.x + node.width + 120,
    y: node.y,
    width: 360,
    height: 320,
    prompt: '',
    textContent: '',
    model,
    status: 'generating',
    sourceNodeId: node.id,
    resultIndex: 0,
    createdAt: now,
    updatedAt: now,
  })
  canvasStore.addConnection({ id: `connection-${node.id}-${resultNodeId}`, from: node.id, to: resultNodeId, kind: 'result' })
  try {
    const result = await sendPlaygroundChat(keyId, {
      model,
      stream: true,
      messages: [
        { role: 'system', content: t('playground.canvasReversePromptSystem') },
        { role: 'user', content: [
          { type: 'text', text: t('playground.canvasReversePromptInstruction') },
          { type: 'image_url', image_url: { url: dataUrl } },
        ] },
      ],
    }, {
      signal: new AbortController().signal,
      onDelta: (delta) => {
        const target = activeProject.value?.nodes.find((item) => item.id === resultNodeId)
        if (!target) return
        const content = `${target.textContent ?? ''}${delta.contentDelta}`
        canvasStore.updateNode(resultNodeId, { textContent: content, prompt: content })
      },
    })
    const content = result.content.trim()
    if (!content) throw new Error(t('playground.canvasReversePromptFailed'))
    canvasStore.updateNode(resultNodeId, { textContent: content, prompt: content, status: 'success' })
    selectedNodeId.value = resultNodeId
    selectedNodeIds.value = new Set([resultNodeId])
  } catch (error) {
    if (activeProject.value?.nodes.some((item) => item.id === resultNodeId)) canvasStore.removeNode(resultNodeId)
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasReversePromptFailed'))
  } finally {
    const next = new Set(reversePromptIds.value)
    next.delete(nodeId)
    reversePromptIds.value = next
  }
}

async function openImageEditor(nodeId: string, imageIndex: number): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  if (!node || node.type !== 'image') return
  const slot = canvasImageSlots(node)[imageIndex]
  const url = slot?.status === 'success' ? slot.url : node.imageUrl
  if (!url) return
  imageEditorTarget.value = { nodeId, imageIndex }
  imageEditorUrl.value = url
  imageEditorOpen.value = true
}

async function quickTransformImage(nodeId: string, imageIndex: number, operation: 'rotate-left' | 'rotate-right' | 'flip-horizontal'): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === nodeId)
  const blob = node ? await resolveImageBlob(node, imageIndex) : null
  if (!node || !blob) return
  const transform: CanvasImageTransform = {
    crop: { x: 0, y: 0, width: 1, height: 1 },
    rotation: operation === 'rotate-left' ? 270 : operation === 'rotate-right' ? 90 : 0,
    flipX: operation === 'flip-horizontal',
    flipY: false,
  }
  await applyImageTransformToNode(node, imageIndex, blob, transform)
}

async function applyImageTransform(transform: CanvasImageTransform): Promise<void> {
  const target = imageEditorTarget.value
  const node = target ? activeProject.value?.nodes.find((item) => item.id === target.nodeId) : null
  const blob = node && target ? await resolveImageBlob(node, target.imageIndex) : null
  if (!node || !target || !blob) return
  if (await applyImageTransformToNode(node, target.imageIndex, blob, transform)) closeImageEditor()
}

async function applyImageTransformToNode(node: CanvasNodeData, imageIndex: number, blob: Blob, transform: CanvasImageTransform): Promise<boolean> {
  imageEditorBusy.value = true
  try {
    const result = await transformCanvasImage(blob, transform)
    const owner = userId.value
    const keyId = selectedKeyId.value
    const cacheKey = owner && keyId ? createPlaygroundImageCacheKey(owner, keyId, `${node.id}-edit-${Date.now()}`, imageIndex) : ''
    const dataUrl = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error ?? new Error('Unable to read edited image.'))
      reader.readAsDataURL(result.blob)
    })
    const url = cacheKey && owner && keyId ? await cachePlaygroundImage(cacheKey, owner, keyId, dataUrl, { prompt: node.prompt, model: node.model }) : URL.createObjectURL(result.blob)
    if (!url) throw new Error(t('playground.canvasImageApplyFailed'))
    imageObjectUrls.add(url)
    const imageSlots = canvasImageSlots(node)
    if (!imageSlots[imageIndex] || imageSlots[imageIndex].status !== 'success') return false
    imageSlots[imageIndex] = { ...imageSlots[imageIndex], status: 'success', url, cacheKey: cacheKey || imageSlots[imageIndex].cacheKey }
    canvasStore.checkpoint()
    canvasStore.updateNode(node.id, { ...syncImageVariantFields(imageSlots, node.primaryImageIndex), imageTransform: transform, width: Math.max(240, Math.min(900, result.width / 2)), height: Math.max(260, Math.min(900, result.height / 2)), status: 'success' })
    return true
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasImageApplyFailed'))
    return false
  } finally {
    imageEditorBusy.value = false
  }
}

function archiveExtension(mimeType: string, kind: 'image' | 'media'): string {
  if (mimeType.includes('png')) return 'png'
  if (mimeType.includes('jpeg')) return 'jpg'
  if (mimeType.includes('webp')) return 'webp'
  if (mimeType.includes('gif')) return 'gif'
  if (mimeType.includes('mp4')) return 'mp4'
  if (mimeType.includes('webm')) return 'webm'
  if (mimeType.includes('mpeg') || mimeType.includes('mp3')) return 'mp3'
  if (mimeType.includes('wav')) return 'wav'
  if (mimeType.includes('ogg')) return 'ogg'
  return kind === 'image' ? 'png' : 'bin'
}

function collectCanvasArchiveAssets(state: CanvasPersistedState): Array<{ key: string; kind: 'image' | 'media' }> {
  const assets = new Map<string, 'image' | 'media'>()
  for (const project of state.projects) {
    for (const node of project.nodes) {
      for (const key of node.imageCacheKeys?.length ? node.imageCacheKeys : node.imageCacheKey ? [node.imageCacheKey] : []) assets.set(key, 'image')
      const contentCacheKey = typeof node.pluginMetadata?.contentCacheKey === 'string' ? node.pluginMetadata.contentCacheKey : ''
      if (contentCacheKey) assets.set(contentCacheKey, 'image')
      for (const reference of node.referenceImages ?? []) if (reference.cacheKey) assets.set(reference.cacheKey, 'image')
      if (node.videoCacheKey) assets.set(node.videoCacheKey, 'media')
      if (node.audioCacheKey) assets.set(node.audioCacheKey, 'media')
    }
  }
  return [...assets].map(([key, kind]) => ({ key, kind }))
}

async function exportCanvas(selection: Set<string> = selectedNodeIds.value): Promise<void> {
  const state = buildCanvasExportState(selection)
  const entries: Array<{ name: string; data: BlobPart }> = []
  const assets: Array<{ key: string; kind: 'image' | 'media'; path: string; mimeType: string }> = []
  for (const [index, asset] of collectCanvasArchiveAssets(state).entries()) {
    const blob = asset.kind === 'image'
      ? await readCachedPlaygroundImageBlob(asset.key).catch(() => null)
      : await readCachedCanvasMediaBlob(asset.key).catch(() => null)
    if (!blob) continue
    const path = `assets/${String(index + 1).padStart(4, '0')}.${archiveExtension(blob.type, asset.kind)}`
    assets.push({ ...asset, path, mimeType: blob.type || (asset.kind === 'image' ? 'image/png' : 'application/octet-stream') })
    entries.push({ name: path, data: blob })
  }
  entries.unshift({
    name: 'projects.json',
    data: JSON.stringify({ app: 'sub2api-canvas', version: 1, exportedAt: new Date().toISOString(), state, assets }, null, 2),
  })
  const blob = await createCanvasArchive(entries)
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `sub2api-canvas-${new Date().toISOString().slice(0, 10)}.zip`
  link.click()
  URL.revokeObjectURL(url)
}

async function exportSelectedCanvasElements(): Promise<void> {
  if (!canvasElementSelection.value.size) return
  try {
    await exportCanvas(canvasElementSelection.value)
    canvasElementSelectMode.value = false
    canvasElementSelection.value = new Set()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasExportFailed'))
  }
}

function webdavStorageKey(): string {
  return `sub2api.playground.canvas.webdav.${userId.value ?? 0}`
}

function loadWebdavConfig(): void {
  if (typeof localStorage === 'undefined') return
  try {
    const parsed = JSON.parse(localStorage.getItem(webdavStorageKey()) || '') as Partial<CanvasWebDavConfig>
    if (parsed && typeof parsed.url === 'string') webdavConfig.value = { ...webdavConfig.value, ...parsed }
  } catch {
    // Keep the empty defaults when a browser has no valid sync settings.
  }
}

async function handleWebdav(action: 'upload' | 'download', config: CanvasWebDavConfig): Promise<void> {
  webdavBusy.value = true
  webdavConfig.value = config
  try {
    if (typeof localStorage !== 'undefined') localStorage.setItem(webdavStorageKey(), JSON.stringify(config))
    if (action === 'upload') {
      await uploadCanvasState(config, canvasStore.state)
      appStore.showSuccess(t('playground.canvasWebdavBackupSuccess'))
    } else {
      if (!window.confirm(t('playground.canvasWebdavRestoreConfirm'))) return
      const remote = await downloadCanvasState(config)
      for (const project of remote.projects) canvasStore.importProject(JSON.stringify(project))
      appStore.showSuccess(t('playground.canvasWebdavRestoreSuccess', { count: remote.projects.length }))
    }
    webdavOpen.value = false
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasWebdavFailed'))
  } finally {
    webdavBusy.value = false
  }
}

function importCanvas(): void {
  importInput.value?.click()
}

function remapImportedNode(node: CanvasNodeData, keyMap: Map<string, string>): CanvasNodeData {
  const remap = (key: string | undefined): string | undefined => key ? (keyMap.get(key) ?? key) : undefined
  const originalImageKeys = node.imageCacheKeys?.length ? node.imageCacheKeys : node.imageCacheKey ? [node.imageCacheKey] : []
  const pluginMetadata = node.pluginMetadata ? { ...node.pluginMetadata } : undefined
  if (pluginMetadata && typeof pluginMetadata.contentCacheKey === 'string') {
    pluginMetadata.contentCacheKey = remap(pluginMetadata.contentCacheKey)
    if (pluginMetadata.contentCacheKey !== node.pluginMetadata?.contentCacheKey) delete pluginMetadata.content
  }
  const imageCacheKeys = originalImageKeys.map((key) => remap(key)).filter((key): key is string => Boolean(key))
  const imageVariants = node.imageVariants?.map((variant) => ({ ...variant, cacheKey: remap(variant.cacheKey) }))
  const imported: CanvasNodeData = {
    ...node,
    imageCacheKey: remap(node.imageCacheKey),
    imageCacheKeys: imageCacheKeys.length ? imageCacheKeys : undefined,
    imageVariants,
    videoCacheKey: remap(node.videoCacheKey),
    audioCacheKey: remap(node.audioCacheKey),
    pluginMetadata,
    referenceImages: node.referenceImages?.map((reference) => ({ ...reference, cacheKey: remap(reference.cacheKey) ?? reference.cacheKey })),
  }
  if (originalImageKeys.some((key) => keyMap.has(key))) {
    imported.imageUrl = undefined
    imported.imageUrls = undefined
    imported.imageVariants = imported.imageVariants?.map((variant) => ({ ...variant, url: variant.cacheKey ? undefined : variant.url }))
  }
  if (keyMap.has(node.videoCacheKey ?? '')) imported.videoUrl = undefined
  if (keyMap.has(node.audioCacheKey ?? '')) imported.audioUrl = undefined
  return imported
}

async function importCanvasArchive(file: File): Promise<void> {
  const files = await readCanvasArchive(file)
  const manifestBlob = files.get('projects.json')
  if (!manifestBlob) throw new Error('Missing projects.json')
  const manifest = JSON.parse(await manifestBlob.text()) as { state?: CanvasPersistedState; assets?: Array<{ key: string; path: string; kind: 'image' | 'media'; mimeType?: string }> }
  if (!manifest.state?.projects?.length) throw new Error('Canvas archive has no projects')
  const keyMap = new Map<string, string>()
  const owner = userId.value ?? 0
  const keyId = selectedKeyId.value ?? 0
  const stamp = Date.now()
  for (const [index, asset] of (manifest.assets ?? []).entries()) {
    const blob = files.get(asset.path)
    if (!blob) continue
    const nextKey = `${asset.kind === 'image' ? createPlaygroundImageCacheKey(owner, keyId, `import-${stamp}-${index}`, 0) : createCanvasMediaCacheKey(owner, keyId, `import-${stamp}-${index}`)}`
    if (asset.kind === 'image') {
      const dataUrl = await readFileAsDataUrl(new File([blob], asset.path, { type: asset.mimeType || blob.type || 'image/png' }))
      await cachePlaygroundImage(nextKey, owner, keyId, dataUrl, { model: 'canvas-import' })
    } else {
      await cacheCanvasMedia(nextKey, new Blob([blob], { type: asset.mimeType || blob.type || 'application/octet-stream' }))
    }
    keyMap.set(asset.key, nextKey)
  }
  for (const source of manifest.state.projects) {
    const project = { ...source, nodes: source.nodes.map((node) => remapImportedNode(node, keyMap)) }
    canvasStore.importProject(JSON.stringify(project))
  }
  await restoreImages()
  await restoreVideos()
  await restoreAudio()
  for (const project of canvasStore.state.projects) {
    for (const node of project.nodes) await loadReferencePreviews(node)
  }
}

async function handleImportFile(): Promise<void> {
  const file = importInput.value?.files?.[0]
  if (!file) return
  try {
    if (file.type === 'application/zip' || file.name.toLowerCase().endsWith('.zip')) await importCanvasArchive(file)
    else canvasStore.importProject(await file.text())
  } catch {
    appStore.showError(t('playground.canvasImportFailed'))
  } finally {
    if (importInput.value) importInput.value.value = ''
  }
}

async function handleReferenceFiles(): Promise<void> {
  const node = selectedNode.value
  const keyId = selectedKeyId.value
  const owner = userId.value
  const files = Array.from(referenceInput.value?.files ?? []).filter((file) => file.type.startsWith('image/')).slice(0, MAX_REFERENCE_IMAGES)
  if (!node || !files.length || !owner || !keyId) return

  const added: CanvasReferenceImage[] = []
  const batch = Date.now()
  for (const [index, file] of files.entries()) {
    const dataUrl = await readFileAsDataUrl(file)
    // 图片本体写进 IndexedDB，节点只留 cacheKey——base64 绝不进 localStorage。
    const cacheKey = createPlaygroundImageCacheKey(owner, keyId, `${node.id}-ref-${batch}`, index)
    const previewUrl = await cachePlaygroundImage(cacheKey, owner, keyId, dataUrl, {
      prompt: node.prompt,
      model: 'canvas-reference',
    })
    if (!previewUrl) continue
    referencePreviews.value = { ...referencePreviews.value, [cacheKey]: previewUrl }
    added.push({ id: cacheKey, name: file.name, mimeType: file.type, size: file.size, cacheKey })
  }

  if (added.length) {
    canvasStore.updateNode(node.id, {
      referenceImages: [...(node.referenceImages ?? []), ...added].slice(0, MAX_REFERENCE_IMAGES),
    })
  }
  if (referenceInput.value) referenceInput.value.value = ''
}

function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read reference image.'))
    reader.readAsDataURL(file)
  })
}

// 缓存里取回的是 blob object URL，而上游接口要求 data URL（api.ts 会校验 data:image/ 前缀），
// 所以发请求前需要转回来。
async function objectUrlToDataUrl(objectUrl: string): Promise<string | null> {
  try {
    const blob = await (await fetch(objectUrl)).blob()
    return await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error ?? new Error('Unable to read cached reference image.'))
      reader.readAsDataURL(blob)
    })
  } catch {
    return null
  }
}

function collectCanvasUpstreamNodes(project: CanvasProject, targetId: string): CanvasNodeData[] {
  const visited = new Set<string>()
  const queue = project.connections
    .filter((connection) => connection.to === targetId)
    .map((connection) => connection.from)
  const nodes: CanvasNodeData[] = []
  while (queue.length) {
    const id = queue.shift()
    if (!id || visited.has(id)) continue
    visited.add(id)
    const node = project.nodes.find((candidate) => candidate.id === id)
    if (!node) continue
    nodes.push(node)
    for (const connection of project.connections) {
      if (connection.to === id && !visited.has(connection.from)) queue.push(connection.from)
    }
  }
  return nodes
}

function composeCanvasPrompt(node: CanvasNodeData, project: CanvasProject): string {
  const upstreamText = collectCanvasUpstreamNodes(project, node.id)
    .flatMap((candidate) => candidate.type === 'group' ? expandCanvasGroupResourceNodes(candidate, project) : [candidate])
    .filter((candidate) => candidate.type === 'text')
    .map((candidate) => (candidate.textContent || candidate.prompt).trim())
    .filter(Boolean)
  const blocks = [...upstreamText, node.prompt.trim()].filter(Boolean)
  const uniqueBlocks = blocks.filter((block, index) => blocks.indexOf(block) === index)
  return resolveCanvasNodeMentions(uniqueBlocks.join('\n\n'), project).trim()
}

// 生图前把参考图从 IndexedDB 还原成接口要的 PlaygroundAttachment 形态。
async function resolveReferenceAttachments(node: CanvasNodeData): Promise<PlaygroundAttachment[]> {
  const attachments: PlaygroundAttachment[] = []
  for (const reference of node.referenceImages ?? []) {
    const objectUrl = referencePreviews.value[reference.cacheKey] ?? await restoreCachedPlaygroundImage(reference.cacheKey)
    if (!objectUrl) continue
    const dataUrl = await objectUrlToDataUrl(objectUrl)
    if (!dataUrl) continue
    attachments.push({
      id: reference.id,
      name: reference.name,
      mimeType: reference.mimeType,
      size: reference.size,
      dataUrl,
    })
  }
  const project = activeProject.value
  if (project) {
    const mentionAttachments = await resolveMentionAttachments(node.prompt, project)
    for (const attachment of mentionAttachments) {
      if (!attachments.some((item) => item.dataUrl === attachment.dataUrl)) attachments.push(attachment)
    }
  }
  for (const upstream of project ? collectCanvasUpstreamNodes(project, node.id) : []) {
    if (upstream?.type === 'image') {
      const blob = await resolveImageBlob(upstream, 0)
      if (blob) {
        const dataUrl = await blobToDataUrl(blob)
        if (dataUrl && !attachments.some((item) => item.dataUrl === dataUrl)) {
          attachments.push({
            id: upstream.id,
            name: `${canvasNodeLabel(upstream)}.png`,
            mimeType: blob.type || 'image/png',
            size: blob.size,
            dataUrl,
          })
        }
      }
      continue
    }
    if (upstream?.type === 'group' && project) {
      const urls = expandCanvasGroupResourceNodes(upstream, project)
        .filter((child) => child.type === 'image' || child.pluginRenderer === 'svg' || child.pluginRenderer === 'panorama')
        .map((child) => getCanvasNodePreviewUrl(child))
        .filter((url): url is string => Boolean(url))
      const extra = await pluginReferenceAttachments(urls)
      for (const attachment of extra) {
        if (!attachments.some((item) => item.dataUrl === attachment.dataUrl)) attachments.push(attachment)
      }
      continue
    }
    const definition = upstream ? getCanvasPluginDefinition(upstream.type) : undefined
    let resource: { kind: string; text?: string; url?: string } | null = null
    try {
      resource = upstream && definition?.resource?.(upstream) || null
    } catch (error) {
      console.warn('[canvas-plugin] resource resolver failed', error)
    }
    if (!resource || resource.kind !== 'image' || !resource.url || attachments.some((item) => item.dataUrl === resource.url)) continue
    const extra = await pluginReferenceAttachments([resource.url])
    attachments.push(...extra)
  }
  return attachments
}

async function resolveMentionAttachments(value: string, project: CanvasProject): Promise<PlaygroundAttachment[]> {
  const ids = Array.from(value.matchAll(/@\[node:([^\]]+)\]/g), (match) => match[1])
  const attachments: PlaygroundAttachment[] = []
  for (const [index, id] of ids.entries()) {
    const node = project.nodes.find((candidate) => candidate.id === id)
    if (!node || attachments.some((item) => item.id === id)) continue
    let dataUrl = ''
    if (node.type === 'group') { const child = expandCanvasGroupResourceNodes(node, project).find((item) => item.type === 'image'); if (child) { const blob = await resolveImageBlob(child, 0); if (blob) dataUrl = await blobToDataUrl(blob) } } else if (node.type === 'image') {
      const blob = await resolveImageBlob(node, 0)
      if (blob) dataUrl = await blobToDataUrl(blob)
    } else {
      const content = typeof node.pluginMetadata?.content === 'string' ? node.pluginMetadata.content.trim() : ''
      if (/^data:image\//i.test(content)) dataUrl = content
      else if (/^https?:\/\//i.test(content)) {
        const extra = await pluginReferenceAttachments([content])
        dataUrl = extra[0]?.dataUrl ?? ''
      }
    }
    if (!dataUrl) continue
    attachments.push({ id, name: `canvas-reference-${index + 1}`, mimeType: 'image/*', size: dataUrl.length, dataUrl })
  }
  return attachments
}

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read canvas reference image.'))
    reader.readAsDataURL(blob)
  })
}

async function loadReferencePreviews(node: CanvasNodeData | null): Promise<void> {
  for (const reference of node?.referenceImages ?? []) {
    if (referencePreviews.value[reference.cacheKey]) continue
    const url = await restoreCachedPlaygroundImage(reference.cacheKey)
    if (url) referencePreviews.value = { ...referencePreviews.value, [reference.cacheKey]: url }
  }
}

function removeReference(index: number): void {
  const node = selectedNode.value
  if (!node) return
  const target = node.referenceImages?.[index]
  canvasStore.updateNode(node.id, {
    referenceImages: node.referenceImages?.filter((_, itemIndex) => itemIndex !== index),
  })
  if (!target) return
  const { [target.cacheKey]: _removed, ...rest } = referencePreviews.value
  referencePreviews.value = rest
  void removeCachedPlaygroundImages([target.cacheKey])
}

function updateGenerationSet(target: Ref<Set<string>>, id: string, active: boolean): void {
  const next = new Set(target.value)
  if (active) next.add(id)
  else next.delete(id)
  target.value = next
}

function generateNode(id: string): void {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  const keyId = selectedKeyId.value
  const model = node?.model || selectedModel.value
  if (
    !node
    || node.kind === 'result'
    || node.type !== 'image'
    || !keyId
    || !model
    || activeGenerationIds.value.has(id)
    || !canGenerate.value
  ) return

  const upstreamNodes = collectCanvasUpstreamNodes(activeProject.value, id)
  const expandedUpstreamNodes = upstreamNodes.flatMap((candidate) => candidate.type === 'group' ? expandCanvasGroupResourceNodes(candidate, activeProject.value) : [candidate])
  const upstreamText = expandedUpstreamNodes.filter((candidate) => candidate.type === 'text').map((candidate) => candidate.textContent || candidate.prompt).filter(Boolean)
  const upstreamConfig = expandedUpstreamNodes.find((candidate) => candidate.type === 'config')?.config
  const snapshot = {
    ...node,
    prompt: resolveCanvasNodeMentions([...upstreamText, node.prompt].filter(Boolean).join('\n\n'), activeProject.value),
    config: upstreamConfig ?? node.config,
    referenceImages: node.referenceImages?.map((reference) => ({ ...reference })),
    model,
    imageCount: Math.min(10, Math.max(1, Math.floor(node.imageCount ?? 4))),
  } as CanvasNodeData
  canvasStore.checkpoint()
  const generationToken = `${id}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  generationTokens.set(id, generationToken)
  canvasStore.updateNode(id, { status: 'generating', errorMessage: undefined, model, imageCount: snapshot.imageCount })
  canvasStore.removeDerivedNodes(id)
  updateGenerationSet(activeGenerationIds, id, true)
  updateGenerationSet(queuedGenerationIds, id, true)
  generationQueue.push(async () => {
    updateGenerationSet(queuedGenerationIds, id, false)
    if (generationTokens.get(id) !== generationToken) return
    await runGeneration(snapshot, keyId, generationToken)
  })
  void drainGenerationQueue()
}

async function drainGenerationQueue(): Promise<void> {
  while (runningGenerations.value < MAX_CONCURRENT_GENERATIONS && generationQueue.length) {
    const task = generationQueue.shift()
    if (!task) return
    runningGenerations.value += 1
    void task().catch(() => undefined).finally(() => {
      runningGenerations.value -= 1
      void drainGenerationQueue()
    })
  }
}

async function runGeneration(node: CanvasNodeData, keyId: number, generationToken: string): Promise<void> {
  try {
    if (generationTokens.get(node.id) !== generationToken || !activeProject.value?.nodes.some((item) => item.id === node.id)) return
    const plannedImageCount = Math.min(10, Math.max(1, Math.floor(node.imageCount ?? 4)))
    const plannedVariants: CanvasImageVariant[] = Array.from({ length: plannedImageCount }, (_, index) => ({
      id: `${node.id}-image-${index}`,
      status: 'pending',
    }))
    commitImageVariants(node.id, plannedVariants.map((variant) => ({ ...variant })), 'generating', undefined, node.primaryImageIndex)
    const references = await resolveReferenceAttachments(node)
    const request = buildPlaygroundImageRequest({
      model: node.model,
      prompt: node.prompt,
      size: node.config?.size ?? '1024x1024',
      quality: node.config?.quality ?? 'auto',
      imageCount: node.imageCount ?? 4,
      outputFormat: 'png',
      outputCompression: 90,
      background: node.config?.background ?? 'auto',
      moderation: 'auto',
      style: 'auto',
      inputFidelity: 'auto',
      hasReferenceImages: references.length > 0,
    })
    const requestedImageCount = request.requestedImageCount
    const imageVariants: CanvasImageVariant[] = requestedImageCount === plannedImageCount
      ? plannedVariants
      : Array.from({ length: requestedImageCount }, (_, index) => ({ id: `${node.id}-image-${index}`, status: 'pending' as const }))
    if (requestedImageCount !== plannedImageCount) commitImageVariants(node.id, imageVariants.map((variant) => ({ ...variant })), 'generating', undefined, node.primaryImageIndex)
    let nextSlot = 0
    const failures: string[] = []
    for (let requestIndex = 0; requestIndex < requestedImageCount && nextSlot < requestedImageCount; requestIndex += 1) {
      try {
        const response = await sendPlaygroundImageGeneration(keyId, request.body, { referenceImages: references })
        if (generationTokens.get(node.id) !== generationToken || !activeProject.value?.nodes.some((item) => item.id === node.id)) return
        const slotStart = nextSlot
        for (const result of response.data) {
          if (nextSlot >= requestedImageCount) break
          const sourceUrl = playgroundImageUrl(result, 'png')
          if (!sourceUrl) continue
          const resultIndex = nextSlot
          let imageUrl = sourceUrl
          let cacheKey: string | undefined
          if (userId.value) {
            cacheKey = createPlaygroundImageCacheKey(userId.value, keyId, node.id, resultIndex)
            const cachedUrl = await cachePlaygroundImage(cacheKey, userId.value, keyId, sourceUrl, {
              prompt: node.prompt,
              model: node.model,
              size: node.config?.size ?? '1024x1024',
              quality: node.config?.quality ?? 'auto',
              outputFormat: 'png',
              sourceImageCount: references.length,
            })
            if (cachedUrl) {
              imageUrl = cachedUrl
              imageObjectUrls.add(cachedUrl)
            }
          }
          imageVariants[nextSlot] = {
            id: imageVariants[nextSlot]?.id || `${node.id}-image-${nextSlot}`,
            status: 'success',
            url: imageUrl,
            cacheKey,
          }
          nextSlot += 1
          const latest = activeProject.value?.nodes.find((item) => item.id === node.id)
          commitImageVariants(node.id, imageVariants.map((variant) => ({ ...variant })), 'generating', failures.length ? failures.join('; ') : undefined, latest?.primaryImageIndex)
        }
        if (slotStart === nextSlot && nextSlot < requestedImageCount) {
          const message = t('playground.imageGenerationNoResults')
          failures.push(message)
          imageVariants[nextSlot] = { id: imageVariants[nextSlot]?.id || `${node.id}-image-${nextSlot}`, status: 'error', errorMessage: message }
          nextSlot += 1
          const latest = activeProject.value?.nodes.find((item) => item.id === node.id)
          commitImageVariants(node.id, imageVariants.map((variant) => ({ ...variant })), 'generating', failures.join('; '), latest?.primaryImageIndex)
        }
      } catch (error) {
        const message = error instanceof Error ? error.message : t('playground.sendFailed')
        failures.push(message)
        if (nextSlot < requestedImageCount) {
          imageVariants[nextSlot] = { id: imageVariants[nextSlot]?.id || `${node.id}-image-${nextSlot}`, status: 'error', errorMessage: message }
          nextSlot += 1
          const latest = activeProject.value?.nodes.find((item) => item.id === node.id)
          commitImageVariants(node.id, imageVariants.map((variant) => ({ ...variant })), 'generating', failures.join('; '), latest?.primaryImageIndex)
        }
      }
    }
    const successfulVariants = imageVariants.filter((variant): variant is CanvasImageVariant & { status: 'success'; url: string } => variant.status === 'success' && Boolean(variant.url))
    if (!successfulVariants.length) throw new Error(failures[0] || t('playground.imageGenerationNoResults'))
    const latest = activeProject.value?.nodes.find((item) => item.id === node.id)
    commitImageVariants(node.id, imageVariants.map((variant) => ({ ...variant })), 'success', failures.length ? failures.join('; ') : undefined, latest?.primaryImageIndex)
  } catch (error) {
    if (generationTokens.get(node.id) !== generationToken || !activeProject.value?.nodes.some((item) => item.id === node.id)) return
    canvasStore.updateNode(node.id, {
      status: 'error',
      errorMessage: error instanceof Error ? error.message : t('playground.sendFailed'),
    })
  } finally {
    if (generationTokens.get(node.id) === generationToken) {
      generationTokens.delete(node.id)
      updateGenerationSet(activeGenerationIds, node.id, false)
    }
  }
}

async function runImageVariantRetry(node: CanvasNodeData, keyId: number, imageIndex: number): Promise<void> {
  const generationToken = `${node.id}-retry-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  generationTokens.set(node.id, generationToken)
  updateGenerationSet(activeGenerationIds, node.id, true)
  const slots = canvasImageSlots(node)
  slots[imageIndex] = { ...slots[imageIndex], status: 'pending', errorMessage: undefined }
  canvasStore.checkpoint()
  canvasStore.updateNode(node.id, { ...syncImageVariantFields(slots, node.primaryImageIndex ?? imageIndex), status: 'generating', errorMessage: undefined })
  try {
    const references = await resolveReferenceAttachments(node)
    const request = buildPlaygroundImageRequest({
      model: node.model,
      prompt: node.prompt,
      size: node.config?.size ?? '1024x1024',
      quality: node.config?.quality ?? 'auto',
      imageCount: 1,
      outputFormat: 'png',
      outputCompression: 90,
      background: node.config?.background ?? 'auto',
      moderation: 'auto',
      style: 'auto',
      inputFidelity: 'auto',
      hasReferenceImages: references.length > 0,
    })
    const response = await sendPlaygroundImageGeneration(keyId, request.body, { referenceImages: references })
    if (generationTokens.get(node.id) !== generationToken || !activeProject.value?.nodes.some((item) => item.id === node.id)) return
    const result = response.data.map((item) => playgroundImageUrl(item, 'png')).find(Boolean)
    if (!result) throw new Error(t('playground.imageGenerationNoResults'))
    let imageUrl = result
    let cacheKey: string | undefined
    if (userId.value) {
      cacheKey = createPlaygroundImageCacheKey(userId.value, keyId, node.id, imageIndex)
      const cachedUrl = await cachePlaygroundImage(cacheKey, userId.value, keyId, result, {
        prompt: node.prompt,
        model: node.model,
        size: node.config?.size ?? '1024x1024',
        quality: node.config?.quality ?? 'auto',
        outputFormat: 'png',
        sourceImageCount: references.length,
      })
      if (cachedUrl) {
        imageUrl = cachedUrl
        imageObjectUrls.add(cachedUrl)
      }
    }
    const latest = activeProject.value?.nodes.find((item) => item.id === node.id)
    if (!latest) return
    const latestSlots = canvasImageSlots(latest)
    latestSlots[imageIndex] = { id: latestSlots[imageIndex]?.id || `${node.id}-image-${imageIndex}`, status: 'success', url: imageUrl, cacheKey }
    const fields = syncImageVariantFields(latestSlots, latest.primaryImageIndex ?? imageIndex)
    canvasStore.updateNode(node.id, { ...fields, status: 'success', errorMessage: undefined })
  } catch (error) {
    if (generationTokens.get(node.id) !== generationToken || !activeProject.value?.nodes.some((item) => item.id === node.id)) return
    const latest = activeProject.value.nodes.find((item) => item.id === node.id)
    if (!latest) return
    const latestSlots = canvasImageSlots(latest)
    latestSlots[imageIndex] = { id: latestSlots[imageIndex]?.id || `${node.id}-image-${imageIndex}`, status: 'error', errorMessage: error instanceof Error ? error.message : t('playground.sendFailed') }
    const hasSuccess = latestSlots.some((slot) => slot.status === 'success' && slot.url)
    canvasStore.updateNode(node.id, { ...syncImageVariantFields(latestSlots, latest.primaryImageIndex ?? imageIndex), status: hasSuccess ? 'success' : 'error', errorMessage: latestSlots[imageIndex]?.errorMessage })
  } finally {
    if (generationTokens.get(node.id) === generationToken) {
      generationTokens.delete(node.id)
      updateGenerationSet(activeGenerationIds, node.id, false)
    }
  }
}

async function captureVideoFrame(id: string, position: CanvasVideoFramePosition, currentTime = 0): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if (!node || node.type !== 'video' || !node.videoUrl) return

  const label = position === 'first'
    ? t('playground.canvasCaptureFirstFrame')
    : position === 'last'
      ? t('playground.canvasCaptureLastFrame')
      : t('playground.canvasCaptureCurrentFrame')

  try {
    const frame = await captureCanvasVideoFrame(node.videoUrl, position, currentTime)
    const now = Date.now()
    const resultId = `video-frame-${node.id}-${position}-${now}-${Math.random().toString(36).slice(2, 8)}`
    const owner = userId.value
    const keyId = selectedKeyId.value
    const cacheKey = owner && keyId ? createPlaygroundImageCacheKey(owner, keyId, resultId, 0) : ''
    const dataUrl = await readFileAsDataUrl(new File([frame], `${resultId}.png`, { type: 'image/png' }))
    const imageUrl = cacheKey && owner && keyId
      ? await cachePlaygroundImage(cacheKey, owner, keyId, dataUrl, { prompt: `${node.prompt} · ${label}`, model: 'video-frame' })
      : URL.createObjectURL(frame)
    if (!imageUrl) throw new Error(t('playground.canvasVideoFrameFailed'))
    if (imageUrl.startsWith('blob:')) imageObjectUrls.add(imageUrl)

    const aspectRatio = frame.size > 0 ? node.width / Math.max(node.height, 1) : 16 / 9
    const width = Math.max(260, Math.min(640, node.width))
    const height = Math.max(180, Math.min(480, width / Math.max(aspectRatio, 0.25)))
    const resultNode: CanvasNodeData = {
      id: resultId,
      type: 'image',
      kind: 'result',
      x: node.x + node.width + 120,
      y: node.y + (position === 'first' ? 0 : position === 'current' ? height + 40 : (height + 40) * 2),
      width,
      height,
      prompt: `${node.prompt} · ${label}`,
      model: 'video-frame',
      imageUrl,
      imageUrls: [imageUrl],
      imageCacheKey: cacheKey || undefined,
      imageCacheKeys: cacheKey ? [cacheKey] : undefined,
      outputFormat: 'png',
      status: 'success',
      sourceNodeId: node.id,
      resultIndex: 0,
      createdAt: now,
      updatedAt: now,
    }
    canvasStore.checkpoint()
    canvasStore.addNode(resultNode)
    canvasStore.addConnection({ id: `connection-${node.id}-${resultNode.id}`, from: node.id, to: resultNode.id, kind: 'result' })
    selectedNodeId.value = resultNode.id
    selectedNodeIds.value = new Set([resultNode.id])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.canvasVideoFrameFailed'))
  }
}

async function downloadNode(id: string, imageIndex?: number): Promise<void> {
  const node = activeProject.value?.nodes.find((item) => item.id === id)
  if ((!node?.imageUrl && !node?.videoUrl && !node?.audioUrl) || downloadingNodeId.value) return
  const normalizedImageIndex = Number.isFinite(imageIndex) ? Math.max(0, Math.floor(imageIndex as number)) : 0
  const imageSlot = node?.type === 'image' ? canvasImageSlots(node)[normalizedImageIndex] : undefined
  const imageUrl = imageSlot?.status === 'success' ? imageSlot.url : node.imageUrl
  const videoUrl = node.videoUrl
  const audioUrl = node.audioUrl
  downloadingNodeId.value = id
  try {
    if (videoUrl) {
      const response = await fetch(videoUrl)
      if (!response.ok) throw new Error(t('playground.imageDownloadFailed'))
      const url = URL.createObjectURL(await response.blob())
      const link = document.createElement('a')
      link.href = url
      link.download = `sub2api-canvas-${id}.mp4`
      link.click()
      URL.revokeObjectURL(url)
      return
    }
    if (audioUrl) {
      const response = await fetch(audioUrl)
      if (!response.ok) throw new Error(t('playground.imageDownloadFailed'))
      const url = URL.createObjectURL(await response.blob())
      const link = document.createElement('a')
      link.href = url
      link.download = `sub2api-canvas-${id}.mp3`
      link.click()
      URL.revokeObjectURL(url)
      return
    }
    if (!imageUrl) return
    const cacheKey = imageSlot?.cacheKey ?? node.imageCacheKeys?.[normalizedImageIndex] ?? node.imageCacheKey
    const blob = cacheKey ? await readCachedPlaygroundImageBlob(cacheKey) : null
    await downloadPlaygroundImage(blob ?? imageUrl, `sub2api-canvas-${id}-${node.resultIndex ?? normalizedImageIndex}.${imageFilenameExtension(imageUrl, node.outputFormat || 'png')}`)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.imageDownloadFailed'))
  } finally {
    downloadingNodeId.value = null
  }
}

async function useResultAsReference(id: string, imageIndex?: number): Promise<void> {
  const result = activeProject.value?.nodes.find((node) => node.id === id)
  const selected = selectedNode.value
  const target = selected?.kind === 'result'
    ? activeProject.value?.nodes.find((node) => node.id === lastReferenceTargetId.value && node.kind !== 'result')
    : selected
  const normalizedImageIndex = Number.isFinite(imageIndex) ? Math.max(0, Math.floor(imageIndex as number)) : 0
  const resultSlot = result?.type === 'image' ? canvasImageSlots(result)[normalizedImageIndex] : undefined
  const cacheKey = resultSlot?.cacheKey ?? result?.imageCacheKeys?.[normalizedImageIndex] ?? result?.imageCacheKey
  if (!result || !cacheKey || result.type !== 'image') return
  if (!target || target.id === id) {
    appStore.showError(t('playground.canvasReferenceTargetRequired'))
    return
  }
  if ((target.referenceImages?.length ?? 0) >= MAX_REFERENCE_IMAGES) return
  if (target.referenceImages?.some((reference) => reference.cacheKey === cacheKey)) return
  canvasStore.updateNode(target.id, {
    referenceImages: [
      ...(target.referenceImages ?? []),
      {
      id: cacheKey,
      name: `${t('playground.canvasResult', { index: (result.resultIndex ?? normalizedImageIndex) + 1 })}.png`,
      mimeType: 'image/png',
      size: 0,
      cacheKey,
      },
    ].slice(0, MAX_REFERENCE_IMAGES),
  })
  await loadReferencePreviews(target)
}

async function loadImageModels(keyId: number): Promise<void> {
  loadingModels.value = true
  try {
    const fetched = await fetchPlaygroundModels(keyId)
    const available = normalizePlaygroundImageModels(fetched)
    models.value = available
    videoModels.value = normalizePlaygroundVideoModels(fetched)
    const rememberedModel = loadRememberedCanvasModel('image', keyId)
    selectedModel.value = resolveSelectedModel(available, rememberedModel || selectedModel.value || resolvePlaygroundDefaultModel(available, 'image', appStore.cachedPublicSettings?.playground_default_image_model))
    rememberSelectedCanvasModel('image', keyId, selectedModel.value)
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.modelsLoadFailed'))
  } finally {
    loadingModels.value = false
  }
}

async function loadTextModels(keyId: number): Promise<void> {
  loadingTextModels.value = true
  try {
    const fetched = await fetchPlaygroundModels(keyId)
    textModels.value = normalizePlaygroundChatModels(fetched)
    const rememberedModel = loadRememberedCanvasModel('chat', keyId)
    selectedTextModel.value = resolveSelectedModel(textModels.value, rememberedModel || selectedTextModel.value || resolvePlaygroundDefaultModel(textModels.value, 'chat', appStore.cachedPublicSettings?.playground_default_chat_model))
    rememberSelectedCanvasModel('chat', keyId, selectedTextModel.value)
    if (!assistantModel.value || !textModels.value.some((model) => model.id === assistantModel.value)) assistantModel.value = selectedTextModel.value
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.modelsLoadFailed'))
  } finally {
    loadingTextModels.value = false
  }
}

function refreshModels(): void {
  if (selectedKeyId.value) void loadImageModels(selectedKeyId.value)
  if (selectedTextKeyId.value) void loadTextModels(selectedTextKeyId.value)
}

async function loadKeys(): Promise<void> {
  try {
    await keysAPI.ensurePlayground().catch(() => undefined)
    const allKeys: ApiKey[] = []
    let page = 1
    let pages = 1
    do {
      const response = await keysAPI.list(page, 100, undefined, { includeHidden: true })
      allKeys.push(...response.items)
      pages = response.pages
      page += 1
    } while (page <= pages)
    playgroundKeys.value = normalizePlaygroundKeys(allKeys)
    suppressCanvasModelWatchers = true
    const rememberedImageKeyId = loadRememberedCanvasKeyId('image')
    const rememberedChatKeyId = loadRememberedCanvasKeyId('chat')
    selectedKeyId.value = resolveSelectedKeyId(playgroundKeys.value, selectedKeyId.value ?? rememberedImageKeyId ?? resolvePlaygroundDefaultKeyId(playgroundKeys.value, 'image'))
    selectedTextKeyId.value = resolveSelectedKeyId(playgroundKeys.value, selectedTextKeyId.value ?? rememberedChatKeyId ?? resolvePlaygroundDefaultKeyId(playgroundKeys.value, 'chat') ?? selectedKeyId.value)
    await Promise.all([
      selectedKeyId.value ? loadImageModels(selectedKeyId.value) : Promise.resolve(),
      selectedTextKeyId.value ? loadTextModels(selectedTextKeyId.value) : Promise.resolve(),
    ])
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('playground.keysLoadFailed'))
  } finally {
    suppressCanvasModelWatchers = false
  }
}

function nodeStatusLabel(status: CanvasNodeData['status']): string {
  return t(`playground.canvasStatus.${status}`)
}

async function restoreImages(): Promise<void> {
  for (const project of canvasStore.state.projects) {
    for (const node of project.nodes) {
      const contentCacheKey = typeof node.pluginMetadata?.contentCacheKey === 'string' ? node.pluginMetadata.contentCacheKey : ''
      if (contentCacheKey) {
        const restoredContent = await restoreCachedPlaygroundImage(contentCacheKey).catch(() => null)
        if (restoredContent) {
          node.pluginMetadata = { ...(node.pluginMetadata ?? {}), content: restoredContent }
          imageObjectUrls.add(restoredContent)
        }
      }
      if (node.imageVariants?.length) {
        const restoredVariants = await Promise.all(node.imageVariants.map(async (variant) => {
          if (variant.cacheKey) {
            const restoredUrl = await restoreCachedPlaygroundImage(variant.cacheKey).catch(() => null)
            if (restoredUrl) {
              imageObjectUrls.add(restoredUrl)
              return { ...variant, status: 'success' as const, url: restoredUrl, errorMessage: undefined }
            }
          }
          if (variant.status === 'success' && variant.url && !variant.url.startsWith('blob:')) return variant
          return variant.status === 'success'
            ? { ...variant, status: 'error' as const, url: undefined, errorMessage: t('playground.canvasImageRestoreFailed') }
            : variant
        }))
        Object.assign(node, syncImageVariantFields(restoredVariants, node.primaryImageIndex))
        continue
      }
      const cacheKeys = node.imageCacheKeys?.length ? node.imageCacheKeys : node.imageCacheKey ? [node.imageCacheKey] : []
      if (!cacheKeys.length) continue
      const restored = (await Promise.all(cacheKeys.map((key) => restoreCachedPlaygroundImage(key).catch(() => null)))).filter((url): url is string => Boolean(url))
      if (!restored.length) continue
      node.imageUrl = restored[0]
      node.imageUrls = restored
      restored.forEach((url) => imageObjectUrls.add(url))
    }
  }
}

async function restoreVideos(): Promise<void> {
  for (const project of canvasStore.state.projects) {
    for (const node of project.nodes) {
      if (node.type !== 'video' || !node.videoCacheKey || node.videoUrl) continue
      const url = await restoreCanvasMedia(node.videoCacheKey).catch(() => null)
      if (url) {
        node.videoUrl = url
        imageObjectUrls.add(url)
      }
    }
  }
}

async function restoreAudio(): Promise<void> {
  for (const project of canvasStore.state.projects) {
    for (const node of project.nodes) {
      if (node.type !== 'audio' || !node.audioCacheKey || node.audioUrl) continue
      const url = await restoreCanvasMedia(node.audioCacheKey).catch(() => null)
      if (url) {
        node.audioUrl = url
        imageObjectUrls.add(url)
      }
    }
  }
}

function refreshCanvasPlugins(): void {
  plugins.value = loadCanvasPlugins()
}

async function loadCanvasPluginRuntimes(): Promise<void> {
  for (const plugin of plugins.value.filter((item) => item.enabled && item.source)) {
    try {
      await loadCanvasPluginSource(plugin.source as string)
    } catch (error) {
      console.warn(`[canvas-plugin] failed to load ${plugin.id}`, error)
    }
  }
}

async function installCanvasPlugin(url: string): Promise<void> {
  pluginBusy.value = true
  pluginError.value = ''
  try {
    await installCanvasPluginFromUrl(url)
    refreshCanvasPlugins()
    await loadCanvasPluginRuntimes()
    appStore.showSuccess(t('playground.canvasPluginInstalled'))
  } catch (error) {
    pluginError.value = error instanceof Error ? error.message : t('playground.canvasPluginInstallFailed')
  } finally {
    pluginBusy.value = false
  }
}

async function toggleCanvasPlugin(value: { id: string; enabled: boolean }): Promise<void> {
  plugins.value = setCanvasPluginEnabled(value.id, value.enabled)
  if (!value.enabled) {
    unregisterCanvasPlugin(value.id)
    return
  }
  const plugin = plugins.value.find((item) => item.id === value.id)
  if (plugin?.source) {
    try { await loadCanvasPluginSource(plugin.source) } catch (error) { pluginError.value = error instanceof Error ? error.message : t('playground.canvasPluginInstallFailed') }
  }
}

function uninstallCanvasPluginEntry(id: string): void {
  unregisterCanvasPlugin(id)
  plugins.value = removeCanvasPlugin(id)
}

watch(selectedKeyId, (keyId) => {
  if (!suppressCanvasModelWatchers && keyId) {
    rememberSelectedCanvasKeyId('image', keyId)
    void loadImageModels(keyId)
  }
})
watch(selectedTextKeyId, (keyId) => {
  if (!suppressCanvasModelWatchers && keyId) {
    rememberSelectedCanvasKeyId('chat', keyId)
    void loadTextModels(keyId)
  }
})
watch(selectedModel, (model) => {
  if (!suppressCanvasModelWatchers && selectedKeyId.value) rememberSelectedCanvasModel('image', selectedKeyId.value, model)
})
watch(selectedTextModel, (model) => {
  if (!suppressCanvasModelWatchers && selectedTextKeyId.value) rememberSelectedCanvasModel('chat', selectedTextKeyId.value, model)
})
watch(userId, (id) => {
  if (id) {
    loadCustomPrompts()
    loadWebdavConfig()
  }
}, { immediate: true })
watch(canvasPanelTab, (tab) => {
  if (tab === 'assets') void loadCanvasAssets()
  if (tab === 'prompts') void loadPromptLibraryData()
})

onMounted(async () => {
  sidebarCollapsedBeforeCanvas = appStore.sidebarCollapsed
  document.addEventListener('pointerdown', handleNodeCreateMenuOutsidePointerDown, true)
  // The canvas is rendered inside PlaygroundView's AppLayout with `embedded`,
  // so collapsing only in the standalone variant leaves the main sidebar
  // covering valuable canvas width when users enter the workspace.
  appStore.setSidebarCollapsed(true)
  appStore.setMobileOpen(false)
  refreshCanvasViewportSize()
  if (typeof ResizeObserver !== 'undefined' && canvasRef.value) {
    canvasResizeObserver = new ResizeObserver(refreshCanvasViewportSize)
    canvasResizeObserver.observe(canvasRef.value)
  } else {
    window.addEventListener('resize', refreshCanvasViewportSize)
  }
  refreshCanvasPlugins()
  await loadCanvasPluginRuntimes()
  if (typeof localStorage !== 'undefined') {
    try {
      const saved = JSON.parse(localStorage.getItem('sub2api.playground.canvas.agent') || '{}') as { endpoint?: string; token?: string }
      if (typeof saved.endpoint === 'string' && saved.endpoint) agentEndpoint.value = saved.endpoint
      if (typeof saved.token === 'string') agentToken.value = saved.token
    } catch {
      // Ignore malformed local Agent settings.
    }
  }
  await appStore.fetchPublicSettings()
  await loadKeys()
  await restoreImages()
  await restoreVideos()
  await restoreAudio()
  window.addEventListener('keydown', handleKeyDown)
  window.addEventListener('keyup', handleKeyUp)
  window.addEventListener('paste', handlePaste)
  window.addEventListener('blur', resetTemporaryCanvasTool)
  window.addEventListener('blur', cancelActiveCanvasPointerGesture)
})

function handleNodeCreateMenuOutsidePointerDown(event: PointerEvent): void {
  if (!nodeCreatePosition.value) return
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('[data-canvas-create-menu]')) return
  nodeCreatePosition.value = null
  pendingConnectionSource.value = null
  pendingConnectionHandleType.value = 'source'
}

function resetTemporaryCanvasTool(): void {
  temporaryPanBaseTool.value = null
}

function cancelActiveCanvasPointerGesture(): void {
  stopPointerAction()
  cancelConnection()
  stopCanvasPanelResize()
}

function handleKeyDown(event: KeyboardEvent): void {
  const target = event.target instanceof Element ? event.target : null
  if (target?.closest('input,textarea,select,[contenteditable="true"]')) return
  const modifier = event.metaKey || event.ctrlKey
  // Handle clipboard/history commands before the temporary V-to-pan
  // override. Otherwise Ctrl/⌘+V is swallowed as a pan shortcut.
  if (modifier && event.key.toLowerCase() === 'a') {
    event.preventDefault()
    selectedNodeIds.value = new Set(activeProject.value?.nodes.map((node) => node.id) ?? [])
    selectedNodeId.value = selectedNodeIds.value.values().next().value ?? null
    return
  }
  if (modifier && event.key.toLowerCase() === 'c') {
    event.preventDefault()
    copySelectedNodes()
    return
  }
  if (modifier && event.key.toLowerCase() === 'v') {
    event.preventDefault()
    void pasteNodes()
    return
  }
  if (modifier && event.key.toLowerCase() === 'd') {
    event.preventDefault()
    copySelectedNodes()
    void pasteNodes()
    return
  }
  if (modifier && event.key.toLowerCase() === 'z') {
    event.preventDefault()
    if (event.shiftKey) canvasStore.redo()
    else canvasStore.undo()
    return
  }
  if (modifier && event.key.toLowerCase() === 'y') {
    event.preventDefault()
    canvasStore.redo()
    return
  }
  if (event.code === 'Space') {
    event.preventDefault()
    if (!temporaryPanBaseTool.value) temporaryPanBaseTool.value = tool.value
    return
  }
  if (event.key.toLowerCase() === 'v') {
    event.preventDefault()
    if (!temporaryPanBaseTool.value) temporaryPanBaseTool.value = tool.value
    return
  }
  if ((event.key === 'Delete' || event.key === 'Backspace') && (selectedNodeIds.value.size || selectedConnectionId.value)) {
    event.preventDefault()
    deleteSelected()
  }
  if (event.key === 'Escape') {
    event.preventDefault()
    closeContextMenu()
    canvasMenuOpen.value = false
    shortcutsOpen.value = false
    nodeCreatePosition.value = null
    pendingConnectionSource.value = null
    pendingConnectionHandleType.value = 'source'
    cancelConnection()
    cancelPluginReferenceSelection()
    assistantOpen.value = false
    agentOpen.value = false
    pluginPanelNodeId.value = null
    pluginManagerOpen.value = false
    promptLibraryOpen.value = false
    webdavOpen.value = false
    selectedNodeIds.value = new Set()
    selectedNodeId.value = null
    selectedConnectionId.value = null
    return
  }
}

function clearSelectedNodes(): void {
  if (!selectedNodeIds.value.size) return
  if (!window.confirm(t('playground.canvasClearSelectionConfirm', { count: selectedNodeIds.value.size }))) return
  canvasStore.checkpoint()
  for (const id of [...selectedNodeIds.value]) {
    cancelNodeGeneration(id)
    cancelVideoNode(id)
    cancelAudioNode(id)
    canvasStore.removeNode(id)
  }
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
}

function clearCanvas(): void {
  const count = activeProject.value?.nodes.length ?? 0
  if (!count || !window.confirm(t('playground.canvasClearCanvasConfirm', { count }))) return
  canvasStore.checkpoint()
  for (const node of activeProject.value?.nodes ?? []) {
    cancelNodeGeneration(node.id)
    cancelVideoNode(node.id)
    cancelAudioNode(node.id)
  }
  canvasStore.clearProject()
  selectedNodeIds.value = new Set()
  selectedNodeId.value = null
  pendingConnectionSource.value = null
  pendingConnectionHandleType.value = 'source'
}

function handleKeyUp(event: KeyboardEvent): void {
  if (event.code === 'Space' || event.code === 'KeyV') {
    event.preventDefault()
    resetTemporaryCanvasTool()
  }
}

onBeforeUnmount(() => {
  // Flush the debounced canvas snapshot before the view is torn down.
  canvasStore.persist()
  stopCanvasPanelResize()
  if (sidebarCollapsedBeforeCanvas !== null) {
    appStore.setSidebarCollapsed(sidebarCollapsedBeforeCanvas)
    sidebarCollapsedBeforeCanvas = null
  }
  closeAssetPicker()
  cancelAssistant()
  textGenerationControllers.forEach((controller) => controller.abort())
  textGenerationControllers.clear()
  videoControllers.forEach((controller) => controller.abort())
  videoControllers.clear()
  audioControllers.forEach((controller) => controller.abort())
  audioControllers.clear()
  window.removeEventListener('keydown', handleKeyDown)
  window.removeEventListener('keyup', handleKeyUp)
  window.removeEventListener('paste', handlePaste)
  document.removeEventListener('pointerdown', handleNodeCreateMenuOutsidePointerDown, true)
  window.removeEventListener('blur', resetTemporaryCanvasTool)
  window.removeEventListener('blur', cancelActiveCanvasPointerGesture)
  canvasResizeObserver?.disconnect()
  canvasResizeObserver = null
  window.removeEventListener('resize', refreshCanvasViewportSize)
  resetViewportCheckpointSession()
  stopPointerAction()
  if (agentPublishTimer !== null) window.clearTimeout(agentPublishTimer)
  agentBridge.disconnect()
  imageObjectUrls.forEach((url) => URL.revokeObjectURL(url))
})
</script>

<style scoped>
.canvas-menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.625rem;
  border-radius: 0.625rem;
  padding: 0.625rem 0.75rem;
  text-align: left;
  color: rgb(55 65 81);
  transition: background-color 150ms ease, color 150ms ease;
}

.canvas-menu-item:hover {
  background: rgb(240 253 250);
  color: rgb(17 94 89);
}

.canvas-menu-item:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

:global(.dark) .canvas-menu-item {
  color: rgb(209 213 219);
}

:global(.dark) .canvas-menu-item:hover {
  background: rgba(20, 184, 166, 0.18);
  color: rgb(153 246 228);
}
</style>
