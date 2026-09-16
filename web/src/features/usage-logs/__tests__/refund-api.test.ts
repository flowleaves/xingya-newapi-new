/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { getRefundable, postRefund } from '../api'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn() },
}))

const mockGet = vi.mocked(api.get)
const mockPost = vi.mocked(api.post)

const emptyResponse = {
  data: {
    success: true,
    message: '',
    data: {
      setting: {
        enabled: true,
        ratio: 0.5,
        window_hours: 48,
        daily_max_count: 3,
        daily_max_quota: 0,
        min_refund_quota: 0,
        total_used: { count: 0, quota: 0 },
      },
      logs: [],
      total: 0,
    },
  },
}

describe('self-refund selectors', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('addresses one log by request_id when the caller holds a request id', async () => {
    mockGet.mockResolvedValue(emptyResponse)

    await getRefundable({ requestId: 'req-1' })

    expect(mockGet).toHaveBeenCalledWith('/api/log/self/refundable', {
      params: { request_id: 'req-1' },
    })
  })

  it('addresses one log by its real primary key when the caller holds a log id', async () => {
    mockGet.mockResolvedValue(emptyResponse)

    await getRefundable({ logId: 12345 })

    expect(mockGet).toHaveBeenCalledWith('/api/log/self/refundable', {
      params: { log_id: 12345 },
    })
  })

  it('sends no selector for the list view', async () => {
    mockGet.mockResolvedValue(emptyResponse)

    await getRefundable()

    expect(mockGet).toHaveBeenCalledWith('/api/log/self/refundable', {
      params: undefined,
    })
  })

  it('posts the selector in the body so the server re-validates the same row', async () => {
    mockPost.mockResolvedValue({ data: { success: true, message: '' } })

    await postRefund({ logId: 999 })

    expect(mockPost).toHaveBeenCalledWith('/api/log/self/refund', {
      log_id: 999,
    })
  })

  it('never mixes the two keys into one request', async () => {
    mockGet.mockResolvedValue(emptyResponse)
    mockPost.mockResolvedValue({ data: { success: true, message: '' } })

    await getRefundable({ requestId: 'req-2' })
    await postRefund({ requestId: 'req-2' })

    const getParams = mockGet.mock.calls[0][1]?.params
    const postBody = mockPost.mock.calls[0][1]

    expect(getParams).not.toHaveProperty('log_id')
    expect(postBody).not.toHaveProperty('log_id')
  })
})
