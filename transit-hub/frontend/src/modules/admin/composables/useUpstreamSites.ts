import { computed, onBeforeUnmount, ref } from 'vue'
import {
  createUpstreamSite,
  listUpstreamSites,
  removeUpstreamSite,
  streamSyncAllUpstreamSites,
  syncAllUpstreamSites,
  syncUpstreamSite,
  updateUpstreamSite,
} from '../api/upstream'
import type { SiteSyncState, UpstreamMetricValue, UpstreamMetrics, UpstreamSite, UpstreamSiteForm, UpstreamSiteResponse } from '../types/upstream'

const logoClasses = [
  'bg-primary/10 text-primary border-primary/20',
  'bg-accent/10 text-accent border-accent/20',
  'bg-warning/10 text-warning border-warning/20',
  'bg-signal/10 text-signal border-signal/20',
]

const createSiteId = (): string => {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

const siteLogo = (name: string): string => name.trim().charAt(0).toUpperCase() || 'U'

const emptyMetric = (): UpstreamMetricValue => ({ value: null, display: '-' })

const normalizeMetrics = (metrics: UpstreamSiteResponse['metrics'] | null | undefined): UpstreamMetrics => ({
  balance: metrics?.balance ?? emptyMetric(),
  todayConsume: metrics?.todayConsume ?? emptyMetric(),
  historyRecharge: metrics?.historyRecharge ?? emptyMetric(),
  group: metrics?.group ?? { id: '', name: '-', platform: null, multiplier: null, multiplierDisplay: '-' },
  groups: Array.isArray(metrics?.groups) ? metrics.groups : [],
})

const normalizeSite = (site: UpstreamSiteResponse, logoBg: string): UpstreamSite => ({
  ...site,
  metrics: normalizeMetrics(site.metrics),
  settings: site.settings ?? { balanceThreshold: null, manualAccountingEnabled: false, manualGroupMultipliers: {} },
  logo: siteLogo(site.name),
  logoBg,
})

export const useUpstreamSites = () => {
  const sites = ref<UpstreamSite[]>([])
  const isAdding = ref(false)
  const isRefreshing = ref(false)
  const addErrorKey = ref<string | null>(null)

  const connectedCount = computed(() => sites.value.filter((site) => site.status === 'connected' || site.status === 'syncing').length)

  const loadSites = async () => {
    const remoteSites = await listUpstreamSites()
    sites.value = remoteSites.map((site, index) => normalizeSite(site, logoClasses[index % logoClasses.length]))
  }

  const addSite = async (form: UpstreamSiteForm): Promise<boolean> => {
    isAdding.value = true
    addErrorKey.value = null
    try {
      const site = await createUpstreamSite(form)
      sites.value.unshift(normalizeSite(site, logoClasses[sites.value.length % logoClasses.length]))
      return true
    } catch (error) {
      addErrorKey.value = error instanceof Error ? error.message : 'admin.upstream.errors.unknown'
      return false
    } finally {
      isAdding.value = false
    }
  }

  const updateSite = async (id: string, form: UpstreamSiteForm): Promise<boolean> => {
    isAdding.value = true
    addErrorKey.value = null
    try {
      const nextSite = await updateUpstreamSite(id, form)
      const index = sites.value.findIndex((site) => site.id === id)
      if (index >= 0) {
        sites.value[index] = normalizeSite(nextSite, sites.value[index].logoBg)
      }
      return true
    } catch (error) {
      addErrorKey.value = error instanceof Error ? error.message : 'admin.upstream.errors.unknown'
      return false
    } finally {
      isAdding.value = false
    }
  }

  const syncSite = async (id: string) => {
    const site = sites.value.find((item) => item.id === id)
    if (!site) return
    const nextSite = await syncUpstreamSite(id)
    Object.assign(site, normalizeSite(nextSite, site.logoBg))
  }

  const refreshSites = async () => {
    if (isRefreshing.value) return
    isRefreshing.value = true
    try {
      const remoteSites = await syncAllUpstreamSites()
      sites.value = remoteSites.map((site, index) => {
        const current = sites.value.find((item) => item.id === site.id)
        return normalizeSite(site, current?.logoBg ?? logoClasses[index % logoClasses.length])
      })
    } finally {
      isRefreshing.value = false
    }
  }

  // 逐站流式同步状态：每个站点 ID 映射到当前同步阶段。
  const siteSyncStates = ref(new Map<string, SiteSyncState>())
  const syncingSiteIds = ref(new Set<string>())

  const refreshSingleSite = async (id: string) => {
    if (syncingSiteIds.value.has(id)) return
    syncingSiteIds.value = new Set([...syncingSiteIds.value, id])
    try {
      await syncSite(id)
    } finally {
      const next = new Set(syncingSiteIds.value)
      next.delete(id)
      syncingSiteIds.value = next
    }
  }

  const streamRefreshSites = async () => {
    if (isRefreshing.value) return
    isRefreshing.value = true
    siteSyncStates.value = new Map()

    try {
      await streamSyncAllUpstreamSites((event) => {
        const id = event.siteId
        switch (event.event) {
          case 'syncing':
            siteSyncStates.value.set(id, { phase: 'syncing' })
            break
          case 'done':
            if (event.site) {
              const index = sites.value.findIndex((s) => s.id === id)
              if (index >= 0) {
                sites.value[index] = normalizeSite(event.site, sites.value[index].logoBg)
              }
            }
            siteSyncStates.value.set(id, { phase: 'done' })
            setTimeout(() => {
              if (siteSyncStates.value.get(id)?.phase === 'done') {
                siteSyncStates.value.delete(id)
              }
            }, 2000)
            break
          case 'error':
            if (event.site) {
              const index = sites.value.findIndex((s) => s.id === id)
              if (index >= 0) {
                sites.value[index] = normalizeSite(event.site, sites.value[index].logoBg)
              }
            }
            siteSyncStates.value.set(id, { phase: 'error', errorKey: event.errorKey })
            break
          case 'complete':
            isRefreshing.value = false
            break
        }
      })
    } catch {
      // 连接中断时清理状态。
    } finally {
      isRefreshing.value = false
    }
  }

  const deleteSite = async (id: string) => {
    await removeUpstreamSite(id)
    sites.value = sites.value.filter((site) => site.id !== id)
  }

  void loadSites()

  onBeforeUnmount(() => {
    // no-op; backend now owns refresh scheduling
  })

  return {
    sites,
    isAdding,
    isRefreshing,
    addErrorKey,
    connectedCount,
    siteSyncStates,
    syncingSiteIds,
    addSite,
    updateSite,
    syncSite,
    refreshSingleSite,
    refreshSites,
    streamRefreshSites,
    deleteSite,
    loadSites,
  }
}
