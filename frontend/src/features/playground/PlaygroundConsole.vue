<template>
  <div v-if="loadingKeys" class="flex min-h-[calc(100vh-11rem)] items-center justify-center">
    <LoadingSpinner size="lg" />
  </div>

  <div v-else class="-m-4 flex h-[calc(100vh-4rem)] min-h-[34rem] overflow-hidden bg-white dark:bg-dark-950 md:-m-6 lg:-m-8">
    <button
      v-if="historyOpen"
      type="button"
      class="fixed inset-0 z-30 bg-gray-950/30 lg:hidden"
      :aria-label="t('playground.closeHistory')"
      @click="historyOpen = false"
    ></button>

    <aside
      :class="[
        'fixed inset-y-0 left-0 z-40 flex w-72 max-w-[86vw] shrink-0 -translate-x-full flex-col border-r border-gray-200 bg-gray-50 transition-transform dark:border-dark-700 dark:bg-dark-900 lg:static lg:z-auto lg:max-w-none lg:translate-x-0',
        { 'translate-x-0': historyOpen },
      ]"
      aria-label="Project history"
    >
      <div class="flex items-center gap-2 border-b border-gray-200 p-3 dark:border-dark-700">
        <button
          type="button"
          class="btn btn-primary flex min-w-0 flex-1 items-center justify-center gap-2 px-3 py-2 text-sm"
          :disabled="isGenerating"
          @click="newConversation()"
        >
          <Icon name="plus" size="sm" />
          <span>{{ newConversationLabel }}</span>
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-icon h-9 w-9 p-0"
          :title="t('playground.newProject')"
          :disabled="isGenerating"
          @click="startCreateProject"
        >
          <Icon name="inbox" size="sm" />
          <span class="sr-only">{{ t('playground.newProject') }}</span>
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-icon h-9 w-9 p-0"
          :title="selectionMode ? t('playground.cancelSelection') : t('playground.selectConversations')"
          :disabled="isGenerating"
          @click="toggleSelectionMode"
        >
          <Icon :name="selectionMode ? 'x' : 'check'" size="sm" />
          <span class="sr-only">{{ selectionMode ? t('playground.cancelSelection') : t('playground.selectConversations') }}</span>
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-icon h-9 w-9 p-0 lg:hidden"
          :title="t('playground.closeHistory')"
          @click="historyOpen = false"
        >
          <Icon name="x" size="sm" />
          <span class="sr-only">{{ t('playground.closeHistory') }}</span>
        </button>
      </div>

      <div v-if="selectionMode" class="flex items-center gap-1 border-b border-gray-200 px-3 py-2 dark:border-dark-700">
        <button type="button" class="btn btn-ghost h-8 px-2 text-xs" :disabled="isGenerating || modeConversations.length === 0" @click="selectAllModeConversations">
          {{ t('playground.selectAllConversations') }}
        </button>
        <button type="button" class="btn btn-ghost h-8 px-2 text-xs" :disabled="isGenerating || selectedConversationIds.size === 0" @click="clearConversationSelection">
          {{ t('playground.clearSelection') }}
        </button>
        <button type="button" class="btn btn-ghost ml-auto h-8 px-2 text-xs text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30" :disabled="isGenerating || selectedConversationIds.size === 0" @click="deleteSelectedConversations">
          {{ t('playground.deleteSelectedConversations', { count: selectedConversationIds.size }) }}
        </button>
      </div>

      <form v-if="creatingProject" class="border-b border-gray-200 p-3 dark:border-dark-700" @submit.prevent="createProjectFromDraft">
        <label class="sr-only" for="playground-project-name">{{ t('playground.projectName') }}</label>
        <div class="flex gap-2">
          <input
            id="playground-project-name"
            v-model="projectNameDraft"
            class="input min-w-0 flex-1 px-3 py-2 text-sm"
            maxlength="80"
            :placeholder="t('playground.projectNamePlaceholder')"
            :disabled="isGenerating"
          >
          <button type="submit" class="btn btn-primary btn-icon h-9 w-9 p-0" :title="t('common.save')" :disabled="isGenerating">
            <Icon name="check" size="sm" />
            <span class="sr-only">{{ t('common.save') }}</span>
          </button>
          <button type="button" class="btn btn-ghost btn-icon h-9 w-9 p-0" :title="t('common.cancel')" @click="cancelProjectEdit">
            <Icon name="x" size="sm" />
            <span class="sr-only">{{ t('common.cancel') }}</span>
          </button>
        </div>
      </form>

      <nav class="min-h-0 flex-1 overflow-y-auto p-2" :aria-label="t('playground.projectHistory')">
        <section v-for="group in conversationGroups" :key="group.id" class="mb-3">
          <form
            v-if="editingProjectId === group.project?.id"
            class="mb-1 px-1"
            @submit.prevent="saveProjectName(group.project!.id)"
          >
            <label class="sr-only" :for="`project-${group.project!.id}`">{{ t('playground.projectName') }}</label>
            <div class="flex gap-1">
              <input
                :id="`project-${group.project!.id}`"
                v-model="projectNameDraft"
                class="input min-w-0 flex-1 px-2 py-1.5 text-xs"
                maxlength="80"
                :disabled="isGenerating"
              >
              <button type="submit" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.save')">
                <Icon name="check" size="xs" />
                <span class="sr-only">{{ t('common.save') }}</span>
              </button>
              <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.cancel')" @click="cancelProjectEdit">
                <Icon name="x" size="xs" />
                <span class="sr-only">{{ t('common.cancel') }}</span>
              </button>
            </div>
          </form>

          <div class="group/project flex items-center gap-1 px-1">
            <button
              type="button"
              class="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs font-semibold text-gray-500 transition-colors hover:bg-gray-200/70 hover:text-gray-800 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100"
              :aria-expanded="isGroupExpanded(group.id)"
              :aria-controls="`playground-group-${group.id}`"
              @click="toggleGroup(group.id)"
            >
              <Icon :name="isGroupExpanded(group.id) ? 'chevronDown' : 'chevronRight'" size="xs" class="shrink-0" />
              <Icon :name="group.project ? 'inbox' : 'chatBubble'" size="xs" class="shrink-0" />
              <span class="truncate">{{ group.label }}</span>
              <span class="ml-auto text-[10px] font-medium text-gray-400 dark:text-dark-500">{{ group.conversations.length }}</span>
            </button>

            <div class="flex shrink-0 items-center opacity-100 transition-opacity lg:opacity-0 lg:group-hover/project:opacity-100">
              <button
                type="button"
                class="btn btn-ghost btn-icon h-7 w-7 p-0"
                :title="newConversationLabel"
                :disabled="isGenerating"
                @click="newConversation(group.project?.id ?? null)"
              >
                <Icon name="plus" size="xs" />
                <span class="sr-only">{{ newConversationLabel }}</span>
              </button>
              <template v-if="group.project">
                <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.renameProject')" :disabled="isGenerating" @click="startRenameProject(group.project)">
                  <Icon name="edit" size="xs" />
                  <span class="sr-only">{{ t('playground.renameProject') }}</span>
                </button>
                <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-red-600 dark:text-red-400" :title="t('playground.deleteProject')" :disabled="isGenerating" @click="deleteProject(group.project.id)">
                  <Icon name="trash" size="xs" />
                  <span class="sr-only">{{ t('playground.deleteProject') }}</span>
                </button>
              </template>
            </div>
          </div>

          <ul v-show="isGroupExpanded(group.id)" :id="`playground-group-${group.id}`" class="mt-1 space-y-0.5">
            <li v-for="conversation in group.conversations" :key="conversation.id" class="group/conversation flex min-w-0 items-center gap-0.5">
              <input
                v-if="selectionMode"
                type="checkbox"
                class="ml-2 h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="selectedConversationIds.has(conversation.id)"
                :aria-label="conversationTitle(conversation)"
                @change="toggleConversationSelection(conversation.id)"
              >
              <button
                type="button"
                class="min-w-0 flex-1 rounded-md border-l-2 px-2 py-2 text-left transition-colors"
                :class="conversation.id === activeConversationId
                  ? 'border-primary-500 bg-white text-gray-900 shadow-sm dark:bg-dark-800 dark:text-white'
                  : 'border-transparent text-gray-600 hover:bg-gray-200/70 hover:text-gray-900 dark:text-dark-300 dark:hover:bg-dark-800 dark:hover:text-white'"
                :disabled="isGenerating"
                :aria-current="conversation.id === activeConversationId ? 'page' : undefined"
                @click="selectionMode ? toggleConversationSelection(conversation.id) : selectConversation(conversation.id)"
              >
                <span class="flex min-w-0 items-center gap-2">
                  <Icon name="chatBubble" size="xs" class="shrink-0" />
                  <span class="min-w-0 flex-1 truncate text-sm font-medium">{{ conversationTitle(conversation) }}</span>
                </span>
                <span class="mt-0.5 block truncate pl-5 text-[11px] text-gray-400 dark:text-dark-500">{{ conversationPreview(conversation) }}</span>
              </button>
              <div v-if="!selectionMode" class="flex shrink-0 items-center opacity-100 transition-opacity lg:opacity-0 lg:group-hover/conversation:opacity-100">
                <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.renameConversation')" :disabled="isGenerating" @click="startRenameConversation(conversation)">
                  <Icon name="edit" size="xs" />
                  <span class="sr-only">{{ t('playground.renameConversation') }}</span>
                </button>
                <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-red-600 dark:text-red-400" :title="t('playground.deleteConversation')" :disabled="isGenerating" @click="deleteConversation(conversation.id)">
                  <Icon name="trash" size="xs" />
                  <span class="sr-only">{{ t('playground.deleteConversation') }}</span>
                </button>
              </div>
            </li>
          </ul>
        </section>
      </nav>
    </aside>

    <section class="relative flex min-w-0 flex-1 flex-col">
      <header class="relative flex min-h-14 flex-wrap items-center gap-2 border-b border-gray-200 px-3 py-2 dark:border-dark-700 md:px-5">
        <button type="button" class="btn btn-ghost btn-icon h-9 w-9 p-0 lg:hidden" :title="t('playground.openHistory')" @click="historyOpen = true">
          <Icon name="menu" size="sm" />
          <span class="sr-only">{{ t('playground.openHistory') }}</span>
        </button>

        <div class="min-w-0 flex-1">
          <p class="truncate text-xs text-gray-500 dark:text-dark-400">{{ activeProjectName }}</p>
          <form v-if="renamingConversation" class="mt-0.5 flex max-w-xl gap-2" @submit.prevent="saveConversationName">
            <label class="sr-only" for="playground-conversation-title">{{ t('playground.renameConversation') }}</label>
            <input id="playground-conversation-title" v-model="conversationNameDraft" class="input min-w-0 flex-1 px-2 py-1 text-sm" maxlength="80" :disabled="isGenerating">
            <button type="submit" class="btn btn-primary btn-icon h-8 w-8 p-0" :title="t('common.save')">
              <Icon name="check" size="xs" />
              <span class="sr-only">{{ t('common.save') }}</span>
            </button>
            <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.cancel')" @click="cancelConversationRename">
              <Icon name="x" size="xs" />
              <span class="sr-only">{{ t('common.cancel') }}</span>
            </button>
          </form>
          <button v-else type="button" class="max-w-full truncate text-left text-sm font-semibold text-gray-900 hover:text-primary-700 dark:text-white dark:hover:text-primary-300" :disabled="isGenerating" @click="startRenameConversation(activeConversation)">
            {{ activeConversation ? conversationTitle(activeConversation) : newConversationLabel }}
          </button>
        </div>

        <nav class="order-3 flex w-full items-center overflow-x-auto rounded-md bg-gray-100 p-1 text-xs dark:bg-dark-800 sm:order-none sm:w-auto sm:shrink-0" :aria-label="t('playground.workspaceLabel')">
          <RouterLink to="/playground/chat" class="rounded px-2.5 py-1.5 font-medium transition-colors" :class="!isImageMode ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'">{{ t('playground.chatWorkspace') }}</RouterLink>
          <RouterLink to="/playground/images" class="rounded px-2.5 py-1.5 font-medium transition-colors" :class="isImageMode ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100'">{{ t('playground.imageWorkspace') }}</RouterLink>
          <RouterLink to="/playground/gallery" class="rounded px-2.5 py-1.5 font-medium text-gray-500 transition-colors hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-100">{{ t('playground.galleryWorkspace') }}</RouterLink>
        </nav>
        <select v-model="selectedKeyId" class="select h-9 w-48 max-w-[38vw] text-xs sm:w-60" :disabled="isGenerating" :aria-label="t('playground.keyLabel')" :title="selectedKey ? playgroundKeyLabel(selectedKey) : ''">
          <option v-for="key in playgroundKeys" :key="key.id" :value="key.id">{{ playgroundKeyLabel(key) }}</option>
        </select>
        <select v-model="selectedModel" class="select h-9 min-w-0 max-w-36 text-xs sm:max-w-48" :disabled="loadingModels || isGenerating || models.length === 0" :aria-label="t('playground.modelLabel')">
          <option value="" disabled>{{ loadingModels ? t('playground.loadingModels') : t('playground.noModels') }}</option>
          <option v-for="model in models" :key="model.id" :value="model.id">{{ model.id }}</option>
        </select>
        <button type="button" class="btn btn-ghost btn-icon h-9 w-9 p-0" :title="t('playground.refreshModels')" :disabled="loadingModels || !selectedKeyId || isGenerating" @click="refreshModels">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loadingModels }" />
          <span class="sr-only">{{ t('playground.refreshModels') }}</span>
        </button>
        <button v-if="!isImageMode" type="button" class="btn btn-ghost btn-icon h-9 w-9 p-0" :title="t('playground.conversationSettings')" :disabled="isGenerating" @click="conversationSettingsOpen = !conversationSettingsOpen">
          <Icon name="cog" size="sm" />
          <span class="sr-only">{{ t('playground.conversationSettings') }}</span>
        </button>

        <div v-if="!isImageMode && conversationSettingsOpen" class="absolute right-3 top-[calc(100%+0.5rem)] z-30 w-[min(28rem,calc(100vw-2rem))] rounded-lg border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-800 md:right-5">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.conversationSettings') }}</h2>
            <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="conversationSettingsOpen = false">
              <Icon name="x" size="sm" />
              <span class="sr-only">{{ t('common.close') }}</span>
            </button>
          </div>
          <label for="playground-system-prompt" class="mb-1.5 block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('playground.systemPrompt') }}</label>
          <textarea
            id="playground-system-prompt"
            v-model="systemPrompt"
            rows="6"
            maxlength="8000"
            class="input min-h-32 w-full resize-y px-3 py-2 text-sm leading-6"
            :placeholder="t('playground.systemPromptPlaceholder')"
            :disabled="isGenerating"
          ></textarea>
        </div>
      </header>

      <main ref="transcript" class="min-h-0 flex-1 overflow-y-auto">
        <section v-if="messages.length === 0" class="mx-auto flex min-h-full w-full max-w-3xl items-center px-4 py-10 md:px-8">
          <div class="w-full text-center">
            <span class="mx-auto flex h-11 w-11 items-center justify-center rounded-lg border border-gray-200 bg-gray-50 text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">
              <Icon name="chatBubble" size="md" />
            </span>
            <h1 class="mt-5 text-xl font-semibold text-gray-900 dark:text-white">{{ workspaceTitle }}</h1>
            <p class="mx-auto mt-2 max-w-lg text-sm leading-6 text-gray-500 dark:text-dark-400">{{ workspaceDescription }}</p>
            <div class="mx-auto mt-6 grid max-w-2xl gap-2 text-left sm:grid-cols-2">
              <button
                v-for="prompt in starterPrompts"
                :key="prompt.key"
                type="button"
                class="flex min-h-11 items-center gap-2 rounded-lg border border-gray-200 bg-white px-3 py-2.5 text-sm font-medium text-gray-700 transition-colors hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/70 dark:text-gray-200 dark:hover:border-dark-600 dark:hover:bg-dark-800"
                :disabled="isGenerating"
                @click="selectStarterPrompt(prompt.text)"
              >
                <Icon :name="prompt.icon" size="sm" class="shrink-0 text-gray-500 dark:text-dark-400" />
                <span>{{ prompt.text }}</span>
              </button>
            </div>
          </div>
        </section>

        <section v-else class="mx-auto w-full max-w-3xl px-4 py-7 md:px-8 md:py-9">
          <article v-for="message in messages" :key="message.id" class="group mb-7 last:mb-2">
            <div class="flex" :class="message.role === 'user' ? 'justify-end' : 'justify-start'">
              <div class="min-w-0" :class="message.role === 'user' ? 'max-w-[88%]' : 'w-full'">
                <div class="mb-1.5 flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400" :class="message.role === 'user' ? 'justify-end' : ''">
                  <span class="font-medium text-gray-700 dark:text-gray-200">{{ roleLabel(message.role) }}</span>
                  <span>{{ formatTime(message.createdAt) }}</span>
                  <span v-if="message.role === 'assistant' && isGenerating && message.id === streamingMessageId" class="inline-flex items-center gap-1 text-primary-600 dark:text-primary-300">
                    <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-current"></span>
                    {{ generationProgressLabel }}
                  </span>
                </div>

                <div :class="messageSurfaceClass(message.role)">
                  <template v-if="editingMessageId === message.id">
                    <textarea v-model="editingContent" rows="5" class="input min-h-28 resize-y" :aria-label="t('playground.editMessage')"></textarea>
                    <div class="mt-3 flex justify-end gap-2">
                      <button type="button" class="btn btn-ghost btn-sm" @click="cancelEdit">{{ t('playground.cancelEdit') }}</button>
                      <button type="button" class="btn btn-primary btn-sm" :disabled="!editingContent.trim() || isGenerating" @click="resendEditedMessage(message.id)">
                        <Icon name="check" size="sm" />
                        {{ actionLabel }}
                      </button>
                    </div>
                  </template>

                  <template v-else>
                    <div v-if="messageError(message)" class="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2.5 text-sm leading-6 text-red-700 dark:border-red-900/60 dark:bg-red-950/20 dark:text-red-300">
                      <Icon name="exclamationCircle" size="sm" class="mt-0.5 shrink-0" />
                      <div>
                        <p class="font-medium">{{ t('playground.messageFailed') }}</p>
                        <p class="mt-0.5 break-words">{{ messageError(message) }}</p>
                      </div>
                    </div>

                    <div v-if="message.reasoningContent" class="mb-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs leading-5 text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-100">
                      <div class="mb-1 flex items-center gap-1.5 font-semibold">
                        <Icon name="brain" size="xs" />
                        {{ t('playground.reasoning') }}
                      </div>
                      <div class="whitespace-pre-wrap">{{ message.reasoningContent }}</div>
                    </div>

                    <div v-if="message.imageUrls?.length" class="grid gap-3 sm:grid-cols-2">
                      <div v-for="(imageUrl, imageIndex) in message.imageUrls" :key="`${message.id}-${imageIndex}`" class="group/image relative overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900">
                        <a v-if="!isImageLoadFailed(message.id, imageIndex)" :href="imageUrl" target="_blank" rel="noreferrer" class="block">
                          <img :src="imageUrl" :alt="t('playground.generatedImage')" class="block h-auto max-h-[32rem] w-full object-contain" loading="lazy" @error="markImageLoadFailed(message.id, imageIndex)">
                        </a>
                        <div v-else class="flex min-h-56 flex-col items-center justify-center gap-2 p-5 text-center text-sm text-gray-500 dark:text-dark-400">
                          <Icon name="exclamationCircle" size="md" />
                          <p>{{ t('playground.imageLoadFailed') }}</p>
                          <a :href="imageUrl" target="_blank" rel="noreferrer" class="text-primary-600 hover:underline dark:text-primary-300">{{ t('playground.openImage') }}</a>
                        </div>
                        <button
                          type="button"
                          class="btn btn-secondary btn-icon absolute right-2 top-2 h-8 w-8 bg-white/95 p-0 shadow-sm dark:bg-dark-800/95"
                          :title="t('playground.downloadImage')"
                          :disabled="isDownloadingImage(message.id, imageIndex)"
                          @click="downloadGeneratedImage(message, imageUrl, imageIndex)"
                        >
                          <Icon :name="isDownloadingImage(message.id, imageIndex) ? 'refresh' : 'download'" size="sm" :class="{ 'animate-spin': isDownloadingImage(message.id, imageIndex) }" />
                          <span class="sr-only">{{ t('playground.downloadImage') }}</span>
                        </button>
                      </div>
                    </div>
                    <p v-if="message.revisedPrompt" class="mt-3 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ message.revisedPrompt }}</p>
                    <div v-if="message.attachments?.length" class="mt-3 flex flex-wrap gap-1.5">
                      <span v-for="attachment in message.attachments" :key="attachment.id" class="inline-flex max-w-full items-center gap-1 rounded-md border border-gray-200 px-2 py-1 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-300">
                        <Icon name="document" size="xs" />
                        <span class="max-w-56 truncate">{{ attachment.name }}</span>
                      </span>
                    </div>
                    <div v-if="message.content && !message.imageUrls?.length" class="playground-markdown" v-html="renderMarkdown(message.content)"></div>
                    <div v-else-if="message.role === 'assistant' && isGenerating && message.id === streamingMessageId" class="flex items-start gap-2 text-sm text-gray-500 dark:text-dark-400">
                      <span class="mt-2 inline-flex shrink-0 gap-1">
                        <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-current [animation-delay:-0.2s]"></span>
                        <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-current [animation-delay:-0.1s]"></span>
                        <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-current"></span>
                      </span>
                      <div>
                        <p>{{ generationProgressLabel }}</p>
                        <p v-if="isImageMode" class="mt-0.5 text-xs leading-5 text-gray-400 dark:text-dark-500">{{ t('playground.imageGenerationWaitHint') }}</p>
                      </div>
                    </div>
                  </template>
                </div>

                <div class="mt-1.5 flex items-center gap-0.5 opacity-100 transition-opacity md:opacity-0 md:group-hover:opacity-100" :class="message.role === 'user' ? 'justify-end' : ''">
                  <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.copy')" @click="copyMessage(message)">
                    <Icon name="copy" size="xs" />
                    <span class="sr-only">{{ t('playground.copy') }}</span>
                  </button>
                  <button v-if="message.role === 'user'" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.editMessage')" :disabled="isGenerating" @click="startEdit(message)">
                    <Icon name="edit" size="xs" />
                    <span class="sr-only">{{ t('playground.editMessage') }}</span>
                  </button>
                  <button v-if="message.role === 'assistant'" type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0" :title="t('playground.regenerate')" :disabled="isGenerating" @click="regenerate(message.id)">
                    <Icon name="refresh" size="xs" />
                    <span class="sr-only">{{ t('playground.regenerate') }}</span>
                  </button>
                  <button type="button" class="btn btn-ghost btn-icon h-7 w-7 p-0 text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30" :title="t('playground.deleteMessage')" :disabled="isGenerating" @click="deleteMessage(message.id)">
                    <Icon name="trash" size="xs" />
                    <span class="sr-only">{{ t('playground.deleteMessage') }}</span>
                  </button>
                </div>
              </div>
            </div>
          </article>
        </section>
      </main>

      <div class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-950 md:p-4">
        <div class="relative mx-auto w-full max-w-3xl rounded-xl border border-gray-200 bg-white shadow-[0_16px_36px_-26px_rgba(15,23,42,0.45)] dark:border-dark-700 dark:bg-dark-800">
          <div v-if="!isImageMode && parameterPanelOpen" class="absolute bottom-full left-0 z-30 mb-3 w-[min(22rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-dark-600 dark:bg-dark-800">
            <div class="flex items-start justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
              <div>
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.parameterSettings') }}</h2>
                <p class="mt-0.5 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.enabledParametersHint') }}</p>
              </div>
              <div class="flex items-center gap-1">
                <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('playground.resetParameters')" @click="resetParameters">
                  <Icon name="refresh" size="sm" />
                  <span class="sr-only">{{ t('playground.resetParameters') }}</span>
                </button>
                <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="parameterPanelOpen = false">
                  <Icon name="x" size="sm" />
                  <span class="sr-only">{{ t('common.close') }}</span>
                </button>
              </div>
            </div>

            <div class="max-h-[min(54vh,30rem)] space-y-3 overflow-y-auto p-3">
              <section v-for="control in parameterControls" :key="control.key" class="rounded-lg border border-gray-200 p-3 transition-opacity dark:border-dark-700" :class="{ 'opacity-55': !parameters.enabled[control.key] || isGenerating }">
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0">
                    <div class="flex items-center gap-2">
                      <label :for="`playground-${control.key}`" class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t(control.labelKey) }}</label>
                      <span class="rounded border border-gray-200 px-1.5 py-0.5 font-mono text-[11px] text-gray-500 dark:border-dark-600 dark:text-dark-400">{{ parameterDisplayValue(control.key) }}</span>
                    </div>
                    <p class="mt-0.5 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t(control.descriptionKey) }}</p>
                  </div>
                  <label class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center" :title="t(control.labelKey)">
                    <input class="peer sr-only" type="checkbox" :checked="parameters.enabled[control.key]" :disabled="isGenerating" @change="setParameterEnabled(control.key, ($event.target as HTMLInputElement).checked)">
                    <span class="h-5 w-9 rounded-full bg-gray-200 transition-colors peer-checked:bg-primary-500 peer-disabled:cursor-not-allowed dark:bg-dark-600"></span>
                    <span class="absolute left-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4"></span>
                  </label>
                </div>

                <input
                  v-if="control.kind === 'range'"
                  :id="`playground-${control.key}`"
                  class="mt-3 w-full accent-primary-600"
                  type="range"
                  :min="control.min"
                  :max="control.max"
                  :step="control.step"
                  :value="parameterValue(control.key) ?? 0"
                  :disabled="!parameters.enabled[control.key] || isGenerating"
                  @input="setParameterValue(control.key, $event)"
                >
                <input
                  v-else
                  :id="`playground-${control.key}`"
                  class="input mt-3 px-3 py-2"
                  type="number"
                  :min="control.min"
                  :max="control.max"
                  :step="control.step"
                  :value="parameterValue(control.key) ?? ''"
                  :disabled="!parameters.enabled[control.key] || isGenerating"
                  @input="setParameterValue(control.key, $event)"
                >
              </section>

              <label class="flex items-center justify-between gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
                <span>
                  <span class="block text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('playground.stream') }}</span>
                  <span class="mt-0.5 block text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.streamHint') }}</span>
                </span>
                <input v-model="parameters.stream" type="checkbox" class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :disabled="isGenerating">
              </label>
            </div>
          </div>

          <div v-if="isImageMode && imageSettingsOpen" class="absolute bottom-full left-0 z-30 mb-3 w-[min(32rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-dark-600 dark:bg-dark-800">
            <div class="flex items-start justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
              <div>
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('playground.imageSettings') }}</h2>
                <p class="mt-0.5 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.imageSettingsDescription') }}</p>
              </div>
              <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0" :title="t('common.close')" @click="imageSettingsOpen = false">
                <Icon name="x" size="sm" />
                <span class="sr-only">{{ t('common.close') }}</span>
              </button>
            </div>

            <div class="grid max-h-[min(68vh,38rem)] gap-4 overflow-y-auto p-4 sm:grid-cols-2">
              <label class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageSize') }}</span>
                <select v-model="imageSize" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageSizeAuto') }}</option>
                  <option value="1024x1024">{{ t('playground.imageSizeSquare') }}</option>
                  <option value="1536x1024">{{ t('playground.imageSizeLandscape') }}</option>
                  <option value="1024x1536">{{ t('playground.imageSizePortrait') }}</option>
                  <option value="custom">{{ t('playground.imageSizeCustom') }}</option>
                </select>
              </label>

              <label class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageQuality') }}</span>
                <select v-model="imageQuality" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageQualityAuto') }}</option>
                  <option value="low">{{ t('playground.imageQualityLow') }}</option>
                  <option value="medium">{{ t('playground.imageQualityMedium') }}</option>
                  <option value="high">{{ t('playground.imageQualityHigh') }}</option>
                </select>
              </label>

              <div v-if="imageSize === 'custom'" class="sm:col-span-2">
                <div class="grid grid-cols-[1fr_auto_1fr] items-end gap-2">
                  <label class="block text-xs font-medium text-gray-600 dark:text-dark-300">
                    <span>{{ t('playground.imageWidth') }}</span>
                    <input v-model.number="imageCustomWidth" type="number" min="16" max="3840" step="16" class="input mt-1 h-9 px-3 text-sm" :disabled="isGenerating">
                  </label>
                  <span class="pb-2 text-gray-400">x</span>
                  <label class="block text-xs font-medium text-gray-600 dark:text-dark-300">
                    <span>{{ t('playground.imageHeight') }}</span>
                    <input v-model.number="imageCustomHeight" type="number" min="16" max="3840" step="16" class="input mt-1 h-9 px-3 text-sm" :disabled="isGenerating">
                  </label>
                </div>
                <p class="mt-1.5 text-xs" :class="customImageSizeValid ? 'text-gray-500 dark:text-dark-400' : 'text-red-600 dark:text-red-400'">
                  {{ customImageSizeValid ? customImageSizeSummary : t('playground.imageCustomSizeInvalid') }}
                </p>
              </div>

              <div class="sm:col-span-2">
                <div class="flex items-center justify-between gap-3">
                  <label for="playground-image-count" class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('playground.imageCount') }}</label>
                  <span class="rounded border border-gray-200 px-2 py-0.5 text-xs font-medium text-gray-600 dark:border-dark-600 dark:text-dark-300">{{ t('playground.imageCountOption', { count: imageCount }) }}</span>
                </div>
                <input id="playground-image-count" v-model.number="imageCount" type="range" min="1" max="10" step="1" class="mt-2 w-full accent-primary-600" :disabled="isGenerating">
              </div>

              <label v-if="supportsGptImageOptions" class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageOutputFormat') }}</span>
                <select v-model="imageOutputFormat" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="png">PNG</option>
                  <option value="jpeg">JPEG</option>
                  <option value="webp">WebP</option>
                </select>
              </label>

              <label v-if="supportsGptImageOptions" class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageBackground') }}</span>
                <select v-model="imageBackground" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageOptionAuto') }}</option>
                  <option value="opaque">{{ t('playground.imageBackgroundOpaque') }}</option>
                  <option value="transparent">{{ t('playground.imageBackgroundTransparent') }}</option>
                </select>
              </label>

              <div v-if="supportsGptImageOptions && imageOutputFormat !== 'png'" class="sm:col-span-2">
                <div class="flex items-center justify-between gap-3">
                  <label for="playground-image-compression" class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('playground.imageCompression') }}</label>
                  <span class="text-xs text-gray-500 dark:text-dark-400">{{ imageOutputCompression }}%</span>
                </div>
                <input id="playground-image-compression" v-model.number="imageOutputCompression" type="range" min="0" max="100" step="1" class="mt-2 w-full accent-primary-600" :disabled="isGenerating">
              </div>

              <label v-if="supportsGptImageOptions" class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageModeration') }}</span>
                <select v-model="imageModeration" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageOptionAuto') }}</option>
                  <option value="low">{{ t('playground.imageModerationLow') }}</option>
                </select>
              </label>

              <label v-if="supportsGptImageOptions || isDallE3Model" class="block text-sm font-medium text-gray-800 dark:text-gray-100">
                <span>{{ t('playground.imageStyle') }}</span>
                <select v-model="imageStyle" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageOptionAuto') }}</option>
                  <option value="natural">{{ t('playground.imageStyleNatural') }}</option>
                  <option value="vivid">{{ t('playground.imageStyleVivid') }}</option>
                </select>
              </label>

              <label v-if="supportsGptImageOptions" class="block text-sm font-medium text-gray-800 dark:text-gray-100 sm:col-span-2">
                <span>{{ t('playground.imageInputFidelity') }}</span>
                <select v-model="imageInputFidelity" class="select mt-1.5 h-9 w-full text-sm" :disabled="isGenerating">
                  <option value="auto">{{ t('playground.imageOptionAuto') }}</option>
                  <option value="low">{{ t('playground.imageInputFidelityLow') }}</option>
                  <option value="high">{{ t('playground.imageInputFidelityHigh') }}</option>
                </select>
                <span class="mt-1 block text-xs font-normal leading-5 text-gray-500 dark:text-dark-400">{{ t('playground.imageInputFidelityHint') }}</span>
              </label>
            </div>
          </div>

          <textarea
            ref="composer"
            v-model="draftContent"
            rows="3"
            maxlength="8000"
            class="block min-h-24 w-full resize-y border-0 bg-transparent px-4 py-3 text-sm leading-6 text-gray-900 outline-none placeholder:text-gray-400 focus:ring-0 dark:text-gray-100 dark:placeholder:text-dark-400"
            :placeholder="composerPlaceholder"
            :aria-label="t('playground.composerLabel')"
            :disabled="isGenerating"
            @keydown="handleComposerKeydown"
            @paste="handleComposerPaste"
          ></textarea>

          <div v-if="pendingAttachments.length" class="flex flex-wrap gap-1.5 border-t border-gray-100 px-3 py-2 dark:border-dark-700">
            <span v-for="attachment in pendingAttachments" :key="attachment.id" class="inline-flex max-w-full items-center gap-1 rounded-md bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-200">
              <span class="max-w-48 truncate">{{ attachment.name }}</span>
              <button type="button" class="shrink-0 text-gray-400 hover:text-red-500" title="移除附件" @click="removePendingAttachment(attachment.id)">
                <Icon name="x" size="xs" />
                <span class="sr-only">移除附件</span>
              </button>
            </span>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 px-2.5 py-2 dark:border-dark-700">
            <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1">
              <button v-if="!isImageMode" type="button" class="relative flex h-8 items-center gap-1.5 rounded-lg px-2 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-800 disabled:cursor-not-allowed disabled:opacity-50 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-dark-100" :disabled="isGenerating" :aria-expanded="parameterPanelOpen" @click="parameterPanelOpen = !parameterPanelOpen">
                <Icon name="cog" size="sm" />
                <span>{{ t('playground.parameters') }}</span>
                <span class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] text-primary-700 dark:bg-primary-900/40 dark:text-primary-200">{{ enabledParameterCount }}</span>
              </button>
              <template v-if="!isImageMode">
                <input ref="attachmentInput" type="file" multiple class="sr-only" @change="handleAttachmentChange">
                <button
                  type="button"
                  class="btn btn-ghost h-8 gap-1.5 px-2 text-xs"
                  :title="t('playground.uploadAttachments', { count: PLAYGROUND_MAX_ATTACHMENTS })"
                  :disabled="isGenerating || conversationAttachmentCount >= PLAYGROUND_MAX_ATTACHMENTS"
                  @click="attachmentInput?.click()"
                >
                  <Icon name="upload" size="sm" />
                  <span>{{ t('playground.uploadAttachments', { count: PLAYGROUND_MAX_ATTACHMENTS }) }}</span>
                </button>
              </template>
              <template v-if="isImageMode">
                <input ref="imageReferenceInput" type="file" accept="image/*" multiple class="sr-only" @change="handleAttachmentChange">
                <button
                  type="button"
                  class="btn btn-ghost h-8 gap-1.5 px-2 text-xs"
                  :title="t('playground.uploadReferenceImages', { count: PLAYGROUND_MAX_ATTACHMENTS })"
                  :disabled="isGenerating || conversationAttachmentCount >= PLAYGROUND_MAX_ATTACHMENTS"
                  @click="imageReferenceInput?.click()"
                >
                  <Icon name="upload" size="sm" />
                  <span>{{ t('playground.uploadReferenceImages', { count: PLAYGROUND_MAX_ATTACHMENTS }) }}</span>
                </button>
              </template>
              <button v-if="isImageMode" type="button" class="btn btn-ghost h-8 min-w-0 max-w-[min(18rem,60vw)] gap-1.5 px-2 text-xs" :title="t('playground.imageSettings')" :aria-expanded="imageSettingsOpen" @click="imageSettingsOpen = !imageSettingsOpen">
                <Icon name="cog" size="sm" />
                <span class="truncate">{{ imageSettingsSummary }}</span>
              </button>
              <button type="button" class="btn btn-ghost btn-icon h-8 w-8 p-0 text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30" :title="t('playground.clearConversation')" :disabled="messages.length === 0 || isGenerating" @click="clearHistory">
                <Icon name="trash" size="sm" />
                <span class="sr-only">{{ t('playground.clearConversation') }}</span>
              </button>
            </div>

            <div class="flex min-w-0 flex-1 items-center justify-end gap-1.5 sm:flex-none">
              <button v-if="isGenerating" type="button" class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-red-200 text-red-600 transition-colors hover:bg-red-50 dark:border-red-900/50 dark:text-red-300 dark:hover:bg-red-950/30" :title="t('playground.stop')" @click="stopCompletion">
                <Icon name="x" size="sm" />
                <span class="sr-only">{{ t('playground.stop') }}</span>
              </button>
              <button v-else type="button" class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary-500 text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-45" :title="actionLabel" :disabled="!canSend" @click="send">
                <Icon name="arrowUp" size="sm" />
                <span class="sr-only">{{ actionLabel }}</span>
              </button>
            </div>
          </div>
        </div>
      </div>

      <p class="sr-only" aria-live="polite">{{ isGenerating ? generationProgressLabel : '' }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { keysAPI } from '@/api'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { useClipboard } from '@/composables/useClipboard'
import { Icon } from '@/components/icons'
import { useAppStore, useAuthStore } from '@/stores'
import type { ApiKey } from '@/types'
import { fetchPlaygroundHistory, fetchPlaygroundModels, savePlaygroundHistory, sendPlaygroundChat, sendPlaygroundImageGeneration } from './api'
import { renderPlaygroundMarkdown } from './markdown'
import { createPlaygroundPersistScheduler, loadPlaygroundState, mergePlaygroundStates, savePlaygroundState, toPersistedState } from './persistence'
import { cachePlaygroundImage, createPlaygroundImageCacheKey, restoreCachedPlaygroundImage } from './imageCache'
import { downloadPlaygroundImage, imageFilenameExtension, playgroundImageUrl } from './imageDownload'
import type { PlaygroundAttachment, PlaygroundConversation, PlaygroundKeySummary, PlaygroundMessage, PlaygroundMode, PlaygroundModel, PlaygroundParameters, PlaygroundProject, PlaygroundRole } from './types'
import {
  buildChatPayload,
  buildPlaygroundImageRequest,
  normalizePlaygroundImageModels,
  normalizePlaygroundChatModels,
  cloneParameters,
  createConversation,
  createMessage,
  createProject,
  DEFAULT_PLAYGROUND_PARAMETERS,
  deriveConversationTitle,
  formatPlaygroundKeyLabel,
  normalizePlaygroundKeys,
  normalizeProjectName,
  PLAYGROUND_MAX_DRAFT_CHARS,
  PLAYGROUND_MAX_ATTACHMENT_BYTES,
  PLAYGROUND_MAX_ATTACHMENTS,
  PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS,
  rebuildConversationAfterUserEdit,
  resolvePlaygroundDefaultKeyId,
  resolvePlaygroundDefaultModel,
  resolveSelectedKeyId,
  resolveSelectedModel,
  withSystemPrompt,
} from './viewModel'

const props = defineProps<{
  mode: PlaygroundMode
}>()

type ParameterKey = 'temperature' | 'top_p' | 'max_tokens' | 'frequency_penalty' | 'presence_penalty' | 'seed'

type ParameterControl = {
  key: ParameterKey
  labelKey: string
  descriptionKey: string
  kind: 'range' | 'number'
  min: number
  max: number
  step: number
}

type ConversationGroup = {
  id: string
  label: string
  project: PlaygroundProject | null
  conversations: PlaygroundConversation[]
}

const parameterControls: ParameterControl[] = [
  { key: 'temperature', labelKey: 'playground.temperatureLabel', descriptionKey: 'playground.temperatureDescription', kind: 'range', min: 0, max: 2, step: 0.05 },
  { key: 'top_p', labelKey: 'playground.topPLabel', descriptionKey: 'playground.topPDescription', kind: 'range', min: 0, max: 1, step: 0.05 },
  { key: 'frequency_penalty', labelKey: 'playground.frequencyPenaltyLabel', descriptionKey: 'playground.frequencyPenaltyDescription', kind: 'range', min: -2, max: 2, step: 0.1 },
  { key: 'presence_penalty', labelKey: 'playground.presencePenaltyLabel', descriptionKey: 'playground.presencePenaltyDescription', kind: 'range', min: -2, max: 2, step: 0.1 },
  { key: 'max_tokens', labelKey: 'playground.maxTokensLabel', descriptionKey: 'playground.maxTokensDescription', kind: 'number', min: 1, max: 32768, step: 1 },
  { key: 'seed', labelKey: 'playground.seedLabel', descriptionKey: 'playground.seedDescription', kind: 'number', min: -2147483648, max: 2147483647, step: 1 },
]

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()
const authStore = useAuthStore()

const userId = computed(() => authStore.user?.id ?? null)
const loadingKeys = ref(true)
const loadingModels = ref(false)
const playgroundKeys = ref<PlaygroundKeySummary[]>([])
const models = ref<PlaygroundModel[]>([])
const selectedKeyId = ref<number | null>(null)
const selectedModel = ref('')
const imageSize = ref('1024x1024')
const imageCustomWidth = ref(1024)
const imageCustomHeight = ref(1024)
const imageQuality = ref('auto')
const imageCount = ref(1)
const imageOutputFormat = ref('png')
const imageOutputCompression = ref(90)
const imageBackground = ref('auto')
const imageModeration = ref('auto')
const imageInputFidelity = ref('auto')
const imageStyle = ref('auto')
const projects = ref<PlaygroundProject[]>([])
const conversations = ref<PlaygroundConversation[]>([])
const activeConversationId = ref<string | null>(null)
const parameters = ref<PlaygroundParameters>(cloneParameters(DEFAULT_PLAYGROUND_PARAMETERS))
const parameterPanelOpen = ref(false)
const imageSettingsOpen = ref(false)
const conversationSettingsOpen = ref(false)
const historyOpen = ref(false)
const expandedGroupIds = ref<string[]>([])
const creatingProject = ref(false)
const editingProjectId = ref<string | null>(null)
const projectNameDraft = ref('')
const renamingConversation = ref(false)
const conversationNameDraft = ref('')
const composer = ref<HTMLTextAreaElement | null>(null)
const attachmentInput = ref<HTMLInputElement | null>(null)
const imageReferenceInput = ref<HTMLInputElement | null>(null)
const transcript = ref<HTMLElement | null>(null)
const isGenerating = ref(false)
const isHydratingKeyState = ref(false)
const streamingMessageId = ref<string | null>(null)
const editingMessageId = ref<string | null>(null)
const editingContent = ref('')
const downloadingImageIds = ref<Set<string>>(new Set())
const failedImageIds = ref<Set<string>>(new Set())
const generationImageCount = ref(1)
const generationElapsedSeconds = ref(0)
const generatedImageCount = ref(0)
const pendingAttachments = ref<PlaygroundAttachment[]>([])
const selectionMode = ref(false)
const selectedConversationIds = ref<Set<string>>(new Set())

let keyAbortController: AbortController | null = null
let modelsAbortController: AbortController | null = null
let completionAbortController: AbortController | null = null
let generationElapsedTimer: number | null = null
let keyStateHydrationVersion = 0
let userStateHydrated = false
const persistSource = Math.random().toString(36).slice(2)
const playgroundStateUpdatedEvent = 'sub2api:playground-state-updated'
const imageObjectUrls = new Set<string>()
const persistScheduler = createPlaygroundPersistScheduler(500, async (userId, keyId, state) => {
  const saved = savePlaygroundState(userId, keyId, state)
  try {
    await savePlaygroundHistory(userId, saved)
  } catch {
    // Local state remains available while a history request is temporarily unavailable.
  }
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(playgroundStateUpdatedEvent, {
      detail: { userId, source: persistSource },
    }))
  }
  return saved
})

