import { ref } from 'vue'
import {
  cancelGroupRateCampaign,
  endGroupRateCampaign,
  listGroupRateCampaigns,
  startGroupRateCampaign,
} from '../api/groupRateCampaigns'
import type { CampaignListItem, CampaignNotifyDefaults } from '../types/groupRateCampaigns'

export const useGroupRateCampaigns = () => {
  const campaigns = ref<CampaignListItem[]>([])
  const total = ref(0)
  const page = ref(1)
  const pageSize = ref(10)
  const totalPages = ref(1)
  const statusFilter = ref('')
  const notifyDefaults = ref<CampaignNotifyDefaults | null>(null)
  const isLoading = ref(false)
  const isActionLoading = ref(false)
  const errorKey = ref<string | null>(null)

  const loadCampaigns = async () => {
    isLoading.value = true
    errorKey.value = null
    try {
      const response = await listGroupRateCampaigns({
        page: page.value,
        pageSize: pageSize.value,
        status: statusFilter.value,
      })

      campaigns.value = response.items
      total.value = response.total
      page.value = response.page
      pageSize.value = response.pageSize
      totalPages.value = response.totalPages
      notifyDefaults.value = response.defaults
    } catch (error) {
      errorKey.value = error instanceof Error ? error.message : 'admin.groupRateCampaigns.errors.unknown'
    } finally {
      isLoading.value = false
    }
  }

  const resetPageAndLoadCampaigns = async () => {
    page.value = 1
    await loadCampaigns()
  }

  const setStatusFilter = async (value: string) => {
    statusFilter.value = value
    await resetPageAndLoadCampaigns()
  }

  const goToPage = async (targetPage: number) => {
    const nextPage = Math.min(Math.max(targetPage, 1), totalPages.value || 1)
    if (nextPage === page.value) return

    page.value = nextPage
    await loadCampaigns()
  }

  const startCampaign = async (id: string) => {
    isActionLoading.value = true
    errorKey.value = null
    try {
      await startGroupRateCampaign(id)
      await loadCampaigns()
    } catch (error) {
      errorKey.value = error instanceof Error ? error.message : 'admin.groupRateCampaigns.errors.unknown'
      throw error
    } finally {
      isActionLoading.value = false
    }
  }

  const endCampaign = async (id: string) => {
    isActionLoading.value = true
    errorKey.value = null
    try {
      await endGroupRateCampaign(id)
      await loadCampaigns()
    } catch (error) {
      errorKey.value = error instanceof Error ? error.message : 'admin.groupRateCampaigns.errors.unknown'
      throw error
    } finally {
      isActionLoading.value = false
    }
  }

  const cancelCampaign = async (id: string) => {
    isActionLoading.value = true
    errorKey.value = null
    try {
      await cancelGroupRateCampaign(id)
      await loadCampaigns()
    } catch (error) {
      errorKey.value = error instanceof Error ? error.message : 'admin.groupRateCampaigns.errors.unknown'
      throw error
    } finally {
      isActionLoading.value = false
    }
  }

  void loadCampaigns()

  return {
    campaigns,
    total,
    page,
    pageSize,
    totalPages,
    statusFilter,
    notifyDefaults,
    isLoading,
    isActionLoading,
    errorKey,
    loadCampaigns,
    setStatusFilter,
    goToPage,
    startCampaign,
    endCampaign,
    cancelCampaign,
  }
}
