import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import RelayNodesView from '../RelayNodesView.vue'

const { api, stepUpRun, showError, showSuccess } = vi.hoisted(() => ({
  api: {
    getStatus: vi.fn(),
    setEnabled: vi.fn(),
    getGeneralConfig: vi.fn(),
    updateGeneralConfig: vi.fn(),
    listNodes: vi.fn(),
    nodeHealths: vi.fn(),
    checkNodeDomain: vi.fn(),
    activateNode: vi.fn(),
    rejectNode: vi.fn(),
    rejectAllPending: vi.fn(),
    drainNode: vi.fn(),
    undrainNode: vi.fn(),
    disableNode: vi.fn(),
    enableNode: vi.fn(),
    revokeNode: vi.fn(),
    setNodeAllowMultiIP: vi.fn(),
    reclaimNodeQuota: vi.fn(),
    reclaimUserQuota: vi.fn(),
    replaceNode: vi.fn(),
    updateNodeDomain: vi.fn(),
    keyAssignmentSummary: vi.fn(),
    assignUnassignedKeys: vi.fn(),
    moveKeys: vi.fn(),
    moveNodeKeys: vi.fn(),
    userAssignmentSummary: vi.fn(),
    rebalanceUsers: vi.fn(),
    moveUser: vi.fn(),
    pinUser: vi.fn(),
    unpinUser: vi.fn(),
    getNotifications: vi.fn(),
    updateNotifications: vi.fn(),
    testNotification: vi.fn(),
    nodeLogs: vi.fn(),
    allNodeLogs: vi.fn(),
    listKeys: vi.fn(),
    stageKey: vi.fn(),
    activateKey: vi.fn(),
    retireKey: vi.fn()
  },
  stepUpRun: vi.fn((action: () => Promise<unknown>) => action()),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { relay: api } }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess, showInfo: vi.fn() })
}))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: stepUpRun }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => ''
}))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key) })
}))

const FP = 'ab12cd34'

function node(over: Record<string, unknown>) {
  return {
    id: 1,
    name: 'relay-a',
    hostname: 'host-a',
    region: '',
    public_domain: 'a.example.com',
    status: 'active',
    identity_fingerprint: FP,
    registered_ip: '203.0.113.5',
    program_version: '1.0.0',
    bandwidth_limit_mbps: 100,
    allow_multi_ip: false,
    last_seen_ip: '203.0.113.5',
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...over
  }
}

function health(id: number, over: Record<string, unknown> = {}) {
  return {
    node_id: id,
    online: true,
    load_percent: 12,
    clock_skew_ms: 0,
    voucher_shortfall: 0,
    heartbeat: { requests_1m: 30, errors_1m: 1, rx_bytes_per_sec: 125000, tx_bytes_per_sec: 250000 },
    external: { probe_checked: true, probe_ok: true, probe_failures: 0, unreachable: false, dns: 'node', degraded: false },
    ...over
  }
}

const dialogStub = { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }

function mountView() {
  return mount(RelayNodesView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: dialogStub,
        Icon: true,
        TotpStepUpDialog: true
      }
    }
  })
}

async function mounted() {
  const wrapper = mountView()
  await flushPromises()
  return wrapper
}

function buttonByText(wrapper: ReturnType<typeof mountView>, text: string) {
  const b = wrapper.findAll('button').find((x) => x.text() === text)
  if (!b) throw new Error(`no button "${text}"`)
  return b
}

