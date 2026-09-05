<template>
  <AppLayout>
    <div ref="workspaceRef" class="contribution-page mx-auto max-w-7xl space-y-4 p-4 sm:p-6">
      <section class="card contribution-workspace-nav overflow-hidden">
        <div class="workspace-nav-main">
          <nav class="workspace-primary-tabs" :aria-label="t('accountContributions.workspaceNavigation')">
            <button type="button" :class="activeWorkspace === 'accounts' ? 'workspace-primary-tab--active' : ''" @click="selectWorkspace('accounts')">
              <Icon name="database" size="sm" />
              <span>{{ t('accountContributions.accountWorkspace') }}</span>
              <span class="workspace-count">{{ accounts.length }}</span>
            </button>
            <button type="button" :class="activeWorkspace === 'room' ? 'workspace-primary-tab--active' : ''" @click="selectWorkspace('room')">
              <Icon name="users" size="sm" />
              <span>{{ t('accountContributions.roomWorkspace') }}</span>
              <span v-if="ownRoom" class="workspace-count">{{ ownRoom.accounts.length }}</span>
            </button>
          </nav>

          <div class="workspace-summary">
            <div class="workspace-wallet workspace-wallet--credit"><span>{{ t('accountContributions.walletBalance') }}</span><strong>${{ money(wallet.balance) }}</strong></div>
            <div class="workspace-wallet workspace-wallet--earned"><span>{{ t('accountContributions.walletEarned') }}</span><strong>${{ money(wallet.earned_total) }}</strong></div>
            <div class="workspace-wallet workspace-wallet--spent"><span>{{ t('accountContributions.walletSpent') }}</span><strong>${{ money(wallet.spent_total) }}</strong></div>
            <div class="workspace-wallet workspace-wallet--share-rate" :title="t('accountContributions.shareIncomeHint')"><span>{{ t('accountContributions.shareIncome') }}</span><strong>{{ t('accountContributions.actualIncome', { rate: formatPercent(incomeRates.share_reward_rate_percent) }) }}</strong></div>
            <div class="workspace-wallet workspace-wallet--own-rate" :title="t('accountContributions.ownIncomeHint')"><span>{{ t('accountContributions.ownIncome') }}</span><strong>{{ t('accountContributions.actualIncome', { rate: formatPercent(incomeRates.own_income_rate_percent) }) }}</strong></div>
          </div>
        </div>

        <nav v-if="activeWorkspace === 'accounts'" class="workspace-secondary-tabs" :aria-label="t('accountContributions.accountWorkspace')">
          <button type="button" :class="activeAccountPanel === 'list' ? 'workspace-secondary-tab--active' : ''" @click="selectAccountPanel('list')">
            {{ t('accountContributions.accountListTab') }}
          </button>
          <button type="button" :class="activeAccountPanel === 'add' ? 'workspace-secondary-tab--active' : ''" @click="selectAccountPanel('add')">
            {{ t('accountContributions.addAccountTab') }}<span v-if="hasContributionDraft" class="draft-dot" :title="t('accountContributions.draftSaved')" />
          </button>
          <button type="button" :class="activeAccountPanel === 'proxies' ? 'workspace-secondary-tab--active' : ''" @click="selectAccountPanel('proxies')">
            {{ t('accountContributions.myProxiesTab') }}<span class="workspace-subcount">{{ contributionProxies.length }}</span>
          </button>
        </nav>
        <nav v-else class="workspace-secondary-tabs" :aria-label="t('accountContributions.roomWorkspace')">
          <button type="button" :class="activeRoomPanel === 'settings' ? 'workspace-secondary-tab--active' : ''" @click="selectRoomPanel('settings')">
            {{ t('accountContributions.roomSettingsTab') }}
          </button>
          <button type="button" :disabled="!ownRoom" :class="activeRoomPanel === 'accounts' ? 'workspace-secondary-tab--active' : ''" @click="selectRoomPanel('accounts')">
            {{ t('accountContributions.roomAccountsTab') }}<span v-if="ownRoom" class="workspace-subcount">{{ ownRoom.accounts.length }}</span>
          </button>
        </nav>
      </section>

      <section v-show="activeWorkspace === 'accounts' && activeAccountPanel === 'add'" class="card contribution-card overflow-hidden">
        <div class="contribution-panel-header">
          <div class="flex min-w-0 items-start gap-3"><span class="section-icon section-icon--add"><Icon name="upload" size="md" /></span><div><h2>{{ t('accountContributions.addAccountTab') }}</h2><p>{{ t('accountContributions.addAccountHint') }}</p></div></div>
        </div>

        <div class="contribution-form-zone px-5 pb-5 pt-5 sm:px-6 sm:pb-6">
        <section class="space-y-2">
          <span class="block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.platform') }}</span>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-5">
            <button v-for="platform in contributionPlatformOptions" :key="platform.value" type="button" class="rounded-lg border px-3 py-3 text-sm font-medium transition" :class="platformSelected && form.platform === platform.value ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-950/30 dark:text-primary-200' : 'border-gray-200 text-gray-700 hover:border-primary-300 dark:border-dark-600 dark:text-gray-200'" @click="selectContributionPlatform(platform.value)">
              {{ platform.label }}
            </button>
          </div>
        </section>

        <form v-if="platformSelected" class="mt-5 space-y-4" @submit.prevent="submitContribution">
          <div>
            <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.accountType') }}</span>
            <div class="mode-switch flex gap-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-700">
          <button
            v-for="option in modeOptions"
            :key="option.value"
            class="flex flex-1 items-center justify-center gap-2 rounded-md px-4 py-2.5 text-sm font-medium transition"
            :class="form.mode === option.value ? 'bg-white text-primary-600 shadow-sm dark:bg-dark-600 dark:text-primary-300' : 'text-gray-500 dark:text-gray-400'"
            @click="form.mode = option.value"
          >
            <Icon :name="option.value === 'oauth' ? 'document' : 'key'" size="sm" />
            {{ option.label }}
          </button>
            </div>
        </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <label class="block">
              <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.name') }}</span>
              <input v-model.trim="form.name" class="input" :placeholder="t('accountContributions.namePlaceholder')" />
            </label>
            <div class="rounded-lg border border-teal-100 bg-teal-50/60 px-3 py-2 text-xs leading-5 text-teal-800 dark:border-teal-900/70 dark:bg-teal-950/20 dark:text-teal-200">
              {{ t('accountContributions.schedulingHint') }}
            </div>
          </div>

          <template v-if="form.mode === 'oauth'">
            <section v-if="form.platform === 'openai'" class="rounded-lg border border-indigo-100 bg-indigo-50/50 p-4 dark:border-indigo-900/70 dark:bg-indigo-950/15">
              <div class="flex flex-wrap gap-2" :aria-label="t('accountContributions.openaiAuthorizationMethod')">
                <button v-for="option in openAIContributionMethods" :key="option.value" type="button" class="rounded-md px-3 py-2 text-sm font-medium transition" :class="openAIContributionMethod === option.value ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-200' : 'text-gray-600 hover:bg-white/70 dark:text-gray-300 dark:hover:bg-dark-700/70'" @click="openAIContributionMethod = option.value">
                  {{ option.label }}
                </button>
              </div>

              <div v-if="openAIContributionMethod === 'codex_session'" class="mt-4 space-y-3">
                <label class="flex cursor-pointer items-center justify-between gap-4 rounded-lg border border-dashed border-gray-300 bg-white/80 px-4 py-3 transition-colors hover:border-primary-400 dark:border-dark-600 dark:bg-dark-700/40 dark:hover:border-primary-500">
                  <input ref="oauthFileInput" type="file" accept="application/json,text/plain,.json,.txt" multiple class="hidden" @change="handleOAuthFileChange">
                  <span class="min-w-0"><span class="block truncate text-sm text-gray-700 dark:text-gray-200">{{ oauthFilesLabel || t('accountContributions.oauthFilePlaceholder') }}</span><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.oauthFileHint') }}</span></span>
                  <span class="btn btn-secondary btn-sm pointer-events-none shrink-0">{{ t('accountContributions.oauthFileSelect') }}</span>
                </label>
                <label class="block"><span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.codexSessionText') }}</span><textarea v-model.trim="codexSessionInput" rows="3" class="input w-full font-mono text-sm" :placeholder="t('accountContributions.codexSessionPlaceholder')" /></label>
              </div>

              <div v-else-if="openAIContributionMethod === 'manual'" class="mt-4 space-y-3">
                <div class="flex flex-wrap gap-3"><button type="button" class="btn btn-secondary" :disabled="openAIAuthLoading" @click="generateOpenAIAuthURL"><Icon name="link" size="sm" />{{ t('accountContributions.generateOpenAIAuthURL') }}</button><a v-if="openAIAuthURL" :href="openAIAuthURL" target="_blank" rel="noopener noreferrer" class="btn btn-primary"><Icon name="externalLink" size="sm" />{{ t('accountContributions.openOpenAIAuthURL') }}</a></div>
                <label class="block"><span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.openAIAuthCallback') }}</span><textarea v-model.trim="manualAuthorizationInput" rows="3" class="input w-full font-mono text-sm" :placeholder="t('accountContributions.openAIAuthCallbackPlaceholder')" /></label>
              </div>

              <label v-else-if="openAIContributionMethod === 'refresh_token' || openAIContributionMethod === 'mobile_refresh_token'" class="mt-4 block"><span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ openAIContributionMethod === 'mobile_refresh_token' ? t('accountContributions.mobileRefreshToken') : t('accountContributions.refreshToken') }}</span><textarea v-model.trim="refreshTokenInput" rows="4" class="input w-full font-mono text-sm" :placeholder="t('accountContributions.refreshTokenPlaceholder')" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.refreshTokenHint') }}</span></label>

              <label v-else class="mt-4 block"><span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.codexPAT') }}</span><textarea v-model.trim="codexPATInput" rows="3" class="input w-full font-mono text-sm" autocomplete="off" :placeholder="t('accountContributions.codexPATPlaceholder')" /></label>
            </section>

            <section v-else-if="form.platform === 'anthropic'" class="rounded-lg border border-indigo-100 bg-indigo-50/50 p-4 dark:border-indigo-900/70 dark:bg-indigo-950/15">
              <div class="flex flex-wrap gap-3"><button type="button" class="btn btn-secondary" :disabled="anthropicAuthLoading" @click="generateAnthropicAuthURL"><Icon name="link" size="sm" />{{ t('accountContributions.generateAnthropicAuthURL') }}</button><a v-if="anthropicAuthURL" :href="anthropicAuthURL" target="_blank" rel="noopener noreferrer" class="btn btn-primary"><Icon name="externalLink" size="sm" />{{ t('accountContributions.openAnthropicAuthURL') }}</a></div>
              <label class="mt-4 block"><span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.anthropicAuthCode') }}</span><textarea v-model.trim="anthropicAuthorizationInput" rows="3" class="input w-full font-mono text-sm" :placeholder="t('accountContributions.anthropicAuthCodePlaceholder')" /></label>
            </section>
          </template>

          <template v-else>
            <div class="grid gap-4 sm:grid-cols-2">
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.platform') }}</span>
                <select v-model="form.platform" class="input">
                  <option value="anthropic">Anthropic</option>
                  <option value="openai">OpenAI</option>
                  <option value="gemini">Gemini</option>
                  <option value="grok">Grok</option>
                </select>
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.apiKey') }}</span>
                <input v-model.trim="form.apiKey" type="password" autocomplete="off" class="input" required />
              </label>
            </div>
            <label class="block">
              <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.baseURL') }}</span>
              <input v-model.trim="form.baseURL" class="input" :placeholder="t('accountContributions.baseURLPlaceholder')" />
            </label>
          </template>

          <section class="proxy-routing-settings rounded-lg border border-sky-200 bg-sky-50/50 p-4 dark:border-sky-900/70 dark:bg-sky-950/15">
            <div class="flex items-start gap-3">
              <span class="section-icon section-icon--proxy"><Icon name="globe" size="md" /></span>
              <div class="min-w-0 flex-1">
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('accountContributions.proxyConnection') }}</h2>
                <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ t('accountContributions.proxyWhyHint') }}</p>
              </div>
            </div>
            <div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-end">
              <label class="min-w-0 flex-1">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.connectionRoute') }}</span>
                <select v-model.number="form.proxyId" class="input">
                  <option :value="0">{{ t('accountContributions.directConnection') }}</option>
                  <option v-for="proxy in activeContributionProxies" :key="proxy.id" :value="proxy.id">{{ proxy.name }} · {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}</option>
                </select>
                <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ form.proxyId ? t('accountContributions.selectedProxyHint') : t('accountContributions.directConnectionHint') }}</span>
              </label>
              <button type="button" class="btn btn-secondary shrink-0" @click="selectAccountPanel('proxies')"><Icon name="cog" size="sm" />{{ t('accountContributions.manageProxies') }}</button>
            </div>
          </section>

          <label class="block">
            <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.testModel') }}</span>
            <input v-model.trim="form.testModelId" class="input" :placeholder="t('accountContributions.testModelPlaceholder')" />
          </label>

          <section class="scheduling-settings rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('accountContributions.schedulingTitle') }}</h2>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('accountContributions.schedulingDescription') }}</p>
            </div>
            <div class="mt-4">
              <span class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.accountScope') }}</span>
              <div class="inline-flex overflow-hidden rounded-md border border-gray-200 dark:border-dark-600">
                <button type="button" class="scope-option" :class="form.shareScope === 'private' ? 'scope-option--active' : ''" @click="form.shareScope = 'private'; form.poolGroupId = 0">{{ t('accountContributions.privateMode') }}</button>
                <button type="button" class="scope-option" :class="form.shareScope === 'pool' ? 'scope-option--active' : ''" @click="form.shareScope = 'pool'; form.groupIds = []; form.priority = Math.max(form.priority, 20)">{{ t('accountContributions.joinPool') }}</button>
              </div>
            </div>
            <label v-if="form.shareScope === 'pool'" class="mt-4 block">
              <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.poolGroup') }}</span>
              <select v-model.number="form.poolGroupId" class="input" :disabled="groupsLoading">
                <option :value="0">{{ t('accountContributions.poolGroupPlaceholder') }}</option>
                <option v-for="group in formPoolGroups" :key="group.id" :value="group.id">{{ group.name }} ({{ group.platform }}, {{ group.rate_multiplier }}x)</option>
              </select>
              <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('accountContributions.poolGroupHint') }}</span>
              <span v-if="!groupsLoading && formPoolGroups.length === 0" class="mt-1 block text-xs text-amber-700 dark:text-amber-300">{{ t('accountContributions.poolGroupsEmpty') }}</span>
            </label>
            <label v-else class="mt-4 block">
              <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.groups') }}</span>
              <select v-model="form.groupIds" multiple size="4" class="input contribution-group-select" :disabled="groupsLoading">
                <option v-for="group in formGroups" :key="group.id" :value="group.id">{{ group.name }} ({{ group.platform }})</option>
              </select>
              <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('accountContributions.groupsHint') }}</span>
              <span v-if="!groupsLoading && formGroups.length === 0" class="mt-1 block text-xs text-amber-700 dark:text-amber-300">{{ t('accountContributions.groupsEmpty') }}</span>
            </label>
            <div class="mt-4 grid gap-4" :class="form.shareScope === 'pool' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'">
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.concurrency') }}</span>
                <input v-model.number="form.concurrency" type="number" :min="form.shareScope === 'pool' ? 30 : 1" max="1000" class="input" />
                <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ form.shareScope === 'pool' ? t('accountContributions.poolConcurrencyHint') : t('accountContributions.concurrencyHint') }}</span>
              </label>
              <label v-if="form.shareScope === 'pool'" class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.priority') }}</span>
                <input v-model.number="form.priority" type="number" min="20" max="100000" class="input" @change="normalizePoolPriority(form)" />
                <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.poolPriorityHint') }}</span>
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.loadFactor') }}</span>
                <input v-model.number="form.loadFactor" type="number" min="0" max="10000" class="input" :placeholder="t('accountContributions.loadFactorPlaceholder')" />
                <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.loadFactorHint') }}</span>
              </label>
            </div>
          </section>

          <p class="text-xs text-gray-500 dark:text-gray-400">{{ form.shareScope === 'pool' ? t('accountContributions.poolGroupHint') : t('accountContributions.accountPrivateHint') }}</p>

          <div class="flex items-center justify-between gap-4">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.accountVerificationHint') }}</p>
            <button type="submit" class="btn btn-primary shrink-0" :disabled="submitting">
              <Icon name="upload" size="sm" />
              {{ submitting ? t('accountContributions.submitting') : t('accountContributions.submit') }}
            </button>
          </div>
        </form>

        <div v-if="lastResult" class="contribution-result mt-5 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <p class="font-medium text-gray-900 dark:text-white">{{ t('accountContributions.resultSummary', { created: lastResult.created, skipped: lastResult.skipped || 0, failed: lastResult.failed }) }}</p>
          <ul class="mt-2 space-y-1 text-sm text-gray-600 dark:text-gray-300">
            <li v-for="item in lastResult.items" :key="`${item.index}-${item.account_id || 0}`">
              {{ item.name || `#${item.index}` }} - {{ item.status }}<span v-if="item.message">: {{ item.message }}</span>
            </li>
          </ul>
        </div>
        </div>
      </section>

      <section v-show="activeWorkspace === 'room'" class="card contribution-card room-panel p-5 sm:p-6">
          <div class="flex items-start justify-between gap-3">
            <div class="flex min-w-0 items-start gap-3">
              <span class="section-icon section-icon--room"><Icon name="users" size="md" /></span>
              <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('accountContributions.myRoom') }}</h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('accountContributions.myRoomHint') }}</p>
              </div>
            </div>
          </div>

          <div v-if="roomLoading" class="py-8 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
          <form v-else-if="!ownRoom" class="room-create-flow mt-5" @submit.prevent="createRoom">
            <section class="room-create-section">
              <div class="room-create-heading">
                <span>1</span>
                <div><h3>{{ t('accountContributions.roomBasicSettings') }}</h3><p>{{ t('accountContributions.roomBasicSettingsHint') }}</p></div>
              </div>
              <div class="mt-4 grid gap-4 sm:grid-cols-2">
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomName') }}</span>
                  <input v-model.trim="roomForm.name" class="input" :placeholder="t('accountContributions.roomNamePlaceholder')" required maxlength="100" />
                </label>
                <label class="block">
                  <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomMultiplier') }}</span>
                  <input v-model.number="roomForm.consumerRateMultiplier" type="number" min="0.01" max="100" step="0.01" class="input" required />
                </label>
              </div>
            </section>

            <section class="room-create-section">
              <div class="room-create-heading">
                <span>2</span>
                <div><h3>{{ t('accountContributions.roomCreateAccounts') }}</h3><p>{{ t('accountContributions.roomCreateAccountsHint') }}</p></div>
              </div>
              <div v-if="accounts.length" class="room-create-account-list mt-4">
                <label v-for="account in accounts" :key="account.id" class="room-create-account-row" :class="roomCreateSelection[account.id] ? 'room-create-account-row--selected' : ''">
                  <input v-model="roomCreateSelection[account.id]" type="checkbox" class="h-4 w-4 shrink-0 rounded border-gray-300 text-teal-600 focus:ring-teal-500" />
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <strong class="truncate text-sm text-gray-900 dark:text-white">{{ account.name }}</strong>
                      <span class="badge">{{ account.platform }}</span>
                      <span class="text-xs text-gray-500 dark:text-gray-400">{{ account.type }}</span>
                    </div>
                    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.accountMaxConcurrency', { count: account.concurrency }) }} · {{ t('accountContributions.roomCreateAccountCheckHint') }}</p>
                  </div>
                  <div class="room-create-controls" @click.stop>
                    <label>
                      <span>{{ t('accountContributions.roomShareConcurrency') }}</span>
                      <input v-model.number="candidateConcurrencyDraft[account.id]" type="number" min="1" :max="account.concurrency" step="1" class="input" :disabled="!roomCreateSelection[account.id]" :placeholder="t('accountContributions.roomShareConcurrencyPlaceholder', { count: account.concurrency })" @click.stop />
                    </label>
                    <label>
                      <span>{{ t('accountContributions.shareBudget') }}</span>
                      <input v-model.number="candidateBudgetDraft[account.id]" type="number" min="0.000001" max="1000000" step="0.000001" class="input" :disabled="!roomCreateSelection[account.id]" :placeholder="t('accountContributions.shareBudgetPlaceholder')" @click.stop />
                    </label>
                  </div>
                </label>
              </div>
              <div v-else class="room-create-empty mt-4">
                <Icon name="database" size="lg" />
                <p>{{ t('accountContributions.roomCreateNoAccounts') }}</p>
                <button type="button" class="btn btn-secondary btn-sm" @click="openAddAccountForRoom"><Icon name="upload" size="sm" />{{ t('accountContributions.addAccountTab') }}</button>
              </div>
            </section>

            <div class="room-create-submit">
              <p>{{ t('accountContributions.roomCreateSummary', { count: selectedRoomCreateAccounts.length, concurrency: selectedRoomCreateConcurrency, budget: money(selectedRoomCreateBudget) }) }}</p>
              <button type="submit" class="btn btn-primary" :disabled="roomSaving || selectedRoomCreateAccounts.length === 0">
                <Icon name="checkCircle" size="sm" :class="roomSaving ? 'animate-spin' : ''" />
                {{ roomSaving ? t('accountContributions.roomCreatingAndTesting') : t('accountContributions.createRoomWithAccounts') }}
              </button>
            </div>
          </form>

          <template v-else>
            <div class="room-stat-grid mt-5 grid grid-cols-2 gap-3">
              <div><Icon name="checkCircle" size="sm" class="text-emerald-600 dark:text-emerald-400" /><div><div class="text-xs text-gray-500">{{ t('accountContributions.preparedAccounts') }}</div><div class="mt-1 text-lg font-semibold">{{ preparedRoomAccountCount }}</div></div></div>
              <div><Icon name="bolt" size="sm" class="text-amber-600 dark:text-amber-400" /><div><div class="text-xs text-gray-500">{{ t('accountContributions.preparedConcurrency') }}</div><div class="mt-1 text-lg font-semibold">{{ preparedRoomConcurrency }}</div></div></div>
            </div>
            <form v-show="activeRoomPanel === 'settings'" class="room-settings mt-5 grid gap-3 sm:grid-cols-2" @submit.prevent="saveRoom">
              <label class="block sm:col-span-2">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomName') }}</span>
                <input v-model.trim="roomForm.name" class="input" required maxlength="100" />
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomMultiplier') }}</span>
                <input v-model.number="roomForm.consumerRateMultiplier" type="number" min="0.01" max="100" step="0.01" class="input" required />
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomVisibility') }}</span>
                <select v-model="roomForm.visibility" class="input"><option value="public">{{ t('accountContributions.visibilityPublic') }}</option><option value="private">{{ t('accountContributions.visibilityPrivate') }}</option></select>
              </label>
              <label class="block">
                <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomStatus') }}</span>
                <select v-model="roomForm.status" class="input"><option value="active">{{ t('accountContributions.statusActive') }}</option><option value="paused">{{ t('accountContributions.statusPaused') }}</option></select>
              </label>
              <div class="flex items-end justify-end"><button type="submit" class="btn btn-primary" :disabled="roomSaving">{{ roomSaving ? t('common.saving') : t('common.save') }}</button></div>
            </form>

            <div v-show="activeRoomPanel === 'accounts'" class="room-account-workspace">
            <div class="room-subsection mt-4 border-t border-gray-200 pt-5 dark:border-dark-600">
              <div class="subsection-heading">
                <div><h3 class="font-medium text-gray-900 dark:text-white">{{ t('accountContributions.roomAccounts') }}</h3><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.shareBudgetCandidateHint') }}</p></div>
                <span class="count-pill">{{ ownRoom.accounts.length }}</span>
              </div>
              <div v-if="ownRoom.accounts.length" class="mt-3 divide-y divide-gray-200 dark:divide-dark-600">
                <div v-for="account in ownRoom.accounts" :key="account.account_id" class="room-account-row flex flex-wrap items-center justify-between gap-3 py-4">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ account.name }}</p>
                    <p class="mt-1 text-xs text-gray-500">{{ t('accountContributions.accountMaxConcurrency', { count: account.concurrency }) }} · {{ t('accountContributions.currentRoomShareConcurrency', { count: account.share_concurrency }) }}</p>
                    <p class="mt-1 text-xs text-gray-500">{{ t('accountContributions.shareBudgetUsage', { used: money(account.share_used_usd), budget: money(account.share_budget_usd), remaining: money(account.share_remaining_usd) }) }}</p>
                  </div>
                  <div class="flex flex-wrap items-end gap-2">
                    <span :class="healthClass(account)" class="badge">{{ healthLabel(account) }}</span>
                    <label class="block w-32">
                      <span class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('accountContributions.roomShareConcurrency') }}</span>
                      <input v-model.number="roomConcurrencyDraft[account.account_id]" type="number" min="1" :max="account.concurrency" step="1" class="input w-full text-sm" :placeholder="String(account.concurrency)" />
                    </label>
                    <label class="block w-40">
                      <span class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('accountContributions.shareBudget') }}</span>
                      <input v-model.number="roomBudgetDraft[account.account_id]" type="number" min="0" max="1000000" step="0.000001" class="input w-full text-sm" :placeholder="t('accountContributions.shareBudgetPlaceholder')" />
                    </label>
                    <button class="contribution-icon-button contribution-icon-button--compact" :title="t('common.save')" :disabled="busyId === account.account_id" @click="saveRoomAccountSettings(account)"><Icon name="check" size="sm" /></button>
                    <button class="contribution-icon-button contribution-icon-button--compact" :title="account.enabled ? t('accountContributions.disable') : t('accountContributions.enable')" :disabled="busyId === account.account_id" @click="toggleRoomAccount(account)"><Icon :name="account.enabled ? 'ban' : 'checkCircle'" size="sm" /></button>
                    <button class="contribution-icon-button contribution-icon-button--compact" :title="t('accountContributions.test')" :disabled="busyId === account.account_id" @click="testAccountById(account.account_id)"><Icon name="play" size="sm" /></button>
                    <button class="contribution-icon-button contribution-icon-button--danger contribution-icon-button--compact" :title="t('common.delete')" :disabled="busyId === account.account_id" @click="removeRoomAccount(account.account_id)"><Icon name="trash" size="sm" /></button>
                  </div>
                </div>
              </div>
              <p v-else class="mt-3 text-sm text-gray-500">{{ t('accountContributions.roomAccountsEmpty') }}</p>
            </div>
            </div>
          </template>
      </section>

      <section v-show="activeWorkspace === 'accounts' && activeAccountPanel === 'proxies'" class="card contribution-card overflow-hidden">
        <div class="contribution-panel-header">
          <div class="flex min-w-0 items-start gap-3"><span class="section-icon section-icon--proxy"><Icon name="globe" size="md" /></span><div><h2>{{ t('accountContributions.myProxies') }}</h2><p>{{ t('accountContributions.myProxiesHint') }}</p></div></div>
        </div>
        <div class="proxy-management-layout">
          <form class="proxy-editor space-y-4" @submit.prevent="saveContributionProxy">
            <div>
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ proxyEditingId ? t('accountContributions.editProxy') : t('accountContributions.addProxy') }}</h3>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('accountContributions.proxyCredentialHint') }}</p>
            </div>
            <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyName') }}</span><input v-model.trim="proxyForm.name" class="input" :placeholder="t('accountContributions.proxyNamePlaceholder')" /></label>
            <label class="block">
              <span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyAddress') }}</span>
              <div class="flex gap-2">
                <input v-model.trim="proxyAddressInput" class="input min-w-0 flex-1" autocomplete="off" :placeholder="t('accountContributions.proxyAddressPlaceholder')" @input="clearProxyAddressFeedback" />
                <button type="button" class="btn btn-secondary shrink-0" @click="applyProxyAddress(true)"><Icon name="link" size="sm" />{{ t('accountContributions.proxyAddressParse') }}</button>
              </div>
              <span class="mt-1.5 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('accountContributions.proxyAddressHint') }}</span>
              <span v-if="proxyAddressIssue" class="proxy-address-feedback proxy-address-feedback--error"><Icon name="infoCircle" size="xs" />{{ proxyAddressIssueLabel }}</span>
              <span v-else-if="proxyAddressParsedLabel" class="proxy-address-feedback proxy-address-feedback--success"><Icon name="checkCircle" size="xs" />{{ proxyAddressParsedLabel }}</span>
            </label>
            <button type="button" class="proxy-manual-toggle" @click="proxyManualOpen = !proxyManualOpen">
              <span class="inline-flex items-center gap-1.5"><Icon name="cog" size="sm" />{{ t('accountContributions.proxyManualSettings') }}</span>
              <Icon :name="proxyManualOpen ? 'chevronUp' : 'chevronDown'" size="sm" />
            </button>
            <div v-show="proxyManualOpen" class="proxy-manual-fields space-y-3">
              <div class="grid grid-cols-[minmax(0,1fr)_7rem] gap-3">
                <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyProtocol') }}</span><select v-model="proxyForm.protocol" class="input"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option><option value="socks5h">SOCKS5H</option></select></label>
                <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyPort') }}</span><input v-model.number="proxyForm.port" type="number" min="1" max="65535" class="input" /></label>
              </div>
              <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyHost') }}</span><input v-model.trim="proxyForm.host" class="input" :placeholder="t('accountContributions.proxyHostPlaceholder')" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.proxyHostHint') }}</span></label>
              <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-1 xl:grid-cols-2">
                <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyUsername') }}</span><input v-model.trim="proxyForm.username" class="input" autocomplete="off" /></label>
                <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.proxyPassword') }}</span><input v-model="proxyForm.password" type="password" class="input" autocomplete="new-password" :placeholder="proxyEditingId ? t('accountContributions.proxyPasswordKeep') : ''" /></label>
              </div>
            </div>
            <div class="flex justify-end gap-2">
              <button v-if="proxyEditingId" type="button" class="btn btn-secondary" @click="resetProxyForm">{{ t('common.cancel') }}</button>
              <button type="submit" class="btn btn-primary" :disabled="proxySaving"><Icon :name="proxyEditingId ? 'check' : 'plus'" size="sm" />{{ proxySaving ? t('common.loading') : proxyEditingId ? t('common.save') : t('accountContributions.addProxy') }}</button>
            </div>
          </form>

          <div class="proxy-list-zone">
            <div class="mb-3 flex items-center justify-between gap-3"><div><h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('accountContributions.savedProxies') }}</h3><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.savedProxiesHint') }}</p></div><button type="button" class="contribution-icon-button" :title="t('common.refresh')" :disabled="proxiesLoading" @click="loadContributionProxies"><Icon name="refresh" size="sm" :class="proxiesLoading ? 'animate-spin' : ''" /></button></div>
            <div v-if="proxiesLoading" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>
            <div v-else-if="contributionProxies.length === 0" class="proxy-empty-state"><Icon name="globe" size="lg" /><p>{{ t('accountContributions.noProxies') }}</p></div>
            <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
              <div v-for="proxy in contributionProxies" :key="proxy.id" class="proxy-list-row">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2"><p class="truncate font-medium text-gray-900 dark:text-gray-100">{{ proxy.name }}</p><span :class="proxy.status === 'active' ? 'badge badge-success' : 'badge badge-gray'">{{ proxy.status === 'active' ? t('accountContributions.statusActive') : t('accountContributions.statusDisabled') }}</span></div>
                  <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400" :title="proxyEndpoint(proxy)">{{ proxyEndpoint(proxy) }}</p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ proxy.username ? t('accountContributions.proxyAuthConfigured', { username: proxy.username }) : t('accountContributions.proxyNoAuth') }}</p>
                  <p v-if="proxyTestResults[proxy.id]" class="mt-1 text-xs" :class="proxyTestResults[proxy.id]?.success ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-600 dark:text-red-400'">{{ proxyTestResultLabel(proxy.id) }}</p>
                </div>
                <div class="flex shrink-0 items-center gap-1">
                  <button type="button" class="contribution-icon-button contribution-icon-button--compact" :title="t('accountContributions.testProxy')" :disabled="proxyBusyId === proxy.id" @click="testPrivateProxy(proxy)"><Icon name="play" size="sm" /></button>
                  <button type="button" class="contribution-icon-button contribution-icon-button--compact" :title="t('common.edit')" @click="startProxyEdit(proxy)"><Icon name="edit" size="sm" /></button>
                  <button type="button" class="contribution-icon-button contribution-icon-button--compact" :title="proxy.status === 'active' ? t('accountContributions.disable') : t('accountContributions.enable')" :disabled="proxyBusyId === proxy.id" @click="togglePrivateProxy(proxy)"><Icon :name="proxy.status === 'active' ? 'ban' : 'checkCircle'" size="sm" /></button>
                  <button type="button" class="contribution-icon-button contribution-icon-button--danger contribution-icon-button--compact" :title="t('common.delete')" :disabled="proxyBusyId === proxy.id" @click="removePrivateProxy(proxy)"><Icon name="trash" size="sm" /></button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section v-show="activeWorkspace === 'accounts' && activeAccountPanel === 'list'" class="card contribution-card overflow-hidden">
        <div class="contributed-accounts-header border-b border-gray-200 px-5 py-4 dark:border-dark-600 sm:px-6">
          <div class="flex items-start gap-3"><span class="section-icon section-icon--account"><Icon name="database" size="md" /></span><div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('accountContributions.myAccounts') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('accountContributions.myAccountsHint') }}</p></div></div>
        </div>
        <div class="border-b border-gray-200 bg-gray-50/60 px-5 py-3 dark:border-dark-600 dark:bg-dark-800/40 sm:px-6">
          <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
            <input v-model.trim="accountFilters.search" class="input" :placeholder="t('accountContributions.accountSearchPlaceholder')" :aria-label="t('accountContributions.accountSearchPlaceholder')" />
            <select v-model="accountFilters.platform" class="input" :aria-label="t('accountContributions.filterPlatform')">
              <option value="">{{ t('accountContributions.filterPlatform') }}</option>
              <option v-for="platform in accountPlatforms" :key="platform" :value="platform">{{ platform }}</option>
            </select>
            <select v-model="accountFilters.type" class="input" :aria-label="t('accountContributions.filterType')">
              <option value="">{{ t('accountContributions.filterType') }}</option>
              <option v-for="type in accountTypes" :key="type" :value="type">{{ type }}</option>
            </select>
            <select v-model="accountFilters.status" class="input" :aria-label="t('accountContributions.filterStatus')">
              <option value="">{{ t('accountContributions.filterStatus') }}</option>
              <option value="active">{{ t('accountContributions.statusActive') }}</option>
              <option value="disabled">{{ t('accountContributions.statusDisabled') }}</option>
            </select>
            <div class="flex min-w-0 gap-2">
              <select v-model.number="accountFilters.groupId" class="input min-w-0 flex-1" :aria-label="t('accountContributions.filterGroup')">
                <option :value="0">{{ t('accountContributions.filterGroup') }}</option>
                <option v-for="group in accountFilterGroups" :key="group.id" :value="group.id">{{ group.name }}</option>
              </select>
              <button type="button" class="contribution-icon-button shrink-0" :title="t('common.refresh')" :disabled="accountWorkspaceRefreshing" @click="refreshAccountWorkspace"><Icon name="refresh" size="sm" :class="accountWorkspaceRefreshing ? 'animate-spin' : ''" /></button>
              <button v-if="hasAccountFilters" type="button" class="contribution-icon-button shrink-0" :title="t('accountContributions.clearFilters')" @click="clearAccountFilters"><Icon name="x" size="sm" /></button>
            </div>
          </div>
          <div class="mt-2 flex items-center justify-between gap-3 text-xs text-gray-500 dark:text-dark-400">
            <span>{{ t('accountContributions.filteredAccounts', { count: filteredAccounts.length, total: accounts.length }) }}</span>
            <button v-if="selectedAccountIds.length" type="button" class="text-primary-600 hover:text-primary-700 dark:text-primary-300" @click="clearAccountSelection">{{ t('accountContributions.clearSelection') }}</button>
          </div>
        </div>
        <div v-if="selectedAccountIds.length" class="flex flex-wrap items-center gap-2 border-b border-primary-100 bg-primary-50/70 px-5 py-3 dark:border-primary-900/50 dark:bg-primary-900/15 sm:px-6">
          <span class="mr-auto text-sm font-medium text-primary-800 dark:text-primary-200">{{ t('accountContributions.selectedAccounts', { count: selectedAccountIds.length }) }}</span>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="batchBusy" @click="batchTestAccounts"><Icon name="play" size="sm" />{{ t('accountContributions.batchTest') }}</button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="batchBusy" @click="openBatchEdit"><Icon name="edit" size="sm" />{{ t('accountContributions.batchEdit') }}</button>
          <button type="button" class="btn btn-danger btn-sm" :disabled="batchBusy" @click="batchDeleteAccounts"><Icon name="trash" size="sm" />{{ t('accountContributions.batchDelete') }}</button>
        </div>
        <DataTable :columns="accountColumns" :data="filteredAccounts" :loading="loading" :actions-count="5" row-key="id" default-sort-key="name" selectable :selected-keys="selectedAccountIds" @update:selected-keys="selectedAccountIds = $event">
          <template #cell-name="{ row }">
            <div class="max-w-64">
              <p class="truncate font-medium text-gray-900 dark:text-gray-100" :title="row.name">{{ row.name }}</p>
              <p class="mt-1 truncate text-xs text-gray-500 dark:text-dark-400">{{ row.platform }} / {{ row.type }} · ID {{ row.id }}</p>
            </div>
          </template>

          <template #cell-status="{ row }">
            <div class="space-y-1.5">
              <span :class="String(row.status) === 'active' ? 'badge badge-success' : 'badge badge-gray'">{{ accountStatusLabel(row.status) }}</span>
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ accountInOwnRoom(row.id) ? t('accountContributions.inRoom') : t('accountContributions.privateMode') }}</p>
            </div>
          </template>

          <template #cell-scheduling="{ row }">
            <div class="space-y-1 text-xs text-gray-600 dark:text-dark-300">
              <p>{{ t('accountContributions.concurrency') }} {{ row.concurrency }}</p>
              <p v-if="isPoolAccount(row)">{{ t('accountContributions.priority') }} {{ row.priority }}</p>
              <p>{{ t('accountContributions.loadFactor') }} {{ row.load_factor || row.concurrency }}</p>
              <p>{{ t('accountContributions.proxyShortLabel') }} {{ accountProxyLabel(row) }}</p>
            </div>
          </template>

          <template #cell-groups="{ row }">
            <span class="block max-w-40 truncate text-sm text-gray-700 dark:text-gray-300" :title="groupNames(row)">{{ groupNames(row) }}</span>
          </template>

          <template #header-usage="{ column }">
            <div class="flex items-center gap-1">
              <span>{{ column.label }}</span>
              <HelpTooltip :content="t('accountContributions.usageWindowsHint')" width-class="w-72" />
            </div>
          </template>

          <template #cell-usage="{ row }">
            <div class="min-w-[188px] space-y-1">
              <div v-if="isUsageLoading(row) && !usageSummaries[row.id]" class="space-y-1.5 py-1">
                <div v-for="window in 2" :key="window" class="flex items-center gap-1"><span class="h-4 w-8 animate-pulse rounded bg-gray-200 dark:bg-dark-600" /><span class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-dark-600" /><span class="h-4 w-8 animate-pulse rounded bg-gray-200 dark:bg-dark-600" /></div>
              </div>
              <template v-else-if="usageSummaries[row.id]">
                <template v-if="isLocalUsage(row)">
                  <div class="space-y-0.5 text-xs text-gray-600 dark:text-dark-300">
                    <span class="inline-flex rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium text-slate-600 dark:bg-dark-700 dark:text-dark-300">{{ t('accountContributions.localUsage') }}</span>
                    <p>{{ t('accountContributions.last30Days') }} {{ formatTokens(usageSummaries[row.id]?.stats?.summary?.total_tokens) }}</p>
                    <p>{{ t('accountContributions.usageRequestsCost', { requests: formatNumber(usageSummaries[row.id]?.stats?.summary?.total_requests), cost: money(usageSummaries[row.id]?.stats?.summary?.total_cost) }) }}</p>
                  </div>
                </template>
                <template v-else>
                  <UsageProgressBar v-if="usageSummaries[row.id]?.upstream?.five_hour" label="5h" :utilization="usageSummaries[row.id]?.upstream?.five_hour?.utilization ?? 0" :resets-at="usageSummaries[row.id]?.upstream?.five_hour?.resets_at" :window-stats="usageSummaries[row.id]?.upstream?.five_hour?.window_stats" :show-now-when-idle="true" color="indigo" />
                  <UsageProgressBar v-if="usageSummaries[row.id]?.upstream?.seven_day" label="7d" :utilization="usageSummaries[row.id]?.upstream?.seven_day?.utilization ?? 0" :resets-at="usageSummaries[row.id]?.upstream?.seven_day?.resets_at" :window-stats="usageSummaries[row.id]?.upstream?.seven_day?.window_stats" :show-now-when-idle="true" color="emerald" />
                  <p v-if="!usageSummaries[row.id]?.upstream?.five_hour && !usageSummaries[row.id]?.upstream?.seven_day" class="text-xs text-gray-400 dark:text-dark-500">{{ t('accountContributions.windowUnavailable') }}</p>
                </template>
                <button type="button" class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-wait disabled:opacity-70 dark:text-blue-400 dark:hover:bg-blue-900/30" :disabled="isUsageLoading(row)" @click="loadUsageSummary(row, true)"><Icon name="refresh" size="xs" :class="isUsageLoading(row) ? 'animate-spin' : ''" />{{ t('accountContributions.usageQuery') }}</button>
              </template>
              <div v-else class="space-y-1"><p v-if="usageErrors[row.id]" class="truncate text-xs text-red-600 dark:text-red-400" :title="usageErrors[row.id]">{{ usageErrors[row.id] }}</p><button type="button" class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30" @click="loadUsageSummary(row)"><Icon name="refresh" size="xs" />{{ t('accountContributions.usageQuery') }}</button></div>
            </div>
          </template>

          <template #cell-health="{ row }">
            <div class="max-w-52">
              <span :class="['badge', accountHealthClass(row.error_message)]">{{ accountHealthLabel(row.error_message) }}</span>
              <p v-if="row.error_message" class="mt-1 truncate text-xs text-red-600 dark:text-red-400" :title="row.error_message">{{ row.error_message }}</p>
              <p v-else-if="row.last_used_at" class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('accountContributions.healthLastUsed', { date: formatDate(row.last_used_at) }) }}</p>
            </div>
          </template>

          <template #cell-last_used_at="{ row }"><span class="text-sm text-gray-500 dark:text-dark-400">{{ formatRelativeTime(row.last_used_at) }}</span></template>
          <template #cell-submitted_at="{ row }"><span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDate(row.extra?.submitted_at || row.created_at) }}</span></template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1" @click.stop>
              <button class="contribution-icon-button contribution-icon-button--compact" :title="t('accountContributions.test')" :disabled="busyId === row.id || batchBusy" @click="testAccount(row)"><Icon name="play" size="sm" /></button>
              <button class="contribution-icon-button contribution-icon-button--compact" :title="t('common.edit')" :disabled="batchBusy" @click="startEdit(row)"><Icon name="edit" size="sm" /></button>
              <button v-if="row.type === 'apikey'" class="contribution-icon-button contribution-icon-button--compact" :title="t('accountContributions.updateConnection')" :disabled="batchBusy" @click="startConnectionEdit(row)"><Icon name="link" size="sm" /></button>
              <button class="contribution-icon-button contribution-icon-button--compact" :title="String(row.status) === 'active' ? t('accountContributions.disable') : t('accountContributions.enable')" :disabled="busyId === row.id || batchBusy" @click="toggleAccount(row)"><Icon :name="String(row.status) === 'active' ? 'ban' : 'checkCircle'" size="sm" /></button>
              <button class="contribution-icon-button contribution-icon-button--danger contribution-icon-button--compact" :title="t('common.delete')" :disabled="busyId === row.id || batchBusy" @click="removeAccount(row)"><Icon name="trash" size="sm" /></button>
            </div>
          </template>
        </DataTable>
      </section>

      <BaseDialog :show="batchEditOpen" :title="t('accountContributions.batchEdit')" @close="batchEditOpen = false">
        <div class="space-y-4">
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('accountContributions.batchEditHint', { count: selectedAccountIds.length }) }}</p>
          <label class="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200"><input v-model="batchEditForm.applyStatus" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />{{ t('accountContributions.status') }}</label>
          <select v-if="batchEditForm.applyStatus" v-model="batchEditForm.status" class="input"><option value="active">{{ t('accountContributions.statusActive') }}</option><option value="disabled">{{ t('accountContributions.statusDisabled') }}</option></select>
          <label class="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200"><input v-model="batchEditForm.applyConcurrency" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />{{ t('accountContributions.concurrency') }}</label>
          <input v-if="batchEditForm.applyConcurrency" v-model.number="batchEditForm.concurrency" type="number" min="1" max="1000" class="input" />
          <label class="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200"><input v-model="batchEditForm.applyLoadFactor" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" />{{ t('accountContributions.loadFactor') }}</label>
          <input v-if="batchEditForm.applyLoadFactor" v-model.number="batchEditForm.loadFactor" type="number" min="0" max="10000" class="input" :placeholder="t('accountContributions.loadFactorPlaceholder')" />
        </div>
        <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" :disabled="batchBusy" @click="batchEditOpen = false">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="batchBusy" @click="saveBatchEdit">{{ t('common.save') }}</button></div></template>
      </BaseDialog>

      <BaseDialog :show="editingAccount !== null" :title="t('common.edit')" width="wide" @close="editingId = null">
        <div v-if="editingAccount" class="space-y-4">
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.name') }}</span><input v-model.trim="editForm.name" class="input" :placeholder="t('accountContributions.namePlaceholder')" /></label>
            <div class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.accountScope') }}</span><div class="inline-flex overflow-hidden rounded-md border border-gray-200 dark:border-dark-600"><button type="button" class="scope-option" :class="editForm.shareScope === 'private' ? 'scope-option--active' : ''" @click="editForm.shareScope = 'private'; editForm.poolGroupId = 0; editForm.groupIds = []">{{ t('accountContributions.privateMode') }}</button><button type="button" class="scope-option" :class="editForm.shareScope === 'room' ? 'scope-option--active' : ''" :disabled="!ownRoom" @click="editForm.shareScope = 'room'; editForm.poolGroupId = 0; editForm.groupIds = []">{{ t('accountContributions.shareToRoom') }}</button><button type="button" class="scope-option" :class="editForm.shareScope === 'pool' ? 'scope-option--active' : ''" @click="editForm.shareScope = 'pool'; editForm.groupIds = []; editForm.priority = Math.max(editForm.priority, 20)">{{ t('accountContributions.joinPool') }}</button></div><span v-if="!ownRoom" class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.roomRequiredForShare') }}</span></div>
          </div>
          <label v-if="editForm.shareScope === 'pool'" class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.poolGroup') }}</span><select v-model.number="editForm.poolGroupId" class="input" :disabled="groupsLoading"><option :value="0">{{ t('accountContributions.poolGroupPlaceholder') }}</option><option v-for="group in accountPoolGroups(editingAccount)" :key="group.id" :value="group.id">{{ group.name }} ({{ group.rate_multiplier }}x)</option></select><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.poolGroupHint') }}</span></label>
          <div v-else-if="editForm.shareScope === 'room'" class="grid gap-3 sm:grid-cols-2"><label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.roomShareConcurrency') }}</span><input v-model.number="editForm.roomShareConcurrency" type="number" min="1" :max="editingAccount.concurrency" class="input" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.roomShareConcurrencyPlaceholder', { count: editingAccount.concurrency }) }}</span></label><label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.shareBudget') }}</span><input v-model.number="editForm.roomShareBudgetUSD" type="number" min="0.000001" max="1000000" step="0.000001" class="input" :placeholder="t('accountContributions.shareBudgetPlaceholder')" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.shareBudgetCandidateHint') }}</span></label></div>
          <label v-else class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.groups') }}</span><select v-model="editForm.groupIds" multiple size="4" class="input contribution-group-select" :disabled="groupsLoading"><option v-for="group in accountGroups(editingAccount)" :key="group.id" :value="group.id">{{ group.name }} ({{ group.platform }})</option></select><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.groupsHint') }}</span></label>
          <div class="grid gap-3" :class="editForm.shareScope === 'pool' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'">
            <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.concurrency') }}</span><input v-model.number="editForm.concurrency" type="number" :min="editForm.shareScope === 'pool' ? 30 : 1" max="1000" class="input" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ editForm.shareScope === 'pool' ? t('accountContributions.poolConcurrencyHint') : t('accountContributions.concurrencyHint') }}</span></label>
            <label v-if="editForm.shareScope === 'pool'" class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.priority') }}</span><input v-model.number="editForm.priority" type="number" min="20" max="100000" class="input" @change="normalizePoolPriority(editForm)" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.poolPriorityHint') }}</span></label>
            <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.loadFactor') }}</span><input v-model.number="editForm.loadFactor" type="number" min="0" max="10000" class="input" :placeholder="t('accountContributions.loadFactorPlaceholder')" /><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.loadFactorHint') }}</span></label>
          </div>
          <label class="block"><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.connectionRoute') }}</span><select v-model.number="editForm.proxyId" class="input"><option :value="0">{{ t('accountContributions.directConnection') }}</option><option v-for="proxy in contributionProxies" :key="proxy.id" :value="proxy.id" :disabled="proxy.status !== 'active'">{{ proxy.name }} · {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}</option></select><span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.proxyChangeVerificationHint') }}</span></label>
        </div>
        <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="editingId = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="!editingAccount || busyId === editingAccount.id" @click="editingAccount && saveEdit(editingAccount)">{{ t('common.save') }}</button></div></template>
      </BaseDialog>

      <BaseDialog :show="connectionEditingAccount !== null" :title="t('accountContributions.updateConnection')" width="wide" @close="connectionEditingId = null">
        <div v-if="connectionEditingAccount" class="space-y-3"><p class="text-xs text-gray-500 dark:text-gray-400">{{ t('accountContributions.connectionInfoHint') }}</p><div class="grid gap-3 sm:grid-cols-2"><label><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.newAPIKey') }}</span><input v-model.trim="connectionForm.apiKey" type="password" autocomplete="off" class="input" :placeholder="t('accountContributions.newAPIKeyPlaceholder')" /></label><label><span class="mb-1 block text-sm text-gray-700 dark:text-gray-300">{{ t('accountContributions.newBaseURL') }}</span><input v-model.trim="connectionForm.baseURL" class="input" :placeholder="t('accountContributions.newBaseURLPlaceholder')" /></label></div></div>
        <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="connectionEditingId = null">{{ t('common.cancel') }}</button><button type="button" class="btn btn-primary" :disabled="!connectionEditingAccount || busyId === connectionEditingAccount.id" @click="connectionEditingAccount && saveConnectionInfo(connectionEditingAccount)">{{ t('accountContributions.saveConnection') }}</button></div></template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Icon from '@/components/icons/Icon.vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'
