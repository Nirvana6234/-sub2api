<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">访问黑名单</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按用户账户 ID、IP 或 CIDR 拦截 API 请求。</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button>
      </div>

      <div v-if="errorMessage" class="rounded border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300">
        {{ errorMessage }}
      </div>

      <form class="card grid gap-3 p-5 md:grid-cols-[140px_minmax(0,1fr)_minmax(0,1fr)_auto] md:items-end" @submit.prevent="add">
        <label class="block text-sm text-gray-600 dark:text-gray-300">类型
          <select v-model="form.kind" class="input mt-1 w-full">
            <option value="account">账户 ID</option>
            <option value="ip">IP / CIDR</option>
          </select>
        </label>
        <label class="block text-sm text-gray-600 dark:text-gray-300">值
          <input v-model.trim="form.value" class="input mt-1 w-full" required :placeholder="form.kind === 'ip' ? '203.0.113.0/24' : '用户 ID，例如 42'" />
        </label>
        <label class="block text-sm text-gray-600 dark:text-gray-300">原因（可选）
          <input v-model.trim="form.reason" class="input mt-1 w-full" placeholder="滥用、异常流量等" />
        </label>
        <button class="btn btn-danger" type="submit" :disabled="saving">{{ saving ? '添加中...' : '加入黑名单' }}</button>
      </form>

      <div class="card overflow-hidden">
        <div v-if="loading" class="p-8 text-center text-sm text-gray-500">加载中...</div>
        <div v-else-if="entries.length === 0" class="p-8 text-center text-sm text-gray-500">暂无黑名单条目</div>
        <table v-else class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-800"><tr>
            <th class="px-5 py-3 text-left text-xs font-medium text-gray-500">类型</th>
            <th class="px-5 py-3 text-left text-xs font-medium text-gray-500">值</th>
            <th class="px-5 py-3 text-left text-xs font-medium text-gray-500">原因</th>
            <th class="px-5 py-3 text-right text-xs font-medium text-gray-500">操作</th>
          </tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="entry in entries" :key="entry.id">
              <td class="px-5 py-3 text-sm text-gray-700 dark:text-gray-200">{{ entry.kind === 'ip' ? 'IP / CIDR' : '账户 ID' }}</td>
              <td class="px-5 py-3 font-mono text-sm text-gray-900 dark:text-white">{{ entry.value }}</td>
              <td class="px-5 py-3 text-sm text-gray-500">{{ entry.reason || '-' }}</td>
              <td class="px-5 py-3 text-right"><button class="btn btn-danger btn-sm" @click="remove(entry.id)">移除</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { addGlobalBlacklist, deleteGlobalBlacklist, getGlobalBlacklist, type GlobalBlacklistEntry, type GlobalBlacklistKind } from '@/api/admin/settings'

const entries = ref<GlobalBlacklistEntry[]>([])
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const form = reactive<{ kind: GlobalBlacklistKind; value: string; reason: string }>({ kind: 'ip', value: '', reason: '' })

async function load() {
  loading.value = true
  errorMessage.value = ''
  try {
    entries.value = await getGlobalBlacklist()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '加载黑名单失败，请刷新重试'
  } finally {
    loading.value = false
  }
}
async function add() {
  saving.value = true
  errorMessage.value = ''
  try {
    await addGlobalBlacklist({ kind: form.kind, value: form.value, reason: form.reason || undefined })
    form.value = ''; form.reason = ''; await load()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '添加黑名单失败，请重试'
  } finally {
    saving.value = false
  }
}
async function remove(id: string) {
  if (!window.confirm('确定移除此黑名单条目吗？')) return
  errorMessage.value = ''
  try {
    await deleteGlobalBlacklist(id)
    await load()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '移除黑名单失败，请重试'
  }
}
onMounted(load)
</script>