function handleBackgroundStateUpdate(event: Event): void {
  const detail = (event as CustomEvent<{ userId?: number; source?: string }>).detail
  if (detail?.source === persistSource || detail?.userId !== userId.value || isGenerating.value) return
  void hydrateUserState(selectedKeyId.value ?? 0, true)
}

function selectedKeyStorageKey(userId: number): string {
  return `sub2api.playground.v2.user.${userId}.selected-key.${props.mode}`
}

function legacySelectedKeyStorageKey(userId: number): string {
  return `sub2api.playground.v2.user.${userId}.selected-key`
}

function selectedModelStorageKey(userId: number, keyId: number): string {
  return `sub2api.playground.v2.user.${userId}.selected-model.${props.mode}.${keyId}`
}

function loadRememberedKeyId(): number | null {
  if (userId.value === null) return null
  const value = Number(
    localStorage.getItem(selectedKeyStorageKey(userId.value))
      ?? localStorage.getItem(legacySelectedKeyStorageKey(userId.value)),
  )
  return Number.isInteger(value) && value > 0 ? value : null
}

function rememberSelectedKey(): void {
  if (userId.value === null || selectedKeyId.value === null) return
  localStorage.setItem(selectedKeyStorageKey(userId.value), String(selectedKeyId.value))
}