import accountContributionsAPI, { type AccountContributionResult, type ContributionAccountUsageSummary, type ContributionMode, type ContributionProxy, type ContributionProxyTestResult, type UpdateAccountContributionRequest } from '@/api/accountContributions'
import contributionRoomsAPI, { type ContributionRoom, type ContributionRoomAccount } from '@/api/contributionRooms'
import type { Account, AccountPlatform, Group, ProxyProtocol } from '@/types'
import type { Column } from '@/components/common/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatRelativeTime } from '@/utils/format'
import { parseProxyAddress, type ProxyAddressIssue } from '@/utils/proxyAddress'

const { t } = useI18n()
const appStore = useAppStore()
type WorkspaceSection = 'accounts' | 'room'
type AccountPanel = 'list' | 'add' | 'proxies'
type RoomPanel = 'settings' | 'accounts'
type OpenAIContributionMethod = 'codex_session' | 'manual' | 'refresh_token' | 'mobile_refresh_token' | 'codex_pat'

const workspaceRef = ref<HTMLElement | null>(null)
const activeWorkspace = ref<WorkspaceSection>('accounts')
const activeAccountPanel = ref<AccountPanel>('list')
const activeRoomPanel = ref<RoomPanel>('settings')
const accounts = ref<Account[]>([])
const loading = ref(false)
const submitting = ref(false)
const busyId = ref<number | null>(null)
const selectedAccountIds = ref<Array<string | number>>([])
const batchBusy = ref(false)
const batchEditOpen = ref(false)
const accountWorkspaceRefreshing = ref(false)
const accountFilters = reactive({ search: '', platform: '', type: '', status: '', groupId: 0 })
const batchEditForm = reactive({
  applyStatus: false,
  status: 'active' as 'active' | 'disabled',
  applyConcurrency: false,
  concurrency: 3,
  applyLoadFactor: false,
  loadFactor: 0,
})
const editingId = ref<number | null>(null)
const connectionEditingId = ref<number | null>(null)
const lastResult = ref<AccountContributionResult | null>(null)
const wallet = reactive({ balance: 0, earned_total: 0, spent_total: 0 })
const incomeRates = reactive({
  share_reward_rate_percent: null as number | null,
  own_income_rate_percent: null as number | null,
})
const ownRoom = ref<ContributionRoom | null>(null)
const roomLoading = ref(false)
const roomSaving = ref(false)
const roomBudgetDraft = reactive<Record<number, number | undefined>>({})
const roomConcurrencyDraft = reactive<Record<number, number | undefined>>({})
const candidateBudgetDraft = reactive<Record<number, number | undefined>>({})
const candidateConcurrencyDraft = reactive<Record<number, number | undefined>>({})
const roomCreateSelection = reactive<Record<number, boolean>>({})
const contributionGroups = ref<Group[]>([])
const contributionPoolGroups = ref<Group[]>([])
const groupsLoading = ref(false)
const usageLoadingIds = reactive(new Set<number>())
const usageSummaries = reactive<Record<number, ContributionAccountUsageSummary | undefined>>({})
const usageErrors = reactive<Record<number, string | undefined>>({})
const usageRefreshing = ref(false)
const contributionProxies = ref<ContributionProxy[]>([])
const proxiesLoading = ref(false)
const proxySaving = ref(false)
const proxyBusyId = ref<number | null>(null)
const proxyEditingId = ref<number | null>(null)
const proxyTestResults = reactive<Record<number, ContributionProxyTestResult | undefined>>({})