describe('从节点管理页', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    stepUpRun.mockImplementation((action: () => Promise<unknown>) => action())
    api.getStatus.mockResolvedValue({ enabled: true, runtime: { state: 'running', since: '2026-10-01T00:00:00Z' } })
    api.listNodes.mockResolvedValue([
      node({ id: 1 }),
      node({ id: 2, name: 'relay-b', status: 'pending', public_domain: '', registered_ip: '203.0.113.9' })
    ])
    api.nodeHealths.mockResolvedValue([health(1)])
    api.getGeneralConfig.mockResolvedValue({ master_ratio_percent: 10, api_key_node_rule: 'assigned' })
    api.keyAssignmentSummary.mockResolvedValue({ unassigned: 3, master: 5, nodes: { '1': { total: 7, active: 4 } } })
    api.userAssignmentSummary.mockResolvedValue({ '0': 2, '1': 6 })
    api.getNotifications.mockResolvedValue({
      feishu_enabled: false,
      webhook_configured: false,
      secret_configured: false,
      email_enabled: true,
      merge_window_seconds: 15,
      events: [{ kind: 'node_offline', title: '从节点离线', severity: 'critical', default_enabled: true, enabled: true, feishu: true, email: true }]
    })
    api.activateNode.mockResolvedValue(undefined)
    api.updateNodeDomain.mockResolvedValue(undefined)
    api.drainNode.mockResolvedValue(undefined)
    api.disableNode.mockResolvedValue(undefined)
    api.checkNodeDomain.mockResolvedValue({ state: 'other', resolved: ['198.51.100.1'], expected: '203.0.113.9' })
    api.setEnabled.mockResolvedValue({ enabled: false, runtime: { state: 'off', since: '2026-10-01T00:00:00Z' } })
  })

  it('列出主节点行和各节点，并按状态给出可用操作', async () => {
    const wrapper = await mounted()

    expect(wrapper.find('[data-test="master-row"]').exists()).toBe(true)
    // 主节点分到的 Key 和用户数
    expect(wrapper.get('[data-test="master-row"]').text()).toContain('"keys":5')
    expect(wrapper.get('[data-test="master-row"]').text()).toContain('"users":2')

    const active = wrapper.get('[data-test="node-1"]')
    expect(active.find('[data-test="drain"]').exists()).toBe(true)
    expect(active.find('[data-test="disable"]').exists()).toBe(true)
    expect(active.find('[data-test="activate"]').exists()).toBe(false)

    const pending = wrapper.get('[data-test="node-2"]')
    expect(pending.find('[data-test="activate"]').exists()).toBe(true)
    expect(pending.find('[data-test="reject"]').exists()).toBe(true)
    expect(pending.find('[data-test="drain"]').exists()).toBe(false)
  })

  it('排空要先确认，确认后通过二次验证执行并刷新', async () => {
    const wrapper = await mounted()
    api.listNodes.mockClear()

    await wrapper.get('[data-test="node-1"] [data-test="drain"]').trigger('click')
    expect(api.drainNode).not.toHaveBeenCalled()

    await buttonByText(wrapper, 'common.confirm').trigger('click')
    await flushPromises()

    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(api.drainNode).toHaveBeenCalledWith(1)
    expect(showSuccess).toHaveBeenCalled()
    expect(api.listNodes).toHaveBeenCalled()
  })

  it('从节点列表可以修改域名，并要求确认未匹配的 DNS', async () => {
    const wrapper = await mounted()
    await wrapper.get('[data-test="node-1"] [data-test="edit-domain"]').trigger('click')
    await wrapper.get('[data-test="edit-domain-input"]').setValue('relay-new.example.com')
    await wrapper.get('[data-test="edit-domain-check"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="edit-domain-ignore-dns"]').exists()).toBe(true)
    await wrapper.get('[data-test="edit-domain-ignore-dns"]').setValue(true)
    await wrapper.get('[data-test="edit-domain-submit"]').trigger('click')
    await flushPromises()

    expect(api.updateNodeDomain).toHaveBeenCalledWith(1, {
      public_domain: 'relay-new.example.com',
      ignore_dns_mismatch: true
    })
  })

  it('激活：域名没有解析到这台时必须明确勾选才能提交，成功后关闭对话框', async () => {
    const wrapper = await mounted()
    await wrapper.get('[data-test="node-2"] [data-test="activate"]').trigger('click')

    const submit = () => wrapper.get('[data-test="activate-submit"]')
    expect(submit().attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="fingerprint"]').setValue(FP)
    await wrapper.get('[data-test="domain"]').setValue('b.example.com')
    expect(submit().attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-test="check-domain"]').trigger('click')
    await flushPromises()
    expect(api.checkNodeDomain).toHaveBeenCalledWith(2, 'b.example.com')
    expect(wrapper.find('[data-test="domain-check-result"]').exists()).toBe(true)
    expect(submit().attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="ignore-dns"]').setValue(true)
    expect(submit().attributes('disabled')).toBeUndefined()

    await submit().trigger('click')
    await flushPromises()

    expect(api.activateNode).toHaveBeenCalledWith(2, {
      fingerprint: FP,
      name: 'relay-b',
      public_domain: 'b.example.com',
      bandwidth_limit_mbps: 100,
      region: '',
      ignore_dns_mismatch: true
    })
    // 返回空值的接口成功后对话框也要关闭
    expect(wrapper.find('[data-test="activate-form"]').exists()).toBe(false)
  })

  it('激活失败时留在对话框里并提示错误', async () => {
    api.activateNode.mockRejectedValue({ message: 'fingerprint does not match' })
    const wrapper = await mounted()
    await wrapper.get('[data-test="node-2"] [data-test="activate"]').trigger('click')
    await wrapper.get('[data-test="fingerprint"]').setValue('wrong')
    await wrapper.get('[data-test="domain"]').setValue('b.example.com')
    await wrapper.get('[data-test="activate-submit"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('fingerprint does not match')
    expect(wrapper.find('[data-test="activate-form"]').exists()).toBe(true)
  })

  it('健康徽标：不可达、降级、时钟偏差、证书将到期、扣费缺口', async () => {
    const soon = new Date(Date.now() + 3 * 86_400_000).toISOString()
    api.nodeHealths.mockResolvedValue([
      health(1, {
        clock_skew_ms: 45_000,
        voucher_shortfall: 2,
        external: {
          probe_checked: true,
          probe_ok: false,
          probe_failures: 3,
          probe_error: 'handshake failed',
          unreachable: true,
          degraded: true,
          dns: 'other',
          dns_resolved: ['198.51.100.7'],
          cert_not_after: soon
        }
      })
    ])
    const wrapper = await mounted()
    const text = wrapper.get('[data-test="node-1"]').text()

    expect(text).toContain('admin.relay.health.unreachable')
    expect(text).toContain('admin.relay.health.degraded')
    expect(text).toContain('admin.relay.health.clockSkew')
    expect(text).toContain('admin.relay.health.shortfall')
    expect(text).toContain('198.51.100.7')
    expect(text).toContain('handshake failed')
    expect(text).toContain('"days":2')
  })

  it('总开关关不掉时刷新状态让开关回到原位', async () => {
    api.setEnabled.mockRejectedValue({ message: 'relay nodes are still active or draining' })
    const wrapper = await mounted()
    api.getStatus.mockClear()

    await wrapper.get('[data-test="enabled-toggle"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('relay nodes are still active or draining')
    expect(api.getStatus).toHaveBeenCalled()
  })

  it('通用配置：主节点比例改成 0 时提示还在主节点上的 Key 和用户', async () => {
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-config"]').trigger('click')

    expect(wrapper.find('[data-test="ratio-zero-warning"]').exists()).toBe(false)
    await wrapper.get('[data-test="master-ratio"]').setValue('0')

    const warning = wrapper.get('[data-test="ratio-zero-warning"]')
    expect(warning.text()).toContain('"keys":5')
    expect(warning.text()).toContain('"users":2')
  })

  it('主节点比例改成 0：主节点上还有 Key 和用户时先确认，取消不保存，确认才保存', async () => {
    api.updateGeneralConfig.mockResolvedValue({ master_ratio_percent: 0 })
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="master-ratio"]').setValue('0')

    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()
    expect(api.updateGeneralConfig).not.toHaveBeenCalled()

    // 取消：什么都不保存
    await buttonByText(wrapper, 'common.cancel').trigger('click')
    await flushPromises()
    expect(api.updateGeneralConfig).not.toHaveBeenCalled()

    await wrapper.get('[data-test="save-config"]').trigger('click')
    await buttonByText(wrapper, 'common.confirm').trigger('click')
    await flushPromises()
    expect(api.updateGeneralConfig).toHaveBeenCalledTimes(1)
    expect(api.updateGeneralConfig.mock.calls[0][0].master_ratio_percent).toBe(0)
  })

  it('主节点比例改成 0：主节点上已经没有 Key 和用户时直接保存', async () => {
    api.keyAssignmentSummary.mockResolvedValue({ unassigned: 0, master: 0, nodes: { '1': { total: 7, active: 4 } } })
    api.userAssignmentSummary.mockResolvedValue({ '1': 6 })
    api.updateGeneralConfig.mockResolvedValue({ master_ratio_percent: 0 })
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-config"]').trigger('click')
    await wrapper.get('[data-test="master-ratio"]').setValue('0')
    await wrapper.get('[data-test="save-config"]').trigger('click')
    await flushPromises()

    expect(api.updateGeneralConfig).toHaveBeenCalledTimes(1)
  })

  it('吊销节点后提醒更换审核、提示词审计、联网搜索的密钥', async () => {
    api.revokeNode.mockResolvedValue(undefined)
    const wrapper = await mounted()
    await wrapper.get('[data-test="node-1"] [data-test="revoke"]').trigger('click')
    await wrapper.get('[data-test="revoke-reason"]').setValue('suspected compromise')
    await wrapper.get('[data-test="revoke-submit"]').trigger('click')
    await flushPromises()

    expect(api.revokeNode).toHaveBeenCalledWith(1, 'suspected compromise')
    const followup = wrapper.get('[data-test="revoke-followup"]').text()
    expect(followup).toContain('admin.relay.revoke.followupModeration')
    expect(followup).toContain('admin.relay.revoke.followupPromptAudit')
    expect(followup).toContain('admin.relay.revoke.followupWebSearch')
  })

  it('分配页：提示未分配的 Key，并能一键分配', async () => {
    api.assignUnassignedKeys.mockResolvedValue({ assigned: 3, left: 0 })
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-assignment"]').trigger('click')

    expect(wrapper.get('[data-test="unassigned-notice"]').text()).toContain('"count":3')
    await wrapper.get('[data-test="assign-unassigned"]').trigger('click')
    await flushPromises()
    expect(api.assignUnassignedKeys).toHaveBeenCalledTimes(1)
  })

  it('日志页：离线的节点单独标出，不会被悄悄丢掉', async () => {
    api.allNodeLogs.mockResolvedValue({
      nodes: [
        { node_id: 1, node_name: 'relay-a', status: 'ok', records: [{}] },
        { node_id: 3, node_name: 'relay-c', status: 'offline', records: [] }
      ],
      records: [{ node_id: 1, node_name: 'relay-a', ts: 1_790_000_000_000, record: { level: 'warn', message: 'upstream slow' } }]
    })
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-logs"]').trigger('click')
    await wrapper.get('[data-test="search"]').trigger('click')
    await flushPromises()

    const outcomes = wrapper.get('[data-test="node-outcomes"]').text()
    expect(outcomes).toContain('relay-c')
    expect(outcomes).toContain('admin.relay.logs.nodeStatus.offline')
    expect(wrapper.text()).toContain('upstream slow')
  })

  it('通知页：只提交改动过的事件', async () => {
    api.updateNotifications.mockResolvedValue({
      feishu_enabled: false,
      webhook_configured: false,
      secret_configured: false,
      email_enabled: true,
      merge_window_seconds: 15,
      events: []
    })
    const wrapper = await mounted()
    await wrapper.get('[data-test="tab-notifications"]').trigger('click')
    await wrapper.get('[data-test="enabled-node_offline"]').setValue(false)
    await wrapper.get('[data-test="save-notifications"]').trigger('click')
    await flushPromises()

    expect(api.updateNotifications).toHaveBeenCalledTimes(1)
    const sent = api.updateNotifications.mock.calls[0][0]
    expect(sent.events).toEqual({ node_offline: { enabled: false, feishu: true, email: true } })
    // 没有填地址和密钥就不能带这两项（否则会把已保存的清掉）
    expect('webhook_url' in sent).toBe(false)
    expect('secret' in sent).toBe(false)
  })
})
