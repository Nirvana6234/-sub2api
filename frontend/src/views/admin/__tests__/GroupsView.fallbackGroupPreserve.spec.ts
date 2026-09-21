import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AdminGroup } from "@/types";
import GroupsView from "@/views/admin/GroupsView.vue";

// 回归测试：2026-09 生产事故。打开"编辑分组"对话框时，handleEdit() 先设
// editForm.platform 再设 editForm.fallback_group_id（同一个同步函数里）。
// watch(() => editForm.platform, ...) 是 post-flush 的，要等这个函数跑完才触发——
// 那时 fallback_group_id 已经被正确回填过了，如果 watch 回调不区分"刚打开编辑框、
// platform 只是从上一次编辑残留的旧值变成这个分组自己的 platform"和"用户在编辑过程中
// 主动切换了 platform"，就会把刚回填好的值又冲成 null，保存时被后端当作"明确清空"，
// 静默清空了生产在用的兜底配置。

const { listGroups } = vi.hoisted(() => ({
  listGroups: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: vi.fn(),
      getModelsListCandidates: vi.fn().mockResolvedValue([]),
      getModelAllowlistCandidates: vi.fn().mockResolvedValue([]),
      getUsageSummary: vi.fn().mockResolvedValue([]),
      getCapacitySummary: vi.fn().mockResolvedValue([]),
      getLiveCapability: vi.fn().mockResolvedValue({ supported: false }),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      duplicate: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn(),
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
  }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn(),
  }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  };
});

const fallbackPoolGroup = {
  id: 29,
  name: "puls-兜底",
  description: null,
  platform: "openai",
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: "active",
  subscription_type: "standard",
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  long_context_pricing_enabled: true,
  force_openai_fast: false,
  free_openai_fast: false,
  model_pricing: [],
  profit_control_enabled: false,
  profit_min_margin: 0,
  profit_safety_buffer: 0,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  search_price_per_1k: null,
  audio_realtime_price_per_min: null,
  audio_tts_price_per_million_chars: null,
  audio_stt_price_per_hour: null,
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_ids: [],
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  allow_live: false,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: "2026-09-05T00:00:00Z",
  updated_at: "2026-09-05T00:00:00Z",
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  is_fallback_pool: true,
  account_count: 6,
  active_account_count: 6,
  rate_limited_account_count: 0,
  models_list_config: undefined,
  sort_order: 5,
} satisfies AdminGroup;

// 对应真实事故里的 "plus" 组：openai 平台，已经配好 fallback_group_id=29。
const plusGroup = {
  ...fallbackPoolGroup,
  id: 2,
  name: "plus",
  is_fallback_pool: false,
  fallback_group_id: 29,
  sort_order: 1,
} satisfies AdminGroup;

const AppLayoutStub = defineComponent({ template: "<main><slot /></main>" });
const TablePageLayoutStub = defineComponent({
  template:
    '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
});
const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template:
    '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
});
const BaseDialogStub = defineComponent({
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
});

// 真实 Select 组件换成一个透出 modelValue 的哑元件，用 data-select-id 区分页面上
// 多个 Select——真实组件被全局 stub 掉时拿不到这个信息。
const SelectProbeStub = defineComponent({
  props: {
    modelValue: { type: null, default: undefined },
    placeholder: { type: String, default: "" },
  },
  template:
    '<div class="select-probe" :data-placeholder="placeholder" :data-value="String(modelValue)"></div>',
});

// 兜底分组已升级为多选（GroupSelector），同样换成透出 modelValue 的哑元件。
const GroupSelectorProbeStub = defineComponent({
  props: {
    modelValue: { type: Array as () => number[], default: () => [] },
    label: { type: String, default: "" },
  },
  template:
    '<div class="group-selector-probe" :data-label="label" :data-value="JSON.stringify(modelValue)"></div>',
});

const mountView = () =>
  mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectProbeStub,
        GroupSelector: GroupSelectorProbeStub,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        ReasoningEffortPolicyFields: true,
        CodexManifestAccountsField: true,
        PricingEntryCard: true,
        VueDraggable: true,
      },
    },
  });

describe("GroupsView edit dialog preserves fallback_group_id on open", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    localStorage.clear();
    listGroups.mockReset();
    listGroups.mockResolvedValue({
      items: [plusGroup, fallbackPoolGroup],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    });
  });

  it("does not reset an existing fallback_group_id when the edit dialog just opens", async () => {
    const wrapper = mountView();
    await flushPromises();

    const editButtons = wrapper
      .findAll("button")
      .filter((button) => button.text().includes("common.edit"));
    // plusGroup 是第一行。
    await editButtons[0]!.trigger("click");
    await flushPromises();
    await flushPromises();

    const fallbackSelector = wrapper
      .findAll(".group-selector-probe")
      .find((el) => el.attributes("data-label") === "兜底分组");
    expect(fallbackSelector).toBeTruthy();
    expect(JSON.parse(fallbackSelector!.attributes("data-value")!)).toEqual([
      29,
    ]);

    wrapper.unmount();
  });
});