let usageAutoLoadGeneration = 0
const usageAutoLoadConcurrency = 4

const contributionPlatformOptions: Array<{ value: AccountPlatform; label: string }> = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'grok', label: 'Grok' },
]
const modeOptions = computed<Array<{ value: ContributionMode; label: string }>>(() => {
  if (form.platform === 'openai' || form.platform === 'anthropic') {
    return [
      { value: 'oauth', label: t('accountContributions.oauthMode') },
      { value: 'api_key', label: t('accountContributions.apiMode') },
    ]
  }
  return [{ value: 'api_key', label: t('accountContributions.apiMode') }]
})
const openAIContributionMethods: Array<{ value: OpenAIContributionMethod; label: string }> = [
  { value: 'manual', label: t('accountContributions.manualAuthorization') },
  { value: 'refresh_token', label: t('accountContributions.refreshToken') },
  { value: 'mobile_refresh_token', label: t('accountContributions.mobileRefreshToken') },
  { value: 'codex_session', label: t('accountContributions.codexSessionImport') },
  { value: 'codex_pat', label: t('accountContributions.codexPAT') },
]

const oauthFileInput = ref<HTMLInputElement | null>(null)
const oauthFiles = ref<File[]>([])
const openAIContributionMethod = ref<OpenAIContributionMethod>('manual')
const openAIAuthURL = ref('')
const openAIAuthSessionID = ref('')
const openAIAuthLoading = ref(false)
const anthropicAuthURL = ref('')
const anthropicAuthSessionID = ref('')
const anthropicAuthLoading = ref(false)
const manualAuthorizationInput = ref('')
const anthropicAuthorizationInput = ref('')
const refreshTokenInput = ref('')
const codexSessionInput = ref('')
const codexPATInput = ref('')
const form = reactive({
  mode: 'oauth' as ContributionMode, name: '', platform: 'anthropic' as AccountPlatform, apiKey: '', baseURL: '',
  concurrency: 3, priority: 0, loadFactor: 0, groupIds: [] as number[], poolGroupId: 0, shareScope: 'private' as 'private' | 'pool', testModelId: '', proxyId: 0,
})
const platformSelected = ref(false)
const editForm = reactive({ name: '', concurrency: 3, priority: 0, loadFactor: 0, groupIds: [] as number[], poolGroupId: 0, shareScope: 'private' as 'private' | 'room' | 'pool', roomShareBudgetUSD: 5, roomShareConcurrency: 1, proxyId: 0 })
const connectionForm = reactive({ apiKey: '', baseURL: '' })
const roomForm = reactive({ name: '', consumerRateMultiplier: 1, visibility: 'public' as 'public' | 'private', status: 'active' as 'active' | 'paused' })
const proxyForm = reactive({ name: '', protocol: 'http' as ProxyProtocol, host: '', port: 8080, username: '', password: '' })
const proxyAddressInput = ref('')
const proxyAddressIssue = ref<ProxyAddressIssue | null>(null)
const proxyAddressScheme = ref('')
const proxyAddressParsedLabel = ref('')
const proxyManualOpen = ref(false)

