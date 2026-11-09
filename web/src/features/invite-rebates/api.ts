import { api } from '@/lib/api'

import type { InviteRebateResponse } from './types'

export async function getInviteRebates(
  page = 1,
  pageSize = 20
): Promise<InviteRebateResponse> {
  const res = await api.get('/api/user/invite_rebates', {
    params: { p: page, page_size: pageSize },
  })
  return res.data
}
