/**
 * Ticket notification state.
 *
 * The admin and personal ticket areas have different unread semantics, so
 * keep their counts separate even when an administrator can see both menus.
 */

import { defineStore } from 'pinia'
import { ref } from 'vue'
import ticketsAPI from '@/api/tickets'
import adminTicketsAPI from '@/api/admin/tickets'
import { useAuthStore } from '@/stores/auth'

const POLL_INTERVAL_MS = 30_000

export const useTicketStore = defineStore('tickets', () => {
  const authStore = useAuthStore()

  const userUnreadCount = ref(0)
  const adminUnreadCount = ref(0)
  const loading = ref(false)

  let pollerInterval: ReturnType<typeof setInterval> | null = null
  let activePromise: Promise<void> | null = null
  let requestGeneration = 0

  async function refreshUnreadCounts(): Promise<void> {
    if (!authStore.isAuthenticated) {
      clear()
      return
    }

    if (activePromise) return activePromise

    loading.value = true
    const isAdmin = authStore.isAdmin
    const currentGeneration = ++requestGeneration
    const request = Promise.allSettled([
      ticketsAPI.unreadCount(),
      ...(isAdmin ? [adminTicketsAPI.unreadCount()] : [])
    ])
      .then(([userResult, adminResult]) => {
        if (currentGeneration !== requestGeneration || !authStore.isAuthenticated) return

        if (userResult.status === 'fulfilled') {
          userUnreadCount.value = normalizeCount(userResult.value)
        } else {
          console.error('Failed to fetch personal ticket unread count:', userResult.reason)
        }

        if (isAdmin && adminResult?.status === 'fulfilled') {
          adminUnreadCount.value = normalizeCount(adminResult.value)
        } else if (isAdmin && adminResult?.status === 'rejected') {
          console.error('Failed to fetch admin ticket unread count:', adminResult.reason)
        }
      })
      .finally(() => {
        if (activePromise === request) {
          activePromise = null
          loading.value = false
        }
      })

    activePromise = request
    return request
  }

  function startPolling() {
    if (pollerInterval) return

    pollerInterval = setInterval(() => {
      refreshUnreadCounts().catch((error) => {
        console.error('Ticket unread count polling failed:', error)
      })
    }, POLL_INTERVAL_MS)
  }

  function stopPolling() {
    if (pollerInterval) {
      clearInterval(pollerInterval)
      pollerInterval = null
    }
  }

  function clear() {
    requestGeneration++
    userUnreadCount.value = 0
    adminUnreadCount.value = 0
    activePromise = null
    loading.value = false
    stopPolling()
  }

  function normalizeCount(value: unknown): number {
    const count = Number(value)
    return Number.isFinite(count) && count > 0 ? Math.floor(count) : 0
  }

  return {
    userUnreadCount,
    adminUnreadCount,
    loading,
    refreshUnreadCounts,
    startPolling,
    stopPolling,
    clear
  }
})