const proxyAddressIssueLabel = computed(() => {
  switch (proxyAddressIssue.value) {
    case 'empty': return t('accountContributions.proxyAddressRequired')
    case 'missing_scheme': return t('accountContributions.proxyAddressMissingScheme')
    case 'node_link': return t('accountContributions.proxyAddressNodeUnsupported', { protocol: proxyAddressScheme.value.toUpperCase() })
    case 'unsupported_scheme': return t('accountContributions.proxyAddressUnsupported', { protocol: proxyAddressScheme.value.toUpperCase() })
    case 'unsupported_components': return t('accountContributions.proxyAddressUnsupportedComponents')
    case 'invalid_url':
    case 'missing_host': return t('accountContributions.proxyAddressInvalid')
    default: return ''
  }
})

const selectedRoomCreateAccounts = computed(() => accounts.value.filter((account) => roomCreateSelection[account.id]))
const selectedRoomCreateBudget = computed(() => selectedRoomCreateAccounts.value.reduce((total, account) => {
  const budget = Number(candidateBudgetDraft[account.id])
  return total + (Number.isFinite(budget) && budget > 0 ? budget : 0)
}, 0))
const selectedRoomCreateConcurrency = computed(() => selectedRoomCreateAccounts.value.reduce((total, account) => {
  const concurrency = Number(candidateConcurrencyDraft[account.id])
  return total + (Number.isInteger(concurrency) && concurrency > 0 ? concurrency : 0)
}, 0))
const oauthFilesLabel = computed(() => {
  if (oauthFiles.value.length === 0) return ''
  if (oauthFiles.value.length === 1) return oauthFiles.value[0]?.name || ''
  return t('accountContributions.oauthFileSelected', { count: oauthFiles.value.length })
})
const hasCodexSessionSource = computed(() => oauthFiles.value.length > 0 || Boolean(codexSessionInput.value.trim()))
const preparedRoomAccounts = computed(() => ownRoom.value?.accounts.filter((account) => account.enabled && !account.needs_attention) || [])
const preparedRoomAccountCount = computed(() => preparedRoomAccounts.value.length)
const preparedRoomConcurrency = computed(() => preparedRoomAccounts.value.reduce((total, account) => total + Number(account.share_concurrency || 0), 0))
const formPlatform = computed<AccountPlatform>(() => form.platform)
const formGroups = computed(() => contributionGroups.value.filter((group) => group.platform === formPlatform.value && !(form.mode === 'api_key' && group.require_oauth_only)))
const formPoolGroups = computed(() => contributionPoolGroups.value.filter((group) => group.platform === formPlatform.value && !(form.mode === 'api_key' && group.require_oauth_only)))
const activeContributionProxies = computed(() => contributionProxies.value.filter((proxy) => proxy.status === 'active'))
const editingAccount = computed(() => accounts.value.find((account) => account.id === editingId.value) || null)
const connectionEditingAccount = computed(() => accounts.value.find((account) => account.id === connectionEditingId.value) || null)
const accountPlatforms = computed(() => Array.from(new Set(accounts.value.map((account) => account.platform))).sort())
const accountTypes = computed(() => Array.from(new Set(accounts.value.map((account) => account.type))).sort())
const accountFilterGroups = computed(() => {
  const groups = new Map<number, Group>()
  for (const group of [...contributionGroups.value, ...contributionPoolGroups.value]) groups.set(group.id, group)
  for (const account of accounts.value) for (const group of account.groups || []) groups.set(group.id, group)
  return Array.from(groups.values()).sort((left, right) => left.name.localeCompare(right.name))
})
const hasAccountFilters = computed(() => Boolean(
  accountFilters.search || accountFilters.platform || accountFilters.type || accountFilters.status || accountFilters.groupId,
))
const filteredAccounts = computed(() => {
  const search = accountFilters.search.trim().toLocaleLowerCase()
  return accounts.value.filter((account) => {
    if (accountFilters.platform && account.platform !== accountFilters.platform) return false
    if (accountFilters.type && account.type !== accountFilters.type) return false
    if (accountFilters.status && String(account.status).toLowerCase() !== accountFilters.status) return false
    const groupIDs = new Set([...(account.group_ids || []), ...(account.groups || []).map((group) => group.id)])
    if (accountFilters.groupId && !groupIDs.has(accountFilters.groupId)) return false
    if (!search) return true
    const searchable = [account.name, account.platform, account.type, ...(account.groups || []).map((group) => group.name)]
      .join(' ')
      .toLocaleLowerCase()
    return searchable.includes(search)
  })
})
const hasContributionDraft = computed(() => Boolean(
  oauthFiles.value.length
  || manualAuthorizationInput.value
  || anthropicAuthorizationInput.value
  || refreshTokenInput.value
  || codexSessionInput.value
  || codexPATInput.value
  || form.name
  || form.apiKey
  || form.baseURL
  || form.testModelId
  || form.proxyId !== 0
  || form.groupIds.length
  || form.concurrency !== 3
  || form.priority !== 0
  || form.loadFactor !== 0
))
const accountColumns = computed<Column[]>(() => [
  { key: 'name', label: t('accountContributions.columns.name'), sortable: true },
  { key: 'status', label: t('accountContributions.columns.status'), sortable: true },
  { key: 'scheduling', label: t('accountContributions.columns.scheduling') },
  { key: 'groups', label: t('accountContributions.columns.groups') },
  { key: 'usage', label: t('accountContributions.columns.usage') },
  { key: 'health', label: t('accountContributions.columns.health') },
  { key: 'last_used_at', label: t('accountContributions.columns.lastUsed'), sortable: true },
  { key: 'submitted_at', label: t('accountContributions.columns.submittedAt'), sortable: true },
  { key: 'actions', label: t('accountContributions.columns.actions') }
])