function loadRememberedModel(keyId: number): string {
  if (userId.value === null) return ''
  return localStorage.getItem(selectedModelStorageKey(userId.value, keyId))?.trim() ?? ''
}

function rememberSelectedModel(keyId = selectedKeyId.value): void {
  if (userId.value === null || keyId === null || !selectedModel.value.trim()) return
  localStorage.setItem(selectedModelStorageKey(userId.value, keyId), selectedModel.value.trim())
}

const selectedKey = computed(() => playgroundKeys.value.find((key) => key.id === selectedKeyId.value) ?? null)

function playgroundKeyLabel(key: (typeof playgroundKeys.value)[number]): string {
  if (!key.autoGroup) return formatPlaygroundKeyLabel(key)
  const strategy = key.autoGroupStrategy === 'speed'
    ? t('playground.autoGroupStrategySpeed')
    : key.autoGroupStrategy === 'balanced'
      ? t('playground.autoGroupStrategyBalanced')
      : t('playground.autoGroupStrategyPrice')
  return formatPlaygroundKeyLabel(key, t('playground.autoGroupKey', { strategy }))
}

const isImageMode = computed(() => props.mode === 'image')
const workspaceTitle = computed(() => t(isImageMode.value ? 'playground.imageWorkspace' : 'playground.chatWorkspace'))
const workspaceDescription = computed(() => t(isImageMode.value ? 'playground.imageWorkspaceDescription' : 'playground.chatWorkspaceDescription'))
const composerPlaceholder = computed(() => t(isImageMode.value ? 'playground.imagePromptPlaceholder' : 'playground.chatPromptPlaceholder'))
const actionLabel = computed(() => t(isImageMode.value ? 'playground.generate' : 'playground.send'))
const progressLabel = computed(() => t(isImageMode.value ? 'playground.generating' : 'playground.sending'))
const generationProgressLabel = computed(() => {
  if (!isImageMode.value) return progressLabel.value
  if (generatedImageCount.value > 0) {
    return t('playground.imageGenerationPartialProgress', {
      completed: generatedImageCount.value,
      count: generationImageCount.value,
      seconds: generationElapsedSeconds.value,
    })
  }
  if (generationElapsedSeconds.value >= 15) {
    return t('playground.imageGenerationProgress', {
      count: generationImageCount.value,
      seconds: generationElapsedSeconds.value,
    })
  }
  return t('playground.imageGenerationStarted', { count: generationImageCount.value })
})
const newConversationLabel = computed(() => t(isImageMode.value ? 'playground.newImageTask' : 'playground.newChat'))
const modeConversations = computed(() => conversations.value.filter((conversation) => conversation.mode === props.mode))
const activeConversation = computed(() => modeConversations.value.find((conversation) => conversation.id === activeConversationId.value) ?? null)
const messages = computed<PlaygroundMessage[]>({
  get: () => activeConversation.value?.messages ?? [],
  set: (value) => {
    const conversation = activeConversation.value
    if (!conversation) return
    conversation.messages = value
    touchConversation(conversation)
  },
})
const draftContent = computed<string>({
  get: () => activeConversation.value?.draftContent ?? '',
  set: (value) => {
    const conversation = activeConversation.value
    if (!conversation) return
    conversation.draftContent = value.slice(0, PLAYGROUND_MAX_DRAFT_CHARS)
  },
})
const systemPrompt = computed<string>({
  get: () => activeConversation.value?.systemPrompt ?? '',
  set: (value) => {
    const conversation = activeConversation.value
    if (!conversation) return
    conversation.systemPrompt = value.slice(0, PLAYGROUND_MAX_SYSTEM_PROMPT_CHARS)
    touchConversation(conversation)
  },
})
const customImageSizeValid = computed(() => {
  if (imageSize.value !== 'custom') return true
  const width = imageCustomWidth.value
  const height = imageCustomHeight.value
  return Number.isInteger(width)
    && Number.isInteger(height)
    && width >= 16
    && width <= 3840
    && height >= 16
    && height <= 3840
    && width % 16 === 0
    && height % 16 === 0
})
const customImageSizeSummary = computed(() => t('playground.imageCustomSizeSummary', {
  width: imageCustomWidth.value,
  height: imageCustomHeight.value,
  megapixels: ((imageCustomWidth.value * imageCustomHeight.value) / 1_000_000).toFixed(2),
}))
const resolvedImageSize = computed(() => imageSize.value === 'custom'
  ? `${imageCustomWidth.value}x${imageCustomHeight.value}`
  : imageSize.value)
