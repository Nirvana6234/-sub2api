import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import ContributionUsageModal from '../ContributionUsageModal.vue'

const { queryMock, getStatsMock, showErrorMock } = vi.hoisted(() => ({
  queryMock: vi.fn(),
  getStatsMock: vi.fn(),
  showErrorMock: vi.fn(),
}))

vi.mock('@/api', () => ({
  usageAPI: {
    query: queryMock,
    getStats: getStatsMock,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: showErrorMock }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const SelectStub = defineComponent({
  name: 'Select',
  props: {
    modelValue: { type: [String, Number, Boolean, null], default: '' },
    options: { type: Array, default: () => [] },
  },
  emits: ['update:modelValue', 'change'],
  template: `
    <select
      v-bind="$attrs"
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change', $event.target.value)"
    >
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  `,
})

const UsageTableStub = defineComponent({
  name: 'UsageTable',
  props: { data: { type: Array, default: () => [] } },
  template: '<div data-testid="usage-table-stub">{{ data.length }}</div>',
})

function mountModal(account: { id: number; name: string } | null = { id: 795, name: 'kklt' }) {
  return mount(ContributionUsageModal, {
    props: { show: true, account },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        UsageTable: UsageTableStub,
        Pagination: true,
      },
    },
  })
}

describe('ContributionUsageModal', () => {
  beforeEach(() => {
    queryMock.mockReset()
    getStatsMock.mockReset()
    showErrorMock.mockReset()
    queryMock.mockResolvedValue({ items: [{ id: 1 }, { id: 2 }], total: 2, page: 1, page_size: 20, pages: 1 })
    getStatsMock.mockResolvedValue({ total_requests: 12, total_tokens: 3400, total_actual_cost: 0.5 })
  })

  it('queries the list and stats scoped to the account on open', async () => {
    const wrapper = mountModal()
    await flushPromises()

    expect(queryMock).toHaveBeenCalledTimes(1)
    expect(queryMock).toHaveBeenCalledWith(
      expect.objectContaining({ account_id: 795, page: 1, page_size: 20, start_date: expect.any(String), end_date: expect.any(String) }),
    )
    expect(getStatsMock).toHaveBeenCalledWith(expect.objectContaining({ account_id: 795 }))
    expect(wrapper.get('[data-testid="contribution-usage-requests"]').text()).toBe('12')
    expect(wrapper.get('[data-testid="usage-table-stub"]').text()).toBe('2')
  })

  it('re-queries from page 1 with a same-day range when switching to today', async () => {
    const wrapper = mountModal()
    await flushPromises()
    wrapper.findComponent({ name: 'Pagination' }).vm.$emit('update:page', 3)
    await flushPromises()
    expect(queryMock).toHaveBeenLastCalledWith(expect.objectContaining({ page: 3 }))
    queryMock.mockClear()
    getStatsMock.mockClear()

    await wrapper.get('[data-testid="contribution-usage-period"]').setValue('today')
    await flushPromises()

    expect(queryMock).toHaveBeenCalledTimes(1)
    const params = queryMock.mock.calls[0][0]
    expect(params.page).toBe(1)
    expect(params.start_date).toBe(params.end_date)
    expect(getStatsMock).toHaveBeenCalledTimes(1)
  })

  it('does not query without an account and reports load errors', async () => {
    mountModal(null)
    await flushPromises()
    expect(queryMock).not.toHaveBeenCalled()

    queryMock.mockRejectedValueOnce(new Error('boom'))
    mountModal()
    await flushPromises()
    expect(showErrorMock).toHaveBeenCalledTimes(1)
  })
})