watch(formPlatform, () => {
  const validIDs = new Set(formGroups.value.map((group) => group.id))
  form.groupIds = form.groupIds.filter((groupID) => validIDs.has(groupID))
	const poolIDs = new Set(formPoolGroups.value.map((group) => group.id))
	if (!poolIDs.has(form.poolGroupId)) form.poolGroupId = 0
})

function selectWorkspace(section: WorkspaceSection) {
  activeWorkspace.value = section
  if (section === 'room' && !ownRoom.value) activeRoomPanel.value = 'settings'
  resetWorkspaceScroll()
}

function selectContributionPlatform(platform: AccountPlatform) {
  form.platform = platform
  platformSelected.value = true
  form.mode = platform === 'openai' || platform === 'anthropic' ? 'oauth' : 'api_key'
}

function selectAccountPanel(panel: AccountPanel) {
  activeAccountPanel.value = panel
  resetWorkspaceScroll()
}

function selectRoomPanel(panel: RoomPanel) {
  if (panel === 'accounts' && !ownRoom.value) return
  activeRoomPanel.value = panel
  resetWorkspaceScroll()
}

function resetWorkspaceScroll() {
  void nextTick(() => window.scrollTo({ top: 0, behavior: 'auto' }))
}

async function loadAccounts() {
  const usageGeneration = ++usageAutoLoadGeneration
  loading.value = true
  try {
    const result = await accountContributionsAPI.list(1, 100)
    accounts.value = result.items
    for (const account of accounts.value) {
      if (candidateConcurrencyDraft[account.id] === undefined) candidateConcurrencyDraft[account.id] = 1
    }
    const accountIDs = new Set(accounts.value.map((account) => account.id))
    selectedAccountIds.value = selectedAccountIds.value.filter((accountID) => accountIDs.has(Number(accountID)))
    for (const accountID of Object.keys(roomCreateSelection).map(Number)) {
      if (!accountIDs.has(accountID)) delete roomCreateSelection[accountID]
    }
    Object.assign(wallet, result.wallet || { balance: 0, earned_total: 0, spent_total: 0 })
    Object.assign(incomeRates, result.income_rates || { share_reward_rate_percent: null, own_income_rate_percent: null })
    void autoLoadUsageSummaries(result.items, usageGeneration)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    loading.value = false
  }
}

async function loadContributionGroups() {
  groupsLoading.value = true
  try {
    const [groups, poolGroups] = await Promise.all([accountContributionsAPI.listGroups(), accountContributionsAPI.listPoolGroups()])
    contributionGroups.value = groups
    contributionPoolGroups.value = poolGroups
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.groupsLoadFailed')))
  } finally {
    groupsLoading.value = false
  }
}

async function loadContributionProxies() {
  proxiesLoading.value = true
  try {
    contributionProxies.value = await accountContributionsAPI.listProxies()
    if (form.proxyId && !activeContributionProxies.value.some((proxy) => proxy.id === form.proxyId)) form.proxyId = 0
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.proxiesLoadFailed')))
  } finally {
    proxiesLoading.value = false
  }
}

async function loadRooms() {
  roomLoading.value = true
  try {
    const room = await contributionRoomsAPI.getOwn()
    ownRoom.value = room
    populateRoomForm(room)
  } catch (error) {
    if (isNotFound(error)) {
      ownRoom.value = null
      return
    }
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.roomLoadFailed')))
  } finally {
    roomLoading.value = false
  }
}

async function refreshWorkspace() {
	await Promise.all([loadAccounts(), loadContributionGroups(), loadContributionProxies()])
	// A contribution room can only be created with a contributed account. Skip
	// the room endpoint for first-time users so their empty workspace stays an
	// expected state instead of surfacing an irrelevant backend error.
	if (accounts.value.length === 0) {
		ownRoom.value = null
		roomLoading.value = false
		return
	}
	await loadRooms()
}

async function submitContribution() {
	normalizePoolPriority(form)
	if (form.shareScope === 'pool' && form.poolGroupId <= 0) {
		appStore.showError(t('accountContributions.poolGroupRequired'))
		return
	}
	if (form.shareScope === 'pool' && form.concurrency < 30) {
		appStore.showError(t('accountContributions.poolConcurrencyInvalid'))
		return
	}
	let oauthContents: string[] | undefined
  if (form.mode === 'oauth' && form.platform === 'openai') {
    if (openAIContributionMethod.value === 'codex_session') {
      if (!hasCodexSessionSource.value) {
        appStore.showError(t('accountContributions.oauthFileRequired'))
        return
      }
      try {
        oauthContents = await Promise.all(oauthFiles.value.map(readOAuthFile))
        if (codexSessionInput.value.trim()) oauthContents.push(codexSessionInput.value.trim())
      } catch {
        appStore.showError(t('accountContributions.oauthFileReadFailed'))
        return
      }
    }
  }
  submitting.value = true
  try {
    if (form.mode === 'api_key') {
      lastResult.value = await accountContributionsAPI.submit({ mode: form.mode, name: form.name || undefined, platform: form.platform, api_key: form.apiKey, base_url: form.baseURL || undefined, concurrency: form.concurrency, priority: form.shareScope === 'pool' ? form.priority : undefined, load_factor: form.loadFactor, group_ids: form.shareScope === 'private' ? form.groupIds : undefined, pool_group_id: form.shareScope === 'pool' ? form.poolGroupId : undefined, test_model_id: form.testModelId || undefined, proxy_id: form.proxyId || undefined })
    } else if (form.platform === 'openai' && openAIContributionMethod.value === 'codex_session') {
      lastResult.value = await accountContributionsAPI.submit({ mode: 'oauth', name: form.name || undefined, contents: oauthContents, concurrency: form.concurrency, priority: form.shareScope === 'pool' ? form.priority : undefined, load_factor: form.loadFactor, group_ids: form.shareScope === 'private' ? form.groupIds : undefined, pool_group_id: form.shareScope === 'pool' ? form.poolGroupId : undefined, test_model_id: form.testModelId || undefined, proxy_id: form.proxyId || undefined })
    } else if (form.platform === 'openai' && openAIContributionMethod.value === 'manual') {
      const callback = parseOpenAIAuthCallback(manualAuthorizationInput.value)
      if (!openAIAuthSessionID.value || !callback.code || !callback.state) {
        appStore.showError(t('accountContributions.openAIAuthCallbackRequired'))
        return
      }
      lastResult.value = await accountContributionsAPI.createOpenAIFromCode({ ...openAIContributionPayload(), session_id: openAIAuthSessionID.value, code: callback.code, state: callback.state })
    } else if (form.platform === 'openai' && (openAIContributionMethod.value === 'refresh_token' || openAIContributionMethod.value === 'mobile_refresh_token')) {
      if (!refreshTokenInput.value.trim()) {
        appStore.showError(t('accountContributions.refreshTokenRequired'))
        return
      }
      lastResult.value = await accountContributionsAPI.createOpenAIFromRefreshToken({ ...openAIContributionPayload(), refresh_token: refreshTokenInput.value.trim() }, openAIContributionMethod.value === 'mobile_refresh_token')
    } else if (form.platform === 'openai') {
      if (!codexPATInput.value.trim()) {
        appStore.showError(t('accountContributions.codexPATRequired'))
        return
      }
      lastResult.value = await accountContributionsAPI.createOpenAIFromCodexPAT({ ...openAIContributionPayload(), access_token: codexPATInput.value.trim() })
    } else if (form.platform === 'anthropic') {
      const code = parseAuthorizationCode(anthropicAuthorizationInput.value)
      if (!anthropicAuthSessionID.value || !code) {
        appStore.showError(t('accountContributions.anthropicAuthCodeRequired'))
        return
      }
      lastResult.value = await accountContributionsAPI.createAnthropicFromCode({ ...openAIContributionPayload(), session_id: anthropicAuthSessionID.value, code })
    } else {
      appStore.showError(t('accountContributions.apiMode'))
      return
    }
    const skipped = lastResult.value.skipped || 0
    if (lastResult.value.created > 0) {
      appStore.showSuccess(skipped > 0
        ? t('accountContributions.submitSuccessWithSkipped', { created: lastResult.value.created, skipped })
        : t('accountContributions.submitSuccess'))
      form.apiKey = ''
      oauthFiles.value = []
      codexSessionInput.value = ''
      refreshTokenInput.value = ''
      codexPATInput.value = ''
      manualAuthorizationInput.value = ''
      openAIAuthURL.value = ''
      openAIAuthSessionID.value = ''
      anthropicAuthorizationInput.value = ''
      anthropicAuthURL.value = ''
      anthropicAuthSessionID.value = ''
      await refreshWorkspace()
    } else if (skipped > 0) {
      appStore.showInfo(t('accountContributions.submitSkipped', { skipped }))
    } else appStore.showError(contributionFailureMessage(lastResult.value))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.submitFailed')))
  } finally {
    submitting.value = false
  }
}

async function refreshAccountWorkspace() {
  if (accountWorkspaceRefreshing.value) return
  accountWorkspaceRefreshing.value = true
  try {
    await refreshWorkspace()
    await refreshUsageSummaries()
  } finally {
    accountWorkspaceRefreshing.value = false
  }
}

function openAIContributionPayload() {
	return { name: form.name || undefined, concurrency: form.concurrency, priority: form.shareScope === 'pool' ? form.priority : undefined, load_factor: form.loadFactor, group_ids: form.shareScope === 'private' ? form.groupIds : undefined, pool_group_id: form.shareScope === 'pool' ? form.poolGroupId : undefined, test_model_id: form.testModelId || undefined, proxy_id: form.proxyId || undefined }
}

function contributionFailureMessage(result: AccountContributionResult | null): string {
  const messages = (result?.items || []).map((item) => item.message?.trim()).filter((message): message is string => Boolean(message))
  return messages.length ? messages.join('\n') : t('accountContributions.submitFailed')
}

async function generateOpenAIAuthURL() {
  openAIAuthLoading.value = true
  try {
    const result = await accountContributionsAPI.generateOpenAIAuthURL({ proxy_id: form.proxyId || undefined })
    openAIAuthURL.value = result.auth_url
    openAIAuthSessionID.value = result.session_id
    manualAuthorizationInput.value = ''
    appStore.showSuccess(t('accountContributions.openAIAuthURLReady'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.openAIAuthURLFailed')))
  } finally {
    openAIAuthLoading.value = false
  }
}

async function generateAnthropicAuthURL() {
  anthropicAuthLoading.value = true
  try {
    const result = await accountContributionsAPI.generateAnthropicAuthURL({ proxy_id: form.proxyId || undefined })
    anthropicAuthURL.value = result.auth_url
    anthropicAuthSessionID.value = result.session_id
    anthropicAuthorizationInput.value = ''
    appStore.showSuccess(t('accountContributions.anthropicAuthURLReady'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.anthropicAuthURLFailed')))
  } finally {
    anthropicAuthLoading.value = false
  }
}

function parseOpenAIAuthCallback(value: string): { code: string; state: string } {
  const raw = value.trim()
  if (!raw) return { code: '', state: '' }
  try {
    const url = raw.includes('?') ? new URL(raw) : new URL(`http://localhost/callback?${raw.replace(/^\?/, '')}`)
    return { code: url.searchParams.get('code') || '', state: url.searchParams.get('state') || '' }
  } catch {
    return { code: '', state: '' }
  }
}

function parseAuthorizationCode(value: string): string {
  const raw = value.trim()
  if (!raw) return ''
  try {
    const url = raw.includes('?') ? new URL(raw) : new URL(`http://localhost/callback?${raw.replace(/^\?/, '')}`)
    return url.searchParams.get('code') || raw
  } catch {
    return raw
  }
}

function handleOAuthFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (files.length === 0) return
  if (files.some((file) => !isOAuthContributionFile(file))) {
    appStore.showError(t('accountContributions.oauthFileInvalid'))
    return
  }
  oauthFiles.value = files
}

function isOAuthContributionFile(file: File) {
  const name = file.name.toLowerCase()
  return name.endsWith('.json') || name.endsWith('.txt') || file.type === 'application/json' || file.type === 'text/plain'
}

async function readOAuthFile(file: File): Promise<string> {
  if (typeof file.text === 'function') return file.text()
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('oauth_file_read_failed'))
    reader.readAsText(file)
  })
}