const imageSettingsSummary = computed(() => t('playground.imageSettingsSummary', {
  size: resolvedImageSize.value,
  quality: imageQuality.value,
  count: imageCount.value,
}))
const supportsGptImageOptions = computed(() => /^gpt-image-/i.test(selectedModel.value))
const isDallE3Model = computed(() => /^dall-e-3$/i.test(selectedModel.value))
const canSend = computed(() => Boolean(
  !isGenerating.value
  && selectedKeyId.value
  && selectedModel.value
  && (!isImageMode.value || customImageSizeValid.value)
  && (isImageMode.value ? draftContent.value.trim() : (draftContent.value.trim() || pendingAttachments.value.length)),
))
const conversationAttachmentCount = computed(() => (
  (activeConversation.value?.messages ?? []).reduce((total, message) => total + (message.attachments?.length ?? 0), 0)
  + pendingAttachments.value.length
))
const enabledParameterCount = computed(() => Object.values(parameters.value.enabled).filter(Boolean).length)
const activeProjectName = computed(() => {
  const projectId = activeConversation.value?.projectId
  return projects.value.find((project) => project.id === projectId)?.name ?? t('playground.ungrouped')
})
const starterPrompts = computed(() => [
  ...(isImageMode.value
    ? [
      { key: 'image-product', icon: 'chart' as const, text: t('playground.starterImageProduct') },
      { key: 'image-landscape', icon: 'document' as const, text: t('playground.starterImageLandscape') },
      { key: 'image-city', icon: 'terminal' as const, text: t('playground.starterImageCity') },
      { key: 'image-illustration', icon: 'lightbulb' as const, text: t('playground.starterImageIllustration') },
    ]
    : [
      { key: 'chat-analyze', icon: 'chart' as const, text: t('playground.starterChatAnalyze') },
      { key: 'chat-summarize', icon: 'document' as const, text: t('playground.starterChatSummarize') },
      { key: 'chat-code', icon: 'terminal' as const, text: t('playground.starterChatCode') },
      { key: 'chat-advice', icon: 'lightbulb' as const, text: t('playground.starterChatAdvice') },
    ]),
])
const conversationGroups = computed<ConversationGroup[]>(() => {
  const sortedConversations = (projectId: string | null) => modeConversations.value
    .filter((conversation) => conversation.projectId === projectId)
    .sort((left, right) => right.updatedAt - left.updatedAt)
  const projectGroups: ConversationGroup[] = [...projects.value]
    .sort((left, right) => right.updatedAt - left.updatedAt)
    .map((project) => ({
      id: project.id,
      label: project.name,
      project,
      conversations: sortedConversations(project.id),
    }))
  const ungroupedConversations = sortedConversations(null)
  if (ungroupedConversations.length > 0 || projectGroups.length === 0) {
    projectGroups.push({
      id: 'ungrouped',
      label: t('playground.ungrouped'),
      project: null,
      conversations: ungroupedConversations,
    })
  }
  return projectGroups
})

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
    || (typeof error === 'object' && error !== null && 'name' in error && (error as { name?: string }).name === 'AbortError')
}

