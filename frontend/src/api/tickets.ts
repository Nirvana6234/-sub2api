/**
 * User Tickets API endpoints
 *
 * 这些接口只返回当前登录用户自己的工单，归属过滤在后端 SQL 层完成。
 */

import { apiClient } from './client'
import type {
  BasePaginationResponse,
  CreateTicketRequest,
  Ticket,
  TicketListFilters,
  TicketMessage
} from '@/types'

export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: TicketListFilters,
  options?: { signal?: AbortSignal }
): Promise<BasePaginationResponse<Ticket>> {
  const { data } = await apiClient.get<BasePaginationResponse<Ticket>>('/tickets', {
    params: { page, page_size: pageSize, ...filters },
    signal: options?.signal
  })
  return data
}

export async function getById(id: number): Promise<Ticket> {
  const { data } = await apiClient.get<Ticket>(`/tickets/${id}`)
  return data
}

export async function create(request: CreateTicketRequest): Promise<Ticket> {
  const { data } = await apiClient.post<Ticket>('/tickets', request)
  return data
}

export async function reply(id: number, content: string): Promise<TicketMessage> {
  const { data } = await apiClient.post<TicketMessage>(`/tickets/${id}/messages`, { content })
  return data
}

export async function close(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/tickets/${id}/close`)
  return data
}

export async function unreadCount(): Promise<number> {
  const { data } = await apiClient.get<{ count: number }>('/tickets/unread-count')
  return data.count
}

const ticketsAPI = {
  list,
  getById,
  create,
  reply,
  close,
  unreadCount
}

export default ticketsAPI