async function createRoom() {
  const selectedAccounts = selectedRoomCreateAccounts.value
  if (selectedAccounts.length === 0) {
    appStore.showError(t('accountContributions.roomAccountRequired'))
    return
  }
  const accountsToCreate = selectedAccounts.map((account) => ({
    account,
    shareBudgetUSD: Number(candidateBudgetDraft[account.id]),
    shareConcurrency: Number(candidateConcurrencyDraft[account.id]),
  }))
  const invalidBudget = accountsToCreate.find(({ shareBudgetUSD }) => !Number.isFinite(shareBudgetUSD) || shareBudgetUSD <= 0 || shareBudgetUSD > 1000000)
  if (invalidBudget) {
    appStore.showError(t('accountContributions.roomCreateBudgetRequired', { name: invalidBudget.account.name }))
    return
  }
  const invalidConcurrency = accountsToCreate.find(({ account, shareConcurrency }) => !Number.isInteger(shareConcurrency) || shareConcurrency <= 0 || (account.concurrency > 0 && shareConcurrency > account.concurrency))
  if (invalidConcurrency) {
    appStore.showError(t('accountContributions.roomShareConcurrencyInvalid', { name: invalidConcurrency.account.name, count: invalidConcurrency.account.concurrency }))
    return
  }
  roomSaving.value = true
  try {
    const testResults = await Promise.all(accountsToCreate.map(async ({ account }) => ({
      account,
      result: await accountContributionsAPI.test(account.id),
    })))
    const failedTest = testResults.find(({ result }) => result.status !== 'success')
    if (failedTest) {
      appStore.showError(t('accountContributions.roomCreateAccountTestFailed', {
        name: failedTest.account.name,
        message: failedTest.result.error_message || t('accountContributions.testFailed'),
      }))
      return
    }
    ownRoom.value = await contributionRoomsAPI.create({
      name: roomForm.name,
      consumer_rate_multiplier: roomForm.consumerRateMultiplier,
      accounts: accountsToCreate.map(({ account, shareBudgetUSD, shareConcurrency }) => ({ account_id: account.id, share_budget_usd: shareBudgetUSD, share_concurrency: shareConcurrency })),
    })
    populateRoomForm(ownRoom.value)
    for (const { account } of accountsToCreate) {
      delete roomCreateSelection[account.id]
      delete candidateBudgetDraft[account.id]
      delete candidateConcurrencyDraft[account.id]
    }
	activeRoomPanel.value = 'accounts'
	appStore.showSuccess(t('accountContributions.roomCreateSuccess'))
    await loadRooms()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.roomSaveFailed')))
  } finally {
    roomSaving.value = false
  }
}

function openAddAccountForRoom() {
  selectWorkspace('accounts')
  selectAccountPanel('add')
}

async function saveRoom() {
  roomSaving.value = true
  try {
    ownRoom.value = await contributionRoomsAPI.updateOwn({ name: roomForm.name, consumer_rate_multiplier: roomForm.consumerRateMultiplier, status: roomForm.status, visibility: roomForm.visibility })
    populateRoomForm(ownRoom.value)
    appStore.showSuccess(t('accountContributions.roomSaveSuccess'))
    await loadRooms()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.roomSaveFailed')))
  } finally {
    roomSaving.value = false
  }
}

async function toggleRoomAccount(account: ContributionRoomAccount) {
  busyId.value = account.account_id
  try {
    await contributionRoomsAPI.updateOwnAccount(account.account_id, { enabled: !account.enabled })
    await loadRooms()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    busyId.value = null
  }
}

async function saveRoomAccountSettings(account: ContributionRoomAccount) {
  const shareBudgetUSD = Number(roomBudgetDraft[account.account_id])
  if (!Number.isFinite(shareBudgetUSD) || shareBudgetUSD < 0) {
    appStore.showError(t('accountContributions.shareBudgetInvalid'))
    return
  }
  const shareConcurrency = Number(roomConcurrencyDraft[account.account_id])
  if (!Number.isInteger(shareConcurrency) || shareConcurrency <= 0 || (account.concurrency > 0 && shareConcurrency > account.concurrency)) {
    appStore.showError(t('accountContributions.roomShareConcurrencyInvalid', { name: account.name, count: account.concurrency }))
    return
  }
  busyId.value = account.account_id
  try {
    await contributionRoomsAPI.updateOwnAccount(account.account_id, { share_budget_usd: shareBudgetUSD, share_concurrency: shareConcurrency })
    appStore.showSuccess(t('accountContributions.roomAccountSettingsSaved'))
    await loadRooms()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.shareBudgetSaveFailed')))
  } finally {
    busyId.value = null
  }
}

async function removeRoomAccount(accountId: number) {
  busyId.value = accountId
  try {
    await contributionRoomsAPI.removeAccount(accountId)
    appStore.showSuccess(t('accountContributions.roomAccountRemoved'))
    await loadRooms()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    busyId.value = null
  }
}

function startEdit(account: Account) {
  editingId.value = account.id
  connectionEditingId.value = null
  editForm.name = account.name
  editForm.concurrency = account.concurrency
  editForm.priority = account.priority
  editForm.loadFactor = account.load_factor || 0
  editForm.groupIds = [...(account.group_ids || [])]
  const roomMember = ownRoom.value?.accounts.find((member) => member.account_id === account.id)
  editForm.shareScope = roomMember ? 'room' : (isPoolAccount(account) ? 'pool' : 'private')
  editForm.poolGroupId = isPoolAccount(account) ? Number(account.group_ids?.[0] || 0) : 0
  editForm.roomShareBudgetUSD = roomMember?.share_budget_usd || 5
  editForm.roomShareConcurrency = roomMember?.share_concurrency || Math.min(Math.max(account.concurrency, 1), 1)
  editForm.proxyId = account.proxy_id || 0
}