function roleLabel(role: PlaygroundRole): string {
  return t(`playground.roles.${role}`)
}

function messageSurfaceClass(role: PlaygroundRole): string {
  if (role === 'system') return 'rounded-lg border border-amber-200 bg-amber-50/70 px-4 py-3 text-sm leading-7 text-amber-950 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-100'
  if (role === 'user') return 'rounded-2xl bg-gray-100 px-4 py-3 text-sm leading-7 text-gray-800 dark:bg-dark-800 dark:text-gray-100'
  return 'rounded-lg px-0 py-1 text-sm leading-7 text-gray-800 dark:text-gray-100'
}

function renderMarkdown(content: string): string {
  return renderPlaygroundMarkdown(content)
}

function formatTime(timestamp: number): string {
  return new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(new Date(timestamp))
}

function conversationTitle(conversation: PlaygroundConversation): string {
  return conversation.title || newConversationLabel.value
}

function conversationPreview(conversation: PlaygroundConversation): string {
  const lastMessage = [...conversation.messages].reverse().find((message) => message.content.trim())
  if (lastMessage) return lastMessage.content.replace(/\s+/g, ' ')
  if (conversation.systemPrompt) return conversation.systemPrompt.replace(/\s+/g, ' ')
  return t('playground.emptyConversation')
}

function messageError(message: PlaygroundMessage): string | undefined {
  return (message as PlaygroundMessage & { errorMessage?: string }).errorMessage
}

function setMessageError(message: PlaygroundMessage, value: string): void {
  const messageWithError = message as PlaygroundMessage & { errorMessage?: string }
  messageWithError.errorMessage = value
}

