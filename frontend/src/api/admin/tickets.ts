/**
 * Admin Tickets API endpoints
 *
 * 管理员可见全部用户的工单。
 */

import { apiClient } from '../client'
import type {
  AdminTicket,
  BasePaginationResponse,
  TicketListFilters,
  TicketMessage,
  TicketStatus
} from '@/types'

export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: TicketListFilters,
  options?: { signal?: AbortSignal }
): Promise<BasePaginationResponse<AdminTicket>> {
  const { data } = await apiClient.get<BasePaginationResponse<AdminTicket>>('/admin/tickets', {
    params: { page, page_size: pageSize, ...filters },
    signal: options?.signal
  })
  return data
}

export async function getById(id: number): Promise<AdminTicket> {
  const { data } = await apiClient.get<AdminTicket>(`/admin/tickets/${id}`)
  return data
}

export async function reply(id: number, content: string): Promise<TicketMessage> {
  const { data } = await apiClient.post<TicketMessage>(`/admin/tickets/${id}/messages`, { content })
  return data
}

export async function updateStatus(id: number, status: TicketStatus): Promise<{ message: string }> {
  const { data } = await apiClient.put<{ message: string }>(`/admin/tickets/${id}/status`, { status })
  return data
}

export async function batchReadStatus(ids: number[], read: boolean): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>('/admin/tickets/batch-read-status', { ids, read })
  return data
}

export async function batchDelete(ids: number[]): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>('/admin/tickets/batch-delete', { ids })
  return data
}

export async function unreadCount(): Promise<number> {
  const { data } = await apiClient.get<{ count: number }>('/admin/tickets/unread-count')
  return data.count
}

const ticketsAPI = {
  list,
  getById,
  reply,
  updateStatus,
  batchReadStatus,
  batchDelete,
  unreadCount
}

export default ticketsAPI