async function saveEdit(account: Account) {
  normalizePoolPriority(editForm)
  if (editForm.shareScope === 'pool' && editForm.poolGroupId <= 0) {
    appStore.showError(t('accountContributions.poolGroupRequired'))
    return
  }
  if (editForm.shareScope === 'pool' && editForm.concurrency < 30) {
    appStore.showError(t('accountContributions.poolConcurrencyInvalid'))
    return
  }
  if (editForm.shareScope === 'room') {
    if (!ownRoom.value) {
      appStore.showError(t('accountContributions.roomRequiredForShare'))
      return
    }
    if (!Number.isFinite(editForm.roomShareBudgetUSD) || editForm.roomShareBudgetUSD <= 0) {
      appStore.showError(t('accountContributions.shareBudgetRequired'))
      return
    }
    if (!Number.isInteger(editForm.roomShareConcurrency) || editForm.roomShareConcurrency <= 0 || editForm.roomShareConcurrency > editForm.concurrency) {
      appStore.showError(t('accountContributions.roomShareConcurrencyInvalid', { name: account.name, count: editForm.concurrency }))
      return
    }
  }
  busyId.value = account.id
  try {
    const roomMember = ownRoom.value?.accounts.find((member) => member.account_id === account.id)
    if (editForm.shareScope !== 'room' && roomMember) await contributionRoomsAPI.removeAccount(account.id)
    await accountContributionsAPI.update(account.id, {
      name: editForm.name,
      concurrency: editForm.concurrency,
      priority: editForm.shareScope === 'pool' ? editForm.priority : undefined,
      load_factor: editForm.loadFactor,
      group_ids: editForm.shareScope === 'private' ? editForm.groupIds : [],
      pool_group_id: editForm.shareScope === 'pool' ? editForm.poolGroupId : 0,
      proxy_id: editForm.proxyId,
    })
    if (editForm.shareScope === 'room') {
      const result = await accountContributionsAPI.test(account.id)
      if (result.status !== 'success') {
        appStore.showError(result.error_message || t('accountContributions.testFailed'))
        return
      }
      if (roomMember) {
        await contributionRoomsAPI.updateOwnAccount(account.id, { share_budget_usd: editForm.roomShareBudgetUSD, share_concurrency: editForm.roomShareConcurrency })
      } else {
        await contributionRoomsAPI.addAccount(account.id, editForm.roomShareBudgetUSD, editForm.roomShareConcurrency)
      }
    }
    editingId.value = null
    appStore.showSuccess(t('accountContributions.updateSuccess'))
    await refreshWorkspace()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally {
    busyId.value = null
  }
}

function isPoolAccount(account: Account): boolean {
	return String(account.extra?.share_mode || '').toLowerCase() === 'pool'
}

function normalizePoolPriority(target: { shareScope: 'private' | 'room' | 'pool'; priority: number }) {
  if (target.shareScope === 'pool' && (!Number.isFinite(target.priority) || target.priority < 20)) target.priority = 20
}

function accountPoolGroups(account: Account): Group[] {
	return contributionPoolGroups.value.filter((group) => group.platform === account.platform && !(account.type === 'apikey' && group.require_oauth_only))
}

function clearProxyAddressFeedback() {
  proxyAddressIssue.value = null
  proxyAddressScheme.value = ''
  proxyAddressParsedLabel.value = ''
}

function applyProxyAddress(showFeedback: boolean): boolean {
  const result = parseProxyAddress(proxyAddressInput.value)
  if (!result.ok) {
    proxyAddressIssue.value = result.issue
    proxyAddressScheme.value = result.scheme || ''
    proxyAddressParsedLabel.value = ''
    if (showFeedback) appStore.showError(proxyAddressIssueLabel.value)
    if (result.issue === 'node_link') proxyManualOpen.value = false
    return false
  }
  Object.assign(proxyForm, result.value)
  if (!proxyForm.name) proxyForm.name = `${result.value.host}:${result.value.port}`
  proxyAddressIssue.value = null
  proxyAddressScheme.value = ''
  proxyAddressParsedLabel.value = t('accountContributions.proxyAddressParsed', {
    protocol: result.value.protocol.toUpperCase(),
    endpoint: `${result.value.host}:${result.value.port}`,
  })
  if (showFeedback) appStore.showSuccess(proxyAddressParsedLabel.value)
  return true
}

async function saveContributionProxy() {
  if (proxyAddressInput.value) {
    if (!applyProxyAddress(false)) {
      appStore.showError(proxyAddressIssueLabel.value)
      return
    }
  } else if (!proxyForm.host || !proxyForm.port) {
    proxyAddressIssue.value = 'empty'
    appStore.showError(proxyAddressIssueLabel.value)
    return
  }
  if (!proxyForm.name) proxyForm.name = `${proxyForm.host}:${proxyForm.port}`
  proxySaving.value = true
  try {
    const payload = {
      name: proxyForm.name,
      protocol: proxyForm.protocol,
      host: proxyForm.host,
      port: proxyForm.port,
      username: proxyForm.username,
      password: proxyForm.password || undefined,
    }
    if (proxyEditingId.value) {
      await accountContributionsAPI.updateProxy(proxyEditingId.value, payload)
      appStore.showSuccess(t('accountContributions.proxyUpdated'))
    } else {
      await accountContributionsAPI.createProxy(payload)
      appStore.showSuccess(t('accountContributions.proxyAdded'))
    }
    resetProxyForm()
    await loadContributionProxies()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.proxySaveFailed')))
  } finally {
    proxySaving.value = false
  }
}

function startProxyEdit(proxy: ContributionProxy) {
  proxyEditingId.value = proxy.id
  proxyForm.name = proxy.name
  proxyForm.protocol = proxy.protocol
  proxyForm.host = proxy.host
  proxyForm.port = proxy.port
  proxyForm.username = proxy.username || ''
  proxyForm.password = ''
  proxyAddressInput.value = ''
  clearProxyAddressFeedback()
  proxyManualOpen.value = true
  resetWorkspaceScroll()
}

function resetProxyForm() {
  proxyEditingId.value = null
  proxyForm.name = ''
  proxyForm.protocol = 'http'
  proxyForm.host = ''
  proxyForm.port = 8080
  proxyForm.username = ''
  proxyForm.password = ''
  proxyAddressInput.value = ''
  clearProxyAddressFeedback()
  proxyManualOpen.value = false
}

async function testPrivateProxy(proxy: ContributionProxy) {
  proxyBusyId.value = proxy.id
  try {
    proxyTestResults[proxy.id] = await accountContributionsAPI.testProxy(proxy.id)
    if (proxyTestResults[proxy.id]?.success) appStore.showSuccess(t('accountContributions.proxyTestSuccess'))
    else appStore.showError(proxyTestResults[proxy.id]?.message || t('accountContributions.proxyTestFailed'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.proxyTestFailed')))
  } finally {
    proxyBusyId.value = null
  }
}

async function togglePrivateProxy(proxy: ContributionProxy) {
  proxyBusyId.value = proxy.id
  try {
    await accountContributionsAPI.updateProxy(proxy.id, { status: proxy.status === 'active' ? 'inactive' : 'active' })
    await loadContributionProxies()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.proxySaveFailed')))
  } finally {
    proxyBusyId.value = null
  }
}

async function removePrivateProxy(proxy: ContributionProxy) {
  if (!window.confirm(t('accountContributions.deleteProxyConfirm', { name: proxy.name }))) return
  proxyBusyId.value = proxy.id
  try {
    await accountContributionsAPI.deleteProxy(proxy.id)
    if (proxyEditingId.value === proxy.id) resetProxyForm()
    delete proxyTestResults[proxy.id]
    appStore.showSuccess(t('accountContributions.proxyDeleted'))
    await loadContributionProxies()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.proxyDeleteFailed')))
  } finally {
    proxyBusyId.value = null
  }
}

function startConnectionEdit(account: Account) {
  editingId.value = null
  connectionEditingId.value = account.id
  connectionForm.apiKey = ''
  connectionForm.baseURL = ''
}

async function saveConnectionInfo(account: Account) {
  if (!connectionForm.apiKey && !connectionForm.baseURL) {
    appStore.showError(t('accountContributions.connectionInfoRequired'))
    return
  }
  busyId.value = account.id
  try {
    await accountContributionsAPI.update(account.id, {
      api_key: connectionForm.apiKey || undefined,
      base_url: connectionForm.baseURL || undefined,
    })
    connectionEditingId.value = null
    connectionForm.apiKey = ''
    connectionForm.baseURL = ''
    appStore.showSuccess(t('accountContributions.connectionInfoSaved'))
    await refreshWorkspace()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.connectionInfoSaveFailed')))
  } finally {
    busyId.value = null
  }
}

async function testAccount(account: Account) { await testAccountById(account.id) }
async function testAccountById(accountId: number) {
  busyId.value = accountId
  try {
    const result = await accountContributionsAPI.test(accountId)
    if (result.status === 'success') appStore.showSuccess(t('accountContributions.testSuccess'))
    else appStore.showError(result.error_message || t('accountContributions.testFailed'))
    await refreshWorkspace()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('accountContributions.testFailed')))
  } finally {
    busyId.value = null
  }
}

async function loadUsageSummary(account: Account, force = false) {
  if (usageLoadingIds.has(account.id)) return
  usageLoadingIds.add(account.id)
  delete usageErrors[account.id]
  try {
    const summary = await accountContributionsAPI.getUsageSummary(account.id, force)
    usageSummaries[account.id] = summary
  } catch (error) {
    usageErrors[account.id] = extractApiErrorMessage(error, t('accountContributions.usageLoadFailed'))
  } finally {
    usageLoadingIds.delete(account.id)
  }
}

async function autoLoadUsageSummaries(contributions: Account[], generation: number) {
  await loadUsageSummaries(contributions, generation)
}

async function refreshUsageSummaries() {
  if (usageRefreshing.value || !accounts.value.length) return
  const generation = ++usageAutoLoadGeneration
  usageRefreshing.value = true
  try {
    await loadUsageSummaries(accounts.value, generation, true)
  } finally {
    usageRefreshing.value = false
  }
}

async function loadUsageSummaries(contributions: Account[], generation: number, force = false) {
  if (!contributions.length || generation !== usageAutoLoadGeneration) return

  let nextIndex = 0
  const workerCount = Math.min(usageAutoLoadConcurrency, contributions.length)
  const worker = async () => {
    while (generation === usageAutoLoadGeneration) {
      const account = contributions[nextIndex++]
      if (!account) return
      await loadUsageSummary(account, force)
    }
  }

  await Promise.all(Array.from({ length: workerCount }, () => worker()))
}

function isUsageLoading(account: Account) {
  return usageLoadingIds.has(account.id)
}

async function toggleAccount(account: Account) {
  busyId.value = account.id
  try {
    await accountContributionsAPI.update(account.id, { status: String(account.status) === 'active' ? 'disabled' : 'active' })
    await refreshWorkspace()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally { busyId.value = null }
}

async function removeAccount(account: Account) {
  if (!window.confirm(t('accountContributions.deleteConfirm', { name: account.name }))) return
  busyId.value = account.id
  try {
    await accountContributionsAPI.delete(account.id)
    appStore.showSuccess(t('accountContributions.deleteSuccess'))
    await refreshWorkspace()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
  } finally { busyId.value = null }
}

function clearAccountFilters() {
  Object.assign(accountFilters, { search: '', platform: '', type: '', status: '', groupId: 0 })
}

function clearAccountSelection() {
  selectedAccountIds.value = []
}

function selectedAccountsForBatch(): Account[] {
  const selected = new Set(selectedAccountIds.value.map((accountID) => Number(accountID)))
  return accounts.value.filter((account) => selected.has(account.id))
}

type BatchResult = { succeeded: number[]; failures: Array<{ account: Account; message: string }> }

async function runSelectedAccountBatch(operation: (account: Account) => Promise<{ success: boolean; message?: string }>): Promise<BatchResult> {
  const selectedAccounts = selectedAccountsForBatch()
  const result: BatchResult = { succeeded: [], failures: [] }
  if (!selectedAccounts.length) {
    clearAccountSelection()
    return result
  }

  batchBusy.value = true
  let nextIndex = 0
  const worker = async () => {
    while (nextIndex < selectedAccounts.length) {
      const account = selectedAccounts[nextIndex++]
      if (!account) return
      try {
        const outcome = await operation(account)
        if (outcome.success) result.succeeded.push(account.id)
        else result.failures.push({ account, message: outcome.message || t('common.error') })
      } catch (error) {
        result.failures.push({ account, message: extractApiErrorMessage(error, t('common.error')) })
      }
    }
  }

  try {
    await Promise.all(Array.from({ length: Math.min(4, selectedAccounts.length) }, () => worker()))
    return result
  } finally {
    batchBusy.value = false
  }
}

function showBatchResult(result: BatchResult) {
  if (result.failures.length === 0) {
    appStore.showSuccess(t('accountContributions.batchOperationSuccess', { count: result.succeeded.length }))
    return
  }
  const firstFailure = result.failures[0]
  appStore.showError(t('accountContributions.batchOperationPartial', {
    succeeded: result.succeeded.length,
    failed: result.failures.length,
    name: firstFailure?.account.name || '',
    message: firstFailure?.message || '',
  }), 8000)
}

async function batchTestAccounts() {
  const result = await runSelectedAccountBatch(async (account) => {
    const response = await accountContributionsAPI.test(account.id)
    return { success: response.status === 'success', message: response.error_message }
  })
  showBatchResult(result)
  await refreshWorkspace()
}

function openBatchEdit() {
  if (!selectedAccountIds.value.length) return
  Object.assign(batchEditForm, {
    applyStatus: false,
    status: 'active',
    applyConcurrency: false,
    concurrency: 3,
    applyLoadFactor: false,
    loadFactor: 0,
  })
  batchEditOpen.value = true
}

async function saveBatchEdit() {
  const payload: UpdateAccountContributionRequest = {}
  if (batchEditForm.applyStatus) payload.status = batchEditForm.status
  if (batchEditForm.applyConcurrency) {
    if (!Number.isInteger(batchEditForm.concurrency) || batchEditForm.concurrency < 1 || batchEditForm.concurrency > 1000) {
      appStore.showError(t('accountContributions.batchConcurrencyInvalid'))
      return
    }
    payload.concurrency = batchEditForm.concurrency
  }
  if (batchEditForm.applyLoadFactor) {
    if (!Number.isInteger(batchEditForm.loadFactor) || batchEditForm.loadFactor < 0 || batchEditForm.loadFactor > 10000) {
      appStore.showError(t('accountContributions.batchLoadFactorInvalid'))
      return
    }
    payload.load_factor = batchEditForm.loadFactor
  }
  if (Object.keys(payload).length === 0) {
    appStore.showError(t('accountContributions.batchEditRequired'))
    return
  }

  const result = await runSelectedAccountBatch(async (account) => {
    await accountContributionsAPI.update(account.id, payload)
    return { success: true }
  })
  const succeeded = new Set(result.succeeded)
  selectedAccountIds.value = selectedAccountIds.value.filter((accountID) => !succeeded.has(Number(accountID)))
  batchEditOpen.value = false
  showBatchResult(result)
  await refreshWorkspace()
}

async function batchDeleteAccounts() {
  const count = selectedAccountsForBatch().length
  if (!count || !window.confirm(t('accountContributions.batchDeleteConfirm', { count }))) return
  const result = await runSelectedAccountBatch(async (account) => {
    await accountContributionsAPI.delete(account.id)
    return { success: true }
  })
  const succeeded = new Set(result.succeeded)
  selectedAccountIds.value = selectedAccountIds.value.filter((accountID) => !succeeded.has(Number(accountID)))
  showBatchResult(result)
  await refreshWorkspace()
}

function populateRoomForm(room: ContributionRoom) {
  roomForm.name = room.name
  roomForm.consumerRateMultiplier = room.consumer_rate_multiplier
  roomForm.visibility = room.visibility === 'private' ? 'private' : 'public'
  roomForm.status = room.status === 'paused' ? 'paused' : 'active'
  for (const account of room.accounts) {
    roomBudgetDraft[account.account_id] = account.share_budget_usd
    roomConcurrencyDraft[account.account_id] = account.share_concurrency
  }
}
function isNotFound(error: unknown) {
  const candidate = error as { status?: number; response?: { status?: number } }
  // apiClient normalizes HTTP errors into { status, message }, while a few
  // direct Axios calls retain response.status. A missing room is an expected
  // first-use state in either shape.
  return candidate?.status === 404 || candidate?.response?.status === 404
}
function formatDate(value: unknown): string { if (!value) return '-'; const date = new Date(String(value)); return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString() }
function money(value: unknown): string { return Number(value || 0).toFixed(4) }
function formatPercent(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '--'
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(value)}%`
}
function formatNumber(value: unknown): string { return new Intl.NumberFormat().format(Number(value || 0)) }
function formatTokens(value: unknown): string {
  const tokens = Number(value || 0)
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(2)}M`
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`
  return formatNumber(tokens)
}
function isLocalUsage(account: Account) { return String(account.type).toLowerCase() === 'apikey' }
function accountStatusLabel(status: unknown) { return String(status).toLowerCase() === 'active' ? t('accountContributions.statusActive') : t('accountContributions.statusDisabled') }
function accountHealthLabel(errorMessage: string | null | undefined) { return errorMessage ? t('accountContributions.needsAttention') : t('accountContributions.healthy') }
function accountHealthClass(errorMessage: string | null | undefined) { return errorMessage ? 'badge-danger' : 'badge-success' }
function accountGroups(account: Account): Group[] {
  return contributionGroups.value.filter((group) => group.platform === account.platform && !(account.type === 'apikey' && group.require_oauth_only))
}
function groupNames(account: Account): string {
  const names = account.groups?.map((group) => group.name).filter(Boolean) || []
  if (names.length > 0) return names.join(', ')
  const groupIDs = account.group_ids || []
  if (groupIDs.length === 0) return t('accountContributions.noGroups')
  const namesByID = new Map(contributionGroups.value.map((group) => [group.id, group.name]))
  return groupIDs.map((groupID) => namesByID.get(groupID) || `#${groupID}`).join(', ')
}
function proxyEndpoint(proxy: ContributionProxy): string { return `${proxy.protocol}://${proxy.host}:${proxy.port}` }
function accountProxyLabel(account: Account): string {
  if (!account.proxy_id) return t('accountContributions.directConnection')
  return contributionProxies.value.find((proxy) => proxy.id === account.proxy_id)?.name || `#${account.proxy_id}`
}
function proxyTestResultLabel(proxyId: number): string {
  const result = proxyTestResults[proxyId]
  if (!result) return ''
  if (!result.success) return result.message || t('accountContributions.proxyTestFailed')
  const location = [result.country, result.region, result.city].filter(Boolean).join(' / ')
  return t('accountContributions.proxyTestResult', {
    ip: result.ip_address || '-',
    latency: result.latency_ms ?? 0,
    location: location || t('accountContributions.proxyLocationUnknown'),
  })
}
function accountInOwnRoom(accountID: number): boolean { return ownRoom.value?.accounts.some((account) => account.account_id === accountID) || false }
function healthLabel(account: ContributionRoomAccount) { return account.needs_attention ? t('accountContributions.needsAttention') : t('accountContributions.healthy') }
function healthClass(account: ContributionRoomAccount) { return account.needs_attention ? 'badge-danger' : 'badge-success' }

onMounted(() => { void refreshWorkspace() })
</script>

<style scoped>
.contribution-page {
  --work-accent: #0f9b8e;
  --work-ink: #14213d;
}

.contribution-card {
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.045);
}

.contribution-workspace-nav {
  box-shadow: 0 10px 28px rgb(15 23 42 / 0.045);
}

.workspace-nav-main {
  display: flex;
  min-height: 4.25rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.75rem 1rem;
}

.workspace-primary-tabs {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.375rem;
}

.workspace-primary-tabs button {
  display: inline-flex;
  height: 2.5rem;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 0 0.875rem;
  border: 1px solid transparent;
  border-radius: 0.375rem;
  color: #64748b;
  font-size: 0.875rem;
  font-weight: 650;
  white-space: nowrap;
  transition: background-color 160ms ease, border-color 160ms ease, color 160ms ease;
}