function touchConversation(conversation: PlaygroundConversation): void {
  conversation.updatedAt = Date.now()
}

function rememberImageObjectUrl(imageUrl: string): void {
  if (imageUrl.startsWith('blob:')) imageObjectUrls.add(imageUrl)
}

function revokeImageObjectUrls(): void {
  if (typeof URL === 'undefined' || typeof URL.revokeObjectURL !== 'function') return
  for (const imageUrl of imageObjectUrls) URL.revokeObjectURL(imageUrl)
  imageObjectUrls.clear()
}

function conversationConfiguration(): Pick<PlaygroundConversation, 'keyId' | 'model' | 'parameters' | 'imageSize' | 'imageCustomWidth' | 'imageCustomHeight' | 'imageQuality' | 'imageCount' | 'imageOutputFormat' | 'imageOutputCompression' | 'imageBackground' | 'imageModeration' | 'imageInputFidelity' | 'imageStyle'> {
  return {
    keyId: selectedKeyId.value ?? undefined,
    model: selectedModel.value || undefined,
    parameters: cloneParameters(parameters.value),
    imageSize: imageSize.value,
    imageCustomWidth: imageCustomWidth.value,
    imageCustomHeight: imageCustomHeight.value,
    imageQuality: imageQuality.value,
    imageCount: imageCount.value,
    imageOutputFormat: imageOutputFormat.value,
    imageOutputCompression: imageOutputCompression.value,
    imageBackground: imageBackground.value,
    imageModeration: imageModeration.value,
    imageInputFidelity: imageInputFidelity.value,
    imageStyle: imageStyle.value,
  }
}

function saveActiveConversationConfiguration(): void {
  const conversation = activeConversation.value
  if (!conversation || isHydratingKeyState.value) return
  Object.assign(conversation, conversationConfiguration())
}

function restoreConversationConfiguration(conversation: PlaygroundConversation | null): void {
  if (!conversation) return
  if (conversation.model) selectedModel.value = conversation.model
  if (conversation.parameters) parameters.value = cloneParameters(conversation.parameters)
  if (conversation.imageSize) imageSize.value = conversation.imageSize
  if (conversation.imageCustomWidth) imageCustomWidth.value = conversation.imageCustomWidth
  if (conversation.imageCustomHeight) imageCustomHeight.value = conversation.imageCustomHeight
  if (conversation.imageQuality) imageQuality.value = conversation.imageQuality
  if (conversation.imageCount) imageCount.value = conversation.imageCount
  if (conversation.imageOutputFormat) imageOutputFormat.value = conversation.imageOutputFormat
  if (conversation.imageOutputCompression !== undefined) imageOutputCompression.value = conversation.imageOutputCompression
  if (conversation.imageBackground) imageBackground.value = conversation.imageBackground
  if (conversation.imageModeration) imageModeration.value = conversation.imageModeration
  if (conversation.imageInputFidelity) imageInputFidelity.value = conversation.imageInputFidelity
  if (conversation.imageStyle) imageStyle.value = conversation.imageStyle
}

function ensureActiveConversation(projectId: string | null = null): PlaygroundConversation {
  const current = activeConversation.value
  if (current) return current

  const conversation = createConversation({
    mode: props.mode,
    projectId,
    ...conversationConfiguration(),
  })
  conversations.value = [conversation, ...conversations.value]
  activeConversationId.value = conversation.id
  return conversation
}

function isEmptyConversation(conversation: PlaygroundConversation): boolean {
  return conversation.messages.length === 0 && !conversation.draftContent.trim() && !conversation.systemPrompt.trim()
}

function newConversation(projectId = activeConversation.value?.projectId ?? null): void {
  if (isGenerating.value) return
  pendingAttachments.value = []
  const current = activeConversation.value
  if (current && isEmptyConversation(current)) {
    current.projectId = projectId
    touchConversation(current)
  } else {
    const conversation = createConversation({
      mode: props.mode,
      projectId,
      ...conversationConfiguration(),
    })
    conversations.value = [conversation, ...conversations.value]
    activeConversationId.value = conversation.id
  }
  historyOpen.value = false
  editingMessageId.value = null
  cancelConversationRename()
  void nextTick(() => composer.value?.focus())
}

function selectConversation(conversationId: string): void {
  if (isGenerating.value || !modeConversations.value.some((conversation) => conversation.id === conversationId)) return
  pendingAttachments.value = []
  activeConversationId.value = conversationId
  restoreConversationConfiguration(modeConversations.value.find((conversation) => conversation.id === conversationId) ?? null)
  historyOpen.value = false
  editingMessageId.value = null
  parameterPanelOpen.value = false
  imageSettingsOpen.value = false
  conversationSettingsOpen.value = false
  cancelConversationRename()
  void nextTick(() => transcript.value?.scrollTo({ top: transcript.value.scrollHeight }))
}

function toggleSelectionMode(): void {
  if (isGenerating.value) return
  selectionMode.value = !selectionMode.value
  selectedConversationIds.value = new Set()
}

function toggleConversationSelection(conversationId: string): void {
  if (!selectionMode.value || isGenerating.value) return
  const next = new Set(selectedConversationIds.value)
  if (next.has(conversationId)) next.delete(conversationId)
  else next.add(conversationId)
  selectedConversationIds.value = next
}

function selectAllModeConversations(): void {
  selectedConversationIds.value = new Set(modeConversations.value.map((conversation) => conversation.id))
}

function clearConversationSelection(): void {
  selectedConversationIds.value = new Set()
}

function deleteSelectedConversations(): void {
  const selectedIds = new Set(selectedConversationIds.value)
  if (isGenerating.value || selectedIds.size === 0) return
  if (!window.confirm(t('playground.deleteConversationsConfirm', { count: selectedIds.size }))) return
  const deletingActive = activeConversationId.value !== null && selectedIds.has(activeConversationId.value)
  conversations.value = conversations.value.filter((conversation) => !selectedIds.has(conversation.id))
  if (deletingActive) {
    activeConversationId.value = modeConversations.value[0]?.id ?? null
    ensureActiveConversation()
  }
  clearConversationSelection()
  selectionMode.value = false
  cancelConversationRename()
}

function startCreateProject(): void {
  if (isGenerating.value) return
  creatingProject.value = true
  editingProjectId.value = null
  projectNameDraft.value = ''
}

function createProjectFromDraft(): void {
  const name = normalizeProjectName(projectNameDraft.value)
  if (!name) {
    appStore.showInfo(t('playground.projectNameRequired'))
    return
  }
  const project = createProject(name)
  projects.value = [project, ...projects.value]
  expandedGroupIds.value = [...new Set([...expandedGroupIds.value, project.id])]
  creatingProject.value = false
  projectNameDraft.value = ''
  newConversation(project.id)
}

function startRenameProject(project: PlaygroundProject): void {
  if (isGenerating.value) return
  creatingProject.value = false
  editingProjectId.value = project.id
  projectNameDraft.value = project.name
}

function saveProjectName(projectId: string): void {
  const name = normalizeProjectName(projectNameDraft.value)
  if (!name) {
    appStore.showInfo(t('playground.projectNameRequired'))
    return
  }
  const project = projects.value.find((item) => item.id === projectId)
  if (project) {
    project.name = name
    project.updatedAt = Date.now()
  }
  cancelProjectEdit()
}

function cancelProjectEdit(): void {
  creatingProject.value = false
  editingProjectId.value = null
  projectNameDraft.value = ''
}

function deleteProject(projectId: string): void {
  if (isGenerating.value || !window.confirm(t('playground.deleteProjectConfirm'))) return
  projects.value = projects.value.filter((project) => project.id !== projectId)
  for (const conversation of conversations.value) {
    if (conversation.projectId === projectId) {
      conversation.projectId = null
      touchConversation(conversation)
    }
  }
  expandedGroupIds.value = expandedGroupIds.value.filter((id) => id !== projectId)
  cancelProjectEdit()
}

function startRenameConversation(conversation: PlaygroundConversation | null): void {
  if (!conversation || isGenerating.value) return
  renamingConversation.value = true
  conversationNameDraft.value = conversationTitle(conversation)
}

function saveConversationName(): void {
  const conversation = activeConversation.value
  const title = deriveConversationTitle(conversationNameDraft.value)
  if (!conversation || !title) {
    appStore.showInfo(t('playground.conversationNameRequired'))
    return
  }
  conversation.title = title
  touchConversation(conversation)
  cancelConversationRename()
}

function cancelConversationRename(): void {
  renamingConversation.value = false
  conversationNameDraft.value = ''
}

function deleteConversation(conversationId: string): void {
  if (isGenerating.value || !window.confirm(t('playground.deleteConversationConfirm'))) return
  const deletingActive = activeConversationId.value === conversationId
  conversations.value = conversations.value.filter((conversation) => conversation.id !== conversationId)
  if (deletingActive) {
    activeConversationId.value = modeConversations.value[0]?.id ?? null
    ensureActiveConversation()
  }
  cancelConversationRename()
}

function isGroupExpanded(groupId: string): boolean {
  return groupId === 'ungrouped' || expandedGroupIds.value.includes(groupId)
}

function toggleGroup(groupId: string): void {
  if (groupId === 'ungrouped') return
  expandedGroupIds.value = isGroupExpanded(groupId)
    ? expandedGroupIds.value.filter((id) => id !== groupId)
    : [...expandedGroupIds.value, groupId]
}

function parameterValue(key: ParameterKey): number | null {
  return parameters.value[key]
}

function parameterDisplayValue(key: ParameterKey): string {
  const value = parameterValue(key)
  if (value === null) return ''
  if (key === 'max_tokens' || key === 'seed') return String(Math.trunc(value))
  return Number.isInteger(value) ? String(value) : value.toFixed(key === 'temperature' || key === 'top_p' ? 2 : 1)
}

function setParameterEnabled(key: ParameterKey, enabled: boolean): void {
  parameters.value.enabled[key] = enabled
}

function setParameterValue(key: ParameterKey, event: Event): void {
  const value = Number((event.target as HTMLInputElement).value)
  if (key === 'max_tokens') {
    parameters.value.max_tokens = Number.isFinite(value) && value > 0 ? Math.trunc(value) : null
    return
  }
  if (key === 'seed') {
    parameters.value.seed = Number.isFinite(value) ? Math.trunc(value) : null
    return
  }
  if (Number.isFinite(value)) parameters.value[key] = value
}

