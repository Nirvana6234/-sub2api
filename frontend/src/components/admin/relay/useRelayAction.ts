import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import {
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
  type StepUpController
} from '@/composables/useStepUp'

/** Error shape the API client rejects with. */
interface ApiErrorLike {
  message?: string
  code?: string | number
  reason?: string
  metadata?: Record<string, string>
}

export function relayErrorMessage(error: unknown, fallback: string): string {
  if (typeof error === 'object' && error !== null) {
    const m = (error as ApiErrorLike).message
    if (typeof m === 'string' && m) return m
  }
  return fallback
}

export function relayErrorCode(error: unknown): string {
  const e = (error ?? {}) as ApiErrorLike
  if (typeof e.reason === 'string' && e.reason) return e.reason
  return typeof e.code === 'string' ? e.code : ''
}

/**
 * Shared "run a sensitive relay admin call" helper: step-up retry, a toast on failure, and silent
 * handling of a cancelled TOTP prompt. Resolves with the result, or undefined when it failed or was cancelled.
 */
export function useRelayAction(stepUp: StepUpController) {
  const { t } = useI18n()
  const appStore = useAppStore()

  function report(error: unknown): void {
    if (isStepUpCancelled(error)) return
    if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled')
      )
      return
    }
    appStore.showError(relayErrorMessage(error, t('common.unknownError')))
  }

  async function run<T>(action: () => Promise<T>, successMessage?: string): Promise<T | undefined> {
    try {
      const result = await stepUp.run(action)
      if (successMessage) appStore.showSuccess(successMessage)
      return result
    } catch (error) {
      report(error)
      return undefined
    }
  }

  /** Like run() for calls that return nothing: resolves true when the call went through. */
  async function runOk(action: () => Promise<unknown>, successMessage?: string): Promise<boolean> {
    const done = await run(async () => {
      await action()
      return true
    }, successMessage)
    return done === true
  }

  /** For read-only calls: toast on failure only. */
  async function load<T>(action: () => Promise<T>): Promise<T | undefined> {
    try {
      return await action()
    } catch (error) {
      appStore.showError(relayErrorMessage(error, t('common.unknownError')))
      return undefined
    }
  }

  return { run, runOk, load, report }
}

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

export function formatBytes(n: number | undefined): string {
  if (!n || n <= 0) return '0 B'
  let v = n
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[i]}`
}

/** Bytes per second as megabits per second. */
export function formatMbps(bytesPerSec: number | undefined): string {
  return `${(((bytesPerSec ?? 0) * 8) / 1_000_000).toFixed(1)} Mbps`
}

export function formatTime(iso: string | number | undefined): string {
  if (iso === undefined || iso === null || iso === '' || iso === 0) return '-'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

/** "3 days" style distance from now to a future time; negative / invalid returns ''. */
export function daysUntil(iso: string | undefined, now: number = Date.now()): number | null {
  if (!iso) return null
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return null
  return Math.floor((t - now) / 86_400_000)
}