.workspace-primary-tabs button:hover { background: #f8fafc; color: #334155; }
.workspace-primary-tabs .workspace-primary-tab--active {
  border-color: rgb(13 148 136 / 0.18);
  background: #ecfdf5;
  color: #0f766e;
}

.workspace-count,
.workspace-subcount {
  display: inline-flex;
  min-width: 1.25rem;
  height: 1.25rem;
  align-items: center;
  justify-content: center;
  padding: 0 0.3125rem;
  border-radius: 999px;
  background: rgb(15 118 110 / 0.09);
  color: #0f766e;
  font-size: 0.625rem;
  font-weight: 750;
}

.workspace-summary {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: flex-end;
  gap: 0.75rem;
}

.workspace-wallet {
  min-width: 6rem;
  padding-left: 0.75rem;
  border-left: 2px solid #99f6e4;
}

.workspace-wallet span { display: block; color: #64748b; font-size: 0.625rem; line-height: 1.25; }
.workspace-wallet strong { display: block; margin-top: 0.125rem; color: #1e293b; font-size: 0.8125rem; font-variant-numeric: tabular-nums; }
.workspace-wallet--earned { border-left-color: #bfdbfe; }
.workspace-wallet--spent { border-left-color: #fde68a; }
.workspace-wallet--share-rate { border-left-color: #c4b5fd; }
.workspace-wallet--own-rate { border-left-color: #f9a8d4; }

.workspace-secondary-tabs {
  display: flex;
  min-height: 2.75rem;
  align-items: flex-end;
  gap: 1.25rem;
  padding: 0 1rem;
  border-top: 1px solid #e2e8f0;
}

.workspace-secondary-tabs button {
  position: relative;
  display: inline-flex;
  height: 2.75rem;
  align-items: center;
  gap: 0.375rem;
  border-bottom: 2px solid transparent;
  color: #64748b;
  font-size: 0.8125rem;
  font-weight: 550;
  white-space: nowrap;
}
.workspace-secondary-tabs button:hover:not(:disabled) { color: #334155; }
.workspace-secondary-tabs button:disabled { cursor: not-allowed; opacity: 0.42; }
.workspace-secondary-tabs .workspace-secondary-tab--active { border-bottom-color: #0f9b8e; color: #0f766e; font-weight: 700; }

.draft-dot {
  width: 0.375rem;
  height: 0.375rem;
  border-radius: 999px;
  background: #f59e0b;
  box-shadow: 0 0 0 2px rgb(245 158 11 / 0.12);
}

.contribution-panel-header {
  display: flex;
  min-height: 4.5rem;
  align-items: center;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid #e2e8f0;
}
.contribution-panel-header h2 { color: #0f172a; font-size: 1rem; font-weight: 700; }
.contribution-panel-header p { margin-top: 0.125rem; color: #64748b; font-size: 0.75rem; line-height: 1.45; }

.contribution-form-zone {
  background: rgb(248 250 252 / 0.72);
}

.mode-switch {
  max-width: 30rem;
}

.contribution-result {
  background: rgb(255 255 255 / 0.82);
}

.scheduling-settings {
  background: rgb(255 255 255 / 0.72);
}

.contribution-group-select {
  min-height: 7.5rem;
}

.contribution-usage-summary {
  background: rgb(248 250 252 / 0.72);
}

.usage-summary-grid > div {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 0.35rem;
  border-left: 2px solid rgb(14 165 233 / 0.5);
  background: rgb(255 255 255 / 0.78);
  padding: 0.8rem;
}

.usage-summary-grid > div:nth-child(2) { border-left-color: rgb(16 185 129 / 0.6); }
.usage-summary-grid > div:nth-child(3) { border-left-color: rgb(168 85 247 / 0.6); }
.usage-summary-grid span,
.usage-summary-grid small { color: rgb(100 116 139); font-size: 0.75rem; line-height: 1.25rem; }
.usage-summary-grid strong { color: var(--work-ink); font-size: 1rem; font-variant-numeric: tabular-nums; }

.section-icon {
  display: inline-flex;
  height: 2.25rem;
  width: 2.25rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid transparent;
  border-radius: 0.5rem;
}

.section-icon--room {
  border-color: rgb(13 148 136 / 0.2);
  background: rgb(240 253 250);
  color: #0f9b8e;
}

.scope-option {
  min-height: 2.25rem;
  padding: 0 0.75rem;
  color: #64748b;
  font-size: 0.8125rem;
  font-weight: 600;
}

.scope-option + .scope-option { border-left: 1px solid rgb(226 232 240); }
.scope-option:hover { background: rgb(248 250 252); color: #0f766e; }
.scope-option--active { background: rgb(240 253 250); color: #0f766e; }

.section-icon--account {
  border-color: rgb(37 99 235 / 0.18);
  background: rgb(239 246 255);
  color: #2563eb;
}

.section-icon--add {
  border-color: rgb(14 165 233 / 0.18);
  background: rgb(240 249 255);
  color: #0284c7;
}

.section-icon--proxy {
  border-color: rgb(14 165 233 / 0.2);
  background: rgb(240 249 255);
  color: #0369a1;
}

.proxy-management-layout {
  display: grid;
  grid-template-columns: minmax(18rem, 22rem) minmax(0, 1fr);
}

.proxy-editor {
  padding: 1.25rem;
  border-right: 1px solid #e2e8f0;
  background: rgb(248 250 252 / 0.72);
}

.proxy-address-feedback {
  display: flex;
  align-items: flex-start;
  gap: 0.375rem;
  margin-top: 0.5rem;
  font-size: 0.75rem;
  line-height: 1.45;
}

.proxy-address-feedback svg { margin-top: 0.0625rem; flex: 0 0 auto; }
.proxy-address-feedback--error { color: #b45309; }
.proxy-address-feedback--success { color: #047857; }

.proxy-manual-toggle {
  display: flex;
  width: 100%;
  min-height: 2.25rem;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.625rem;
  border: 1px solid #dbe4ee;
  border-radius: 0.375rem;
  background: white;
  color: #475569;
  font-size: 0.75rem;
  font-weight: 600;
}

.proxy-manual-toggle:hover { border-color: rgb(14 165 233 / 0.4); color: #0369a1; }

.proxy-manual-fields {
  padding-top: 0.25rem;
}

.proxy-list-zone {
  min-width: 0;
  padding: 1.25rem;
}

.proxy-list-row {
  display: flex;
  min-height: 6rem;
  align-items: center;
  gap: 1rem;
  padding: 1rem 0.25rem;
}

.proxy-empty-state {
  display: flex;
  min-height: 12rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  color: #94a3b8;
  font-size: 0.875rem;
}

.room-stat-grid > div {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  border-left: 2px solid rgb(203 213 225 / 0.82);
  background: rgb(248 250 252 / 0.8);
  padding: 0.85rem;
}

.room-stat-grid > div:first-child { border-left-color: rgb(16 185 129 / 0.65); }
.room-stat-grid > div:last-child { border-left-color: rgb(245 158 11 / 0.7); }

.room-settings {
  border-top: 1px solid rgb(226 232 240 / 0.9);
  padding-top: 1.25rem;
}

.room-create-flow {
  border-top: 1px solid rgb(226 232 240 / 0.9);
}

.room-create-section {
  padding: 1.25rem 0;
  border-bottom: 1px solid rgb(226 232 240 / 0.9);
}

.room-create-heading {
  display: flex;
  align-items: flex-start;
  gap: 0.75rem;
}

.room-create-heading > span {
  display: inline-flex;
  width: 1.75rem;
  height: 1.75rem;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(13 148 136 / 0.28);
  border-radius: 999px;
  background: rgb(240 253 250);
  color: #0f766e;
  font-size: 0.75rem;
  font-weight: 700;
}

.room-create-heading h3 { color: #0f172a; font-size: 0.875rem; font-weight: 650; }
.room-create-heading p { margin-top: 0.2rem; color: #64748b; font-size: 0.75rem; line-height: 1.5; }

.room-create-account-list {
  border-top: 1px solid #e2e8f0;
}

.room-create-account-row {
  display: grid;
  min-height: 5.25rem;
  grid-template-columns: auto minmax(0, 1fr) minmax(20rem, 28rem);
  align-items: center;
  gap: 0.875rem;
  padding: 0.875rem 0.25rem;
  border-bottom: 1px solid #e2e8f0;
  cursor: pointer;
  transition: background-color 160ms ease;
}

.room-create-account-row:hover,
.room-create-account-row--selected { background: rgb(240 253 250 / 0.62); }

.room-create-controls {
  display: grid;
  grid-template-columns: minmax(8rem, 0.8fr) minmax(11rem, 1.2fr);
  gap: 0.625rem;
}

.room-create-controls span {
  display: block;
  margin-bottom: 0.25rem;
  color: #64748b;
  font-size: 0.6875rem;
  font-weight: 600;
}

.room-create-controls .input { min-height: 2.35rem; font-size: 0.8125rem; }

.room-create-empty {
  display: flex;
  min-height: 10rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  color: #94a3b8;
  font-size: 0.8125rem;
}

.room-create-submit {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding-top: 1.25rem;
}

.room-create-submit p { color: #64748b; font-size: 0.75rem; }

.room-account-tabs {
  display: inline-flex;
  gap: 0.25rem;
  margin-top: 1.25rem;
  padding: 0.25rem;
  border-radius: 0.375rem;
  background: #f1f5f9;
}

.room-account-tabs button {
  display: inline-flex;
  height: 2rem;
  align-items: center;
  gap: 0.375rem;
  padding: 0 0.75rem;
  border-radius: 0.25rem;
  color: #64748b;
  font-size: 0.75rem;
  font-weight: 600;
}
.room-account-tabs button span {
  display: inline-flex;
  min-width: 1.125rem;
  height: 1.125rem;
  align-items: center;
  justify-content: center;
  padding: 0 0.25rem;
  border-radius: 999px;
  background: rgb(148 163 184 / 0.16);
  font-size: 0.625rem;
}
.room-account-tabs button:disabled { cursor: not-allowed; opacity: 0.42; }
.room-account-tabs .room-account-tab--active { background: white; color: #0f766e; box-shadow: 0 1px 2px rgb(15 23 42 / 0.08); }

.subsection-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.count-pill {
  display: inline-flex;
  min-width: 1.65rem;
  height: 1.65rem;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: rgb(15 155 142 / 0.1);
  color: #0f766e;
  font-size: 0.75rem;
  font-weight: 700;
}

.room-account-row,
.contributed-account-row {
  transition: background-color 160ms ease;
}

.room-account-row:hover,
.contributed-account-row:hover {
  background: rgb(248 250 252 / 0.78);
}

.contribution-icon-button {
  display: inline-flex;
  height: 2.5rem;
  width: 2.5rem;
  align-items: center;
  justify-content: center;
  border: 1px solid rgb(203 213 225 / 0.9);
  border-radius: 0.5rem;
  background: white;
  color: #475569;
  transition: border-color 160ms ease, background-color 160ms ease, color 160ms ease;
}

.contribution-icon-button:hover:not(:disabled) {
  border-color: rgb(13 148 136 / 0.45);
  background: rgb(240 253 250);
  color: #0f766e;
}

.contribution-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.contribution-icon-button--compact {
  height: 2.1rem;
  width: 2.1rem;
}

.contribution-icon-button--danger:hover:not(:disabled) {
  border-color: rgb(220 38 38 / 0.35);
  background: rgb(254 242 242);
  color: #dc2626;
}

:global(.dark .contribution-form-zone){ background: rgb(15 23 42 / 0.24); }
:global(.dark .room-settings){ border-color: rgb(71 85 105 / 0.55); }
:global(.dark .room-create-flow),
:global(.dark .room-create-section),
:global(.dark .room-create-account-list),
:global(.dark .room-create-account-row){ border-color: rgb(71 85 105 / 0.55); }
:global(.dark .room-create-heading > span){ border-color: rgb(45 212 191 / 0.25); background: rgb(19 78 74 / 0.35); color: #5eead4; }
:global(.dark .room-create-heading h3){ color: #f8fafc; }
:global(.dark .room-create-heading p),
:global(.dark .room-create-controls span),
:global(.dark .room-create-submit p){ color: #94a3b8; }
:global(.dark .room-create-account-row:hover),
:global(.dark .room-create-account-row--selected){ background: rgb(19 78 74 / 0.24); }
:global(.dark .workspace-secondary-tabs),
:global(.dark .contribution-panel-header){ border-color: rgb(71 85 105 / 0.55); }
:global(.dark .workspace-primary-tabs button:hover){ background: rgb(30 41 59 / 0.65); color: #e2e8f0; }
:global(.dark .workspace-primary-tabs .workspace-primary-tab--active){ border-color: rgb(45 212 191 / 0.2); background: rgb(19 78 74 / 0.35); color: #5eead4; }
:global(.dark .workspace-wallet span){ color: #94a3b8; }
:global(.dark .workspace-wallet strong),
:global(.dark .contribution-panel-header h2){ color: #f8fafc; }
:global(.dark .workspace-secondary-tabs button:hover:not(:disabled) ){ color: #e2e8f0; }
:global(.dark .workspace-secondary-tabs .workspace-secondary-tab--active){ color: #5eead4; }
:global(.dark .room-account-tabs){ background: rgb(30 41 59 / 0.75); }
:global(.dark .room-account-tabs .room-account-tab--active){ background: #334155; color: #5eead4; }
:global(.dark .contribution-result){ background: rgb(15 23 42 / 0.42); }
:global(.dark .scheduling-settings){ background: rgb(15 23 42 / 0.22); }
:global(.dark .contribution-usage-summary){ background: rgb(15 23 42 / 0.22); }
:global(.dark .usage-summary-grid > div){ background: rgb(30 41 59 / 0.58); }
:global(.dark .usage-summary-grid span),
:global(.dark .usage-summary-grid small){ color: rgb(148 163 184); }
:global(.dark .usage-summary-grid strong){ color: white; }
:global(.dark .section-icon--room){ border-color: rgb(45 212 191 / 0.2); background: rgb(19 78 74 / 0.28); color: #5eead4; }
:global(.dark .scope-option){ color: #cbd5e1; }
:global(.dark .scope-option + .scope-option){ border-color: rgb(71 85 105 / 0.7); }
:global(.dark .scope-option:hover),
:global(.dark .scope-option--active){ background: rgb(19 78 74 / 0.32); color: #5eead4; }
:global(.dark .section-icon--account){ border-color: rgb(96 165 250 / 0.2); background: rgb(30 58 138 / 0.22); color: #93c5fd; }
:global(.dark .section-icon--add){ border-color: rgb(56 189 248 / 0.2); background: rgb(12 74 110 / 0.25); color: #7dd3fc; }
:global(.dark .section-icon--proxy){ border-color: rgb(56 189 248 / 0.2); background: rgb(12 74 110 / 0.25); color: #7dd3fc; }
:global(.dark .proxy-editor){ border-color: rgb(71 85 105 / 0.55); background: rgb(15 23 42 / 0.24); }
:global(.dark .proxy-manual-toggle){ border-color: #334155; background: #172033; color: #cbd5e1; }
:global(.dark .proxy-manual-toggle:hover){ border-color: rgb(56 189 248 / 0.4); color: #7dd3fc; }
:global(.dark .proxy-address-feedback--error){ color: #fbbf24; }
:global(.dark .proxy-address-feedback--success){ color: #6ee7b7; }
:global(.dark .room-stat-grid > div),
:global(.dark .room-account-row:hover),
:global(.dark .contributed-account-row:hover){ background: rgb(30 41 59 / 0.58); }
:global(.dark .contribution-icon-button){ border-color: rgb(71 85 105 / 0.8); background: rgb(30 41 59); color: #cbd5e1; }
:global(.dark .contribution-icon-button:hover:not(:disabled) ){ background: rgb(19 78 74 / 0.4); color: #5eead4; }
:global(.dark .contribution-icon-button--danger:hover:not(:disabled) ){ background: rgb(127 29 29 / 0.25); color: #fca5a5; }

@media (max-width: 900px) {
  .workspace-nav-main { align-items: stretch; flex-direction: column; }
  .workspace-primary-tabs { width: 100%; }
  .workspace-primary-tabs button { flex: 1; }
  .workspace-summary { display: grid; width: 100%; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .workspace-wallet { min-width: 0; }
  .workspace-summary > .contribution-icon-button { justify-self: end; }
  .proxy-management-layout { grid-template-columns: minmax(0, 1fr); }
  .proxy-editor { border-right: 0; border-bottom: 1px solid #e2e8f0; }
}

@media (max-width: 640px) {
  .contribution-page { padding: 1rem; }
  .workspace-nav-main { padding: 0.75rem; }
  .workspace-primary-tabs button { padding: 0 0.625rem; font-size: 0.8125rem; }
  .workspace-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.625rem 0.375rem; }
  .workspace-wallet { padding-left: 0.375rem; }
  .workspace-wallet span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .room-create-account-row { grid-template-columns: auto minmax(0, 1fr); }
  .room-create-controls { grid-column: 2; width: 100%; grid-template-columns: minmax(0, 1fr); }
  .room-create-submit { align-items: stretch; flex-direction: column; }
  .room-create-submit .btn { width: 100%; justify-content: center; }
  .workspace-wallet strong { font-size: 0.6875rem; }
  .workspace-secondary-tabs { overflow-x: auto; gap: 1rem; padding: 0 0.75rem; scrollbar-width: none; }
  .workspace-secondary-tabs::-webkit-scrollbar { display: none; }
  .contribution-panel-header { padding: 0.875rem 1rem; }
  .proxy-editor,
  .proxy-list-zone { padding: 1rem; }
  .proxy-list-row { align-items: flex-start; flex-direction: column; }
}
</style>