async function hydrateUserState(keyId: number, force = false): Promise<void> {
  if (userId.value === null) return
  void keyId
  if (userStateHydrated && !force) return
  const hydrationVersion = ++keyStateHydrationVersion
  isHydratingKeyState.value = true
  try {
    let saved = loadPlaygroundState(userId.value, keyId)
    try {
      const remoteState = await fetchPlaygroundHistory(userId.value)
      if (remoteState) {
        // A request can finish after this component unmounts. The local
        // snapshot may therefore be newer than the server response observed
        // by a newly mounted page; never let that response erase the result.
        saved = savePlaygroundState(userId.value, keyId, mergePlaygroundStates([saved, remoteState]))
      }
    } catch {
      // A local snapshot is the offline and rolling-deployment fallback.
    }
    await restoreCachedImages(saved)
    if (hydrationVersion !== keyStateHydrationVersion) return
    revokeImageObjectUrls()
    for (const conversation of saved.conversations) {
      for (const message of conversation.messages) {
        for (const imageUrl of message.imageUrls ?? []) rememberImageObjectUrl(imageUrl)
      }
    }
    applyHydratedState(saved)
    userStateHydrated = true
  } finally {
    if (hydrationVersion === keyStateHydrationVersion) isHydratingKeyState.value = false
  }
}

