import { computed } from 'vue'
import { useAppStore, useAuthStore } from '@/stores'
import { FeatureFlags, resolveFeatureFlag, type FeatureFlagDefinition } from '@/utils/featureFlags'

/**
 * 三种用法（网页工作台 / API 接入 / 助手客户端）以及充值入口是否对当前站点开放。
 *
 * 同一套前端要服务两个部署版本：其中一个不提供客户端。后台关掉客户端下载或充值后，
 * 仪表盘、首页等处都要跟着隐藏相关内容，所以判断集中在这里，别在各组件里各写一遍。
 * 充值的判断与侧栏 flagPurchase 保持一致：主通道或备用通道任一开启即可，
 * 但被列入充值黑名单的用户一律不显示。
 */
export function useServiceAvailability() {
  const appStore = useAppStore()
  const authStore = useAuthStore()

  // 走同一个 appStore 实例解析开关（与 isFeatureFlagEnabled 规则相同），组件和测试替身读的是同一份设置。
  const flag = (def: FeatureFlagDefinition) => resolveFeatureFlag(appStore.cachedPublicSettings, def)

  const webEnabled = computed(() => flag(FeatureFlags.playground))
  const clientEnabled = computed(() => flag(FeatureFlags.clientDownload))
  const rechargeEnabled = computed(
    () =>
      authStore.user?.recharge_disabled !== true &&
      (flag(FeatureFlags.payment) || flag(FeatureFlags.backupPayment)),
  )

  // 与 KeysView 一致：优先用后台配置的 api_base_url，没配时退回当前站点地址。
  const apiEndpoint = computed(() => {
    const base = (appStore.cachedPublicSettings?.api_base_url || window.location.origin).trim()
    return `${base.replace(/\/v1\/?$/, '').replace(/\/+$/, '')}/v1`
  })

  return { webEnabled, clientEnabled, rechargeEnabled, apiEndpoint }
}
