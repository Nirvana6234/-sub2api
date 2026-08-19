import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

// 事故复盘：后台"参与调度"开关点击后台会把 schedulability_source 焊死成
// manual/admin_disabled，系统从此永不自动恢复该账号，直到管理员再手动打开。
// 之前这个开关是纯 @click 直接调 API，没有任何提示——一次"开一下试试、马上关掉"
// 的随手操作，就让账号 120 掉进了自动恢复黑洞。本测试锁定：关闭调度前必须先弹
// ConfirmDialog，取消则不调用 API；打开调度不受影响，照常直接调用。
const {
  listAccounts,
  listWithEtag,
  getBatchTodayStats,
  getAllProxies,
  getAllGroups,
  setSchedulable,
  bulkUpdate,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  setSchedulable: vi.fn(),
  bulkUpdate: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      setSchedulable,
      bulkUpdate,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const mountViewWithRow = () =>
  mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: {
          props: ['data', 'columns', 'loading'],
          template: `<div>
            <div v-for="(row, idx) in (data || [])" :key="idx">
              <slot name="cell-schedulable" :row="row" />
            </div>
          </div>`
        },
        Pagination: true,
        AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
        AccountTableFilters: { template: '<div></div>' },
        AccountBulkActionsBar: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        Icon: true
      }
    }
  })

describe('admin AccountsView — 关闭调度前二次确认', () => {
  beforeEach(() => {
    localStorage.clear()
    for (const fn of [listAccounts, listWithEtag, getBatchTodayStats, getAllProxies, getAllGroups, setSchedulable, bulkUpdate, showSuccess, showError]) {
      fn.mockReset()
    }
    listWithEtag.mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('点击关闭已开启调度的账号先弹确认框，不立即调用 API', async () => {
    listAccounts.mockResolvedValue({
      items: [{ id: 120, name: '订阅-147-https://www.mcgrox.top', platform: 'openai', type: 'apikey', schedulable: true }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = mountViewWithRow()
    await flushPromises()

    await wrapper.find('button[title="admin.accounts.schedulableEnabled"]').trigger('click')
    await flushPromises()

    expect(setSchedulable).not.toHaveBeenCalled()
    const dialog = wrapper.findAllComponents(ConfirmDialog).find(d => d.props('show'))
    expect(dialog).toBeTruthy()
    wrapper.unmount()
  })

  it('确认关闭后才调用 setSchedulable(id, false)', async () => {
    setSchedulable.mockResolvedValue({ id: 120, schedulable: false })
    listAccounts.mockResolvedValue({
      items: [{ id: 120, name: '订阅-147-https://www.mcgrox.top', platform: 'openai', type: 'apikey', schedulable: true }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = mountViewWithRow()
    await flushPromises()

    await wrapper.find('button[title="admin.accounts.schedulableEnabled"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.findAllComponents(ConfirmDialog).find(d => d.props('show'))
    dialog?.vm.$emit('confirm')
    await flushPromises()

    expect(setSchedulable).toHaveBeenCalledTimes(1)
    expect(setSchedulable).toHaveBeenCalledWith(120, false)
    wrapper.unmount()
  })

  it('取消确认框不调用 setSchedulable', async () => {
    listAccounts.mockResolvedValue({
      items: [{ id: 120, name: '订阅-147-https://www.mcgrox.top', platform: 'openai', type: 'apikey', schedulable: true }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = mountViewWithRow()
    await flushPromises()

    await wrapper.find('button[title="admin.accounts.schedulableEnabled"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.findAllComponents(ConfirmDialog).find(d => d.props('show'))
    dialog?.vm.$emit('cancel')
    await flushPromises()

    expect(setSchedulable).not.toHaveBeenCalled()
    // 取消后再次打开确认框应该仍然可用（状态被正确复位）
    await wrapper.find('button[title="admin.accounts.schedulableEnabled"]').trigger('click')
    await flushPromises()
    const dialogAgain = wrapper.findAllComponents(ConfirmDialog).find(d => d.props('show'))
    expect(dialogAgain).toBeTruthy()
    wrapper.unmount()
  })

  it('点击打开已关闭调度的账号不弹确认框，直接调用 API', async () => {
    setSchedulable.mockResolvedValue({ id: 120, schedulable: true })
    listAccounts.mockResolvedValue({
      items: [{ id: 120, name: '订阅-147-https://www.mcgrox.top', platform: 'openai', type: 'apikey', schedulable: false }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = mountViewWithRow()
    await flushPromises()

    await wrapper.find('button[title="admin.accounts.schedulableDisabled"]').trigger('click')
    await flushPromises()

    expect(setSchedulable).toHaveBeenCalledTimes(1)
    expect(setSchedulable).toHaveBeenCalledWith(120, true)
    const dialog = wrapper.findAllComponents(ConfirmDialog).find(d => d.props('show'))
    expect(dialog).toBeFalsy()
    wrapper.unmount()
  })
})