async function restoreCachedImages(saved: ReturnType<typeof loadPlaygroundState>): Promise<void> {
  for (const conversation of saved.conversations) {
    for (const message of conversation.messages) {
      const cacheKeys = message.imageCacheKeys ?? []
      if (!cacheKeys.length) continue
      const cachedUrls = await Promise.all(cacheKeys.map((cacheKey) => restoreCachedPlaygroundImage(cacheKey).catch(() => null)))
      const remoteUrls = (message.imageUrls ?? []).filter((imageUrl) => /^https?:\/\//i.test(imageUrl))
      const restoredUrls = cachedUrls.filter((imageUrl): imageUrl is string => imageUrl !== null)
      message.imageUrls = [...restoredUrls, ...remoteUrls]
    }
  }
}

function applyHydratedState(saved: ReturnType<typeof loadPlaygroundState>): void {
  selectedModel.value = saved.model
  parameters.value = saved.parameters
  projects.value = saved.projects
  conversations.value = saved.conversations
  const modeState = saved.conversations.filter((conversation) => conversation.mode === props.mode)
  activeConversationId.value = modeState.some((conversation) => conversation.id === saved.activeConversationId)
    ? saved.activeConversationId
    : modeState[0]?.id ?? null
  expandedGroupIds.value = saved.projects.map((project) => project.id)
  restoreConversationConfiguration(activeConversation.value)
  editingMessageId.value = null
  editingContent.value = ''
  parameterPanelOpen.value = false
  conversationSettingsOpen.value = false
  historyOpen.value = false
  cancelProjectEdit()
  cancelConversationRename()
}

function schedulePersist(): void {
  if (isHydratingKeyState.value || userId.value === null || selectedKeyId.value === null) return
  const state = toPersistedState({
    model: selectedModel.value,
    parameters: parameters.value,
    activeConversationId: activeConversationId.value,
    projects: projects.value,
    conversations: conversations.value,
  })
  persistScheduler.schedule(userId.value, selectedKeyId.value, state)
}

async function loadModels(keyId: number): Promise<void> {
  modelsAbortController?.abort()
  const controller = new AbortController()
  modelsAbortController = controller
  loadingModels.value = true
  models.value = []

  try {
    const availableModels = await fetchPlaygroundModels(keyId, controller.signal)
    if (controller.signal.aborted || selectedKeyId.value !== keyId) return
    models.value = isImageMode.value
      ? normalizePlaygroundImageModels(availableModels)
      : normalizePlaygroundChatModels(availableModels)
    const rememberedModel = loadRememberedModel(keyId)
    const rememberedOrCurrentModel = [rememberedModel, selectedModel.value]
      .find((candidate) => candidate && models.value.some((model) => model.id === candidate)) ?? null
    const administratorDefaultModel = isImageMode.value
      ? appStore.cachedPublicSettings?.playground_default_image_model
      : appStore.cachedPublicSettings?.playground_default_chat_model
    selectedModel.value = resolveSelectedModel(
      models.value,
      rememberedOrCurrentModel ?? resolvePlaygroundDefaultModel(
        models.value,
        isImageMode.value ? 'image' : 'chat',
        administratorDefaultModel,
      ),
    )
    rememberSelectedModel(keyId)
  } catch (error) {
    if (!isAbortError(error)) {
      appStore.showError(error instanceof Error ? error.message : t('playground.modelsLoadFailed'))
    }
  } finally {
    if (modelsAbortController === controller) {
      modelsAbortController = null
      loadingModels.value = false
    }
  }
}

async function loadKeys(): Promise<void> {
  keyAbortController?.abort()
  const controller = new AbortController()
  keyAbortController = controller
  loadingKeys.value = true

  try {
    // Repair users whose signup happened before an eligible group was ready.
    // Listing remains the compatible fallback during a rolling deployment.
    try {
      await keysAPI.ensurePlayground()
    } catch {
      // Existing keys can still be used when the repair endpoint is unavailable.
    }
    const allKeys: ApiKey[] = []
    let page = 1
    let pages = 1
    do {
      const response = await keysAPI.list(page, 100, undefined, { signal: controller.signal })
      allKeys.push(...response.items)
      pages = response.pages
      page += 1
    } while (page <= pages && !controller.signal.aborted)

    if (controller.signal.aborted) return
    playgroundKeys.value = normalizePlaygroundKeys(allKeys)
    const rememberedKeyId = selectedKeyId.value ?? loadRememberedKeyId()
    const defaultKeyId = resolvePlaygroundDefaultKeyId(playgroundKeys.value, props.mode)
    const nextKeyId = resolveSelectedKeyId(playgroundKeys.value, rememberedKeyId ?? defaultKeyId)
    if (nextKeyId === null) {
      appStore.showInfo(t('playground.noValidKeyRedirect'))
      await router.replace('/keys')
      return
    }
    selectedKeyId.value = nextKeyId
  } catch (error) {
    if (!isAbortError(error)) {
      appStore.showError(error instanceof Error ? error.message : t('playground.keysLoadFailed'))
    }
  } finally {
    if (keyAbortController === controller) {
      keyAbortController = null
      loadingKeys.value = false
    }
  }
}

function discardEmptyStreamingMessage(conversationId: string, messageId: string | null): void {
  if (!messageId) return
  const conversation = conversations.value.find((item) => item.id === conversationId)
  if (!conversation) return
  conversation.messages = conversation.messages.filter((message) => message.id !== messageId || message.content || message.imageUrls?.length || message.reasoningContent || messageError(message))
  touchConversation(conversation)
}

function stopCompletion(): void {
  completionAbortController?.abort()
}

function startGenerationTimer(imageCount: number): void {
  generationImageCount.value = imageCount
  generationElapsedSeconds.value = 0
  generatedImageCount.value = 0
  generationElapsedTimer && window.clearInterval(generationElapsedTimer)
  generationElapsedTimer = window.setInterval(() => {
    generationElapsedSeconds.value += 1
  }, 1000)
}

function stopGenerationTimer(): void {
  if (generationElapsedTimer) window.clearInterval(generationElapsedTimer)
  generationElapsedTimer = null
  generationElapsedSeconds.value = 0
  generatedImageCount.value = 0
}

async function runCompletion(conversationId: string, baseMessages: PlaygroundMessage[]): Promise<void> {
  if (!selectedKeyId.value || !selectedModel.value || isGenerating.value) return
  const conversation = conversations.value.find((item) => item.id === conversationId)
  if (!conversation) return

  const controller = new AbortController()
  completionAbortController = controller
  isGenerating.value = true
  const completionMode = props.mode
  const assistantMessage = createMessage('assistant', '')
  streamingMessageId.value = assistantMessage.id
  conversation.messages = [...baseMessages, assistantMessage]
  touchConversation(conversation)

  try {
    const message = conversation.messages.find((item) => item.id === assistantMessage.id)
    if (!message) return

    if (isImageMode.value) {
      const imageRequest = [...baseMessages].reverse().find((item) => item.role === 'user')
      const prompt = imageRequest?.content.trim()
      if (!prompt) throw new Error(t('playground.emptySendHint'))
      const referenceImages = (imageRequest?.attachments ?? []).filter((attachment) => (
        attachment.mimeType.startsWith('image/') && Boolean(attachment.dataUrl)
      ))
      const imageGenerationRequest = buildPlaygroundImageRequest({
        model: selectedModel.value,
        prompt,
        size: resolvedImageSize.value,
        quality: imageQuality.value,
        imageCount: imageCount.value,
        outputFormat: imageOutputFormat.value,
        outputCompression: imageOutputCompression.value,
        background: imageBackground.value,
        moderation: imageModeration.value,
        style: imageStyle.value,
        inputFidelity: imageInputFidelity.value,
        hasReferenceImages: referenceImages.length > 0,
      })
      const { body, requestedImageCount } = imageGenerationRequest
      startGenerationTimer(requestedImageCount)
      const imageUrls: string[] = []
      const imageCacheKeys: string[] = []
      const failures: string[] = []
      for (let index = 0; index < requestedImageCount; index += 1) {
        try {
          const result = await sendPlaygroundImageGeneration(selectedKeyId.value, body, {
            signal: controller.signal,
            referenceImages,
          })
          if (controller.signal.aborted) return
          for (const imageResult of result.data) {
            const sourceUrl = playgroundImageUrl(imageResult, imageOutputFormat.value)
            if (!sourceUrl) continue

            let imageUrl = sourceUrl
            const currentUserId = userId.value
            const currentKeyId = selectedKeyId.value
            if (currentUserId !== null && currentKeyId !== null) {
              const cacheKey = createPlaygroundImageCacheKey(
                currentUserId,
                currentKeyId,
                message.id,
                imageUrls.length,
              )
              try {
                const cachedUrl = await cachePlaygroundImage(cacheKey, currentUserId, currentKeyId, sourceUrl, {
                  prompt,
                  model: selectedModel.value,
                  size: resolvedImageSize.value,
                  quality: imageQuality.value,
                  outputFormat: imageOutputFormat.value,
                  sourceImageCount: referenceImages.length,
                })
                if (cachedUrl) {
                  imageUrl = cachedUrl
                  imageCacheKeys.push(cacheKey)
                  rememberImageObjectUrl(cachedUrl)
                }
              } catch {
                // The image remains visible for the current page even when the browser blocks IndexedDB.
              }
            }
            imageUrls.push(imageUrl)
          }
          message.imageUrls = imageUrls
          message.imageCacheKeys = imageCacheKeys.length ? imageCacheKeys : undefined
          message.imageOutputFormat = imageOutputFormat.value
          message.revisedPrompt = result.data.find((item) => item.revised_prompt?.trim())?.revised_prompt?.trim() ?? message.revisedPrompt
          generatedImageCount.value = Math.min(requestedImageCount, imageUrls.length)
          touchConversation(conversation)
        } catch (error) {
          if (isAbortError(error)) throw error
          failures.push(error instanceof Error ? error.message : t('playground.sendFailed'))
        }
      }
      if (imageUrls.length === 0) {
        throw new Error(failures[0] ?? t('playground.imageGenerationNoResults'))
      }
      message.content = t('playground.generatedImage')
      if (imageUrls.length < requestedImageCount) {
        setMessageError(message, t('playground.imageGenerationIncomplete', {
          completed: imageUrls.length,
          count: requestedImageCount,
        }))
      }
      touchConversation(conversation)
      return
    }

    const result = await sendPlaygroundChat(
      selectedKeyId.value,
      {
        ...buildChatPayload({
        model: selectedModel.value,
        messages: withSystemPrompt(baseMessages, conversation.systemPrompt),
        parameters: parameters.value,
        }),
      },
      {
        signal: controller.signal,
        onDelta: (delta) => {
          message.content += delta.contentDelta
          message.reasoningContent = `${message.reasoningContent ?? ''}${delta.reasoningDelta}`
          touchConversation(conversation)
        },
      },
    )
    if (controller.signal.aborted) return
    message.content = result.content
    message.reasoningContent = result.reasoningContent || undefined
    touchConversation(conversation)
  } catch (error) {
    if (!isAbortError(error)) {
      const message = conversation.messages.find((item) => item.id === assistantMessage.id)
      if (message) {
        setMessageError(message, error instanceof Error ? error.message : t('playground.sendFailed'))
        touchConversation(conversation)
      }
      appStore.showError(error instanceof Error ? error.message : t('playground.sendFailed'))
    }
  } finally {
    stopGenerationTimer()
    if (controller.signal.aborted && completionMode === 'image') {
      const message = conversation.messages.find((item) => item.id === assistantMessage.id)
      if (message) {
        message.content = t('playground.imageGenerationStopped')
        touchConversation(conversation)
      }
    } else if (controller.signal.aborted) {
      discardEmptyStreamingMessage(conversationId, assistantMessage.id)
    }
    if (completionAbortController === controller) {
      completionAbortController = null
      isGenerating.value = false
      streamingMessageId.value = null
      await nextTick()
      schedulePersist()
    }
  }
}

async function send(): Promise<void> {
  if (!selectedKeyId.value || !selectedModel.value || isGenerating.value) return
  const conversation = ensureActiveConversation()
  const draft = draftContent.value.trim()
  if (!draft && pendingAttachments.value.length === 0) {
    appStore.showInfo(t('playground.emptySendHint'))
    return
  }

  const baseMessages = conversation.messages
    .filter((message) => message.content.trim() || message.reasoningContent?.trim())
    .map((message) => ({ ...message }))
  baseMessages.push(createMessage('user', draft, { attachments: pendingAttachments.value.map((attachment) => ({ ...attachment })) }))
  conversation.messages = baseMessages
  conversation.draftContent = ''
  pendingAttachments.value = []
  if (!conversation.title) {
    conversation.title = deriveConversationTitle(draft) || newConversationLabel.value
  }
  touchConversation(conversation)
  await runCompletion(conversation.id, baseMessages)
}

async function handleAttachmentChange(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  await addAttachments(files)
}

async function handleComposerPaste(event: ClipboardEvent): Promise<void> {
  const files = Array.from(event.clipboardData?.files ?? [])
  if (!files.some((file) => file.type.startsWith('image/'))) return
  await addAttachments(files)
}

async function addAttachments(files: File[]): Promise<void> {
  if (!files.length) return
  const remaining = PLAYGROUND_MAX_ATTACHMENTS - conversationAttachmentCount.value
  if (remaining <= 0) {
    appStore.showError(`一次对话最多上传 ${PLAYGROUND_MAX_ATTACHMENTS} 个文件或图片`)
    return
  }
  const attachments: PlaygroundAttachment[] = []
  for (const file of files.slice(0, remaining)) {
    if (isImageMode.value && !file.type.startsWith('image/')) {
      appStore.showError(t('playground.referenceImagesOnly'))
      continue
    }
    if (file.size > PLAYGROUND_MAX_ATTACHMENT_BYTES) {
      appStore.showError(`${file.name} 超过 8MB，无法上传`)
      continue
    }
    try {
      const dataUrl = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result))
        reader.onerror = () => reject(reader.error ?? new Error('读取文件失败'))
        reader.readAsDataURL(file)
      })
      attachments.push({
        id: `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        name: file.name,
        mimeType: file.type || 'application/octet-stream',
        size: file.size,
        dataUrl,
      })
    } catch {
      appStore.showError(`${file.name} 读取失败`)
    }
  }
  pendingAttachments.value = [...pendingAttachments.value, ...attachments].slice(0, remaining)
}

function removePendingAttachment(id: string): void {
  pendingAttachments.value = pendingAttachments.value.filter((attachment) => attachment.id !== id)
}

function handleComposerKeydown(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    void send()
  }
}

function selectStarterPrompt(prompt: string): void {
  draftContent.value = prompt
  void nextTick(() => composer.value?.focus())
}

function startEdit(message: PlaygroundMessage): void {
  editingMessageId.value = message.id
  editingContent.value = message.content
}

function cancelEdit(): void {
  editingMessageId.value = null
  editingContent.value = ''
}

async function resendEditedMessage(messageId: string): Promise<void> {
  if (isGenerating.value) return
  const conversation = activeConversation.value
  if (!conversation) return
  const baseMessages = rebuildConversationAfterUserEdit(conversation.messages, messageId, editingContent.value)
  if (!baseMessages) {
    appStore.showInfo(t('playground.emptySendHint'))
    return
  }
  conversation.messages = baseMessages
  touchConversation(conversation)
  cancelEdit()
  await runCompletion(conversation.id, baseMessages)
}

async function regenerate(messageId: string): Promise<void> {
  const conversation = activeConversation.value
  if (!conversation || isGenerating.value) return
  const messageIndex = conversation.messages.findIndex((message) => message.id === messageId && message.role === 'assistant')
  if (messageIndex < 0) return
  await runCompletion(conversation.id, conversation.messages.slice(0, messageIndex).map((message) => ({ ...message })))
}

function deleteMessage(messageId: string): void {
  const conversation = activeConversation.value
  if (!conversation || isGenerating.value) return
  conversation.messages = conversation.messages.filter((message) => message.id !== messageId)
  touchConversation(conversation)
  if (editingMessageId.value === messageId) cancelEdit()
}

async function copyMessage(message: PlaygroundMessage): Promise<void> {
  const text = message.reasoningContent
    ? `${message.reasoningContent}\n\n${message.content}`.trim()
    : message.content
  await copyToClipboard(text, t('playground.copied'))
}

function downloadImageId(messageId: string, imageIndex: number): string {
  return `${messageId}-${imageIndex}`
}

function isImageLoadFailed(messageId: string, imageIndex: number): boolean {
  return failedImageIds.value.has(downloadImageId(messageId, imageIndex))
}

function markImageLoadFailed(messageId: string, imageIndex: number): void {
  failedImageIds.value = new Set([...failedImageIds.value, downloadImageId(messageId, imageIndex)])
}

function isDownloadingImage(messageId: string, imageIndex: number): boolean {
  return downloadingImageIds.value.has(downloadImageId(messageId, imageIndex))
}

async function downloadGeneratedImage(message: PlaygroundMessage, imageUrl: string, imageIndex: number): Promise<void> {
  const imageId = downloadImageId(message.id, imageIndex)
  if (downloadingImageIds.value.has(imageId)) return

  downloadingImageIds.value = new Set([...downloadingImageIds.value, imageId])
  const timestamp = new Date().toISOString().replace(/[:.]/g, '-')
  const filename = `sub2api-image-${timestamp}-${imageIndex + 1}.${imageFilenameExtension(imageUrl, message.imageOutputFormat)}`
  try {
    await downloadPlaygroundImage(imageUrl, filename)
  } catch {
    appStore.showError(t('playground.imageDownloadFailed'))
    window.open(imageUrl, '_blank', 'noopener,noreferrer')
  } finally {
    const nextDownloadingIds = new Set(downloadingImageIds.value)
    nextDownloadingIds.delete(imageId)
    downloadingImageIds.value = nextDownloadingIds
  }
}

function clearHistory(): void {
  const conversation = activeConversation.value
  if (!conversation || isGenerating.value) return
  conversation.messages = []
  touchConversation(conversation)
  editingMessageId.value = null
  editingContent.value = ''
}

function resetParameters(): void {
  parameters.value = cloneParameters(DEFAULT_PLAYGROUND_PARAMETERS)
}

function refreshModels(): void {
  if (selectedKeyId.value) void loadModels(selectedKeyId.value)
}

watch(
  selectedKeyId,
  async (keyId, previousKeyId) => {
    if (keyId === null) return
    rememberSelectedKey()
    if (previousKeyId !== undefined && previousKeyId !== keyId) {
      persistScheduler.flush()
      stopCompletion()
    }
    if (!userStateHydrated) void hydrateUserState(keyId)
    await loadModels(keyId)
  },
)

watch(
  [
    selectedModel,
    parameters,
    imageSize,
    imageCustomWidth,
    imageCustomHeight,
    imageQuality,
    imageCount,
    imageOutputFormat,
    imageOutputCompression,
    imageBackground,
    imageModeration,
    imageInputFidelity,
    imageStyle,
  ],
  () => {
    rememberSelectedModel()
    saveActiveConversationConfiguration()
    schedulePersist()
  },
  { deep: true },
)

watch(
  [projects, conversations, activeConversationId],
  () => schedulePersist(),
  { deep: true },
)

onMounted(async () => {
  window.addEventListener(playgroundStateUpdatedEvent, handleBackgroundStateUpdate)
  await appStore.fetchPublicSettings()
  await loadKeys()
})

onBeforeUnmount(() => {
  window.removeEventListener(playgroundStateUpdatedEvent, handleBackgroundStateUpdate)
  keyAbortController?.abort()
  modelsAbortController?.abort()
  stopGenerationTimer()
  revokeImageObjectUrls()
  persistScheduler.flush()
})
</script>

<style scoped>
.playground-markdown :deep(p) {
  margin: 0 0 0.7rem;
}

.playground-markdown :deep(p:last-child) {
  margin-bottom: 0;
}

.playground-markdown :deep(ul),
.playground-markdown :deep(ol) {
  margin: 0.65rem 0;
  padding-left: 1.25rem;
}

.playground-markdown :deep(ul) {
  list-style: disc;
}

.playground-markdown :deep(ol) {
  list-style: decimal;
}

.playground-markdown :deep(li + li) {
  margin-top: 0.25rem;
}

.playground-markdown :deep(pre) {
  margin: 0.8rem 0;
  overflow-x: auto;
  border-radius: 0.5rem;
  background: rgba(15, 23, 42, 0.95);
  padding: 0.85rem 1rem;
  color: #e2e8f0;
}

.playground-markdown :deep(code) {
  border-radius: 0.25rem;
  background: rgba(148, 163, 184, 0.15);
  padding: 0.1rem 0.3rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.88em;
}

.playground-markdown :deep(pre code) {
  background: transparent;
  padding: 0;
}

.playground-markdown :deep(a) {
  color: inherit;
  text-decoration: underline;
  text-underline-offset: 0.2em;
}

.playground-markdown :deep(blockquote) {
  margin: 0.8rem 0;
  border-left: 3px solid rgba(22, 155, 208, 0.5);
  padding-left: 0.85rem;
  color: rgba(100, 116, 139, 0.95);
}
</style>
