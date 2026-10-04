import { api } from '@/lib/api'

import type {
  InviteRewardApiResponse,
  InviteRewardClaimResponse,
} from './types'

export async function getInviteRewards(
  page: number,
  pageSize = 20
): Promise<InviteRewardApiResponse> {
  const res = await api.get('/api/user/invite_rewards/self', {
    params: { p: page, page_size: pageSize },
  })
  return res.data
}

export async function claimInviteReward(
  rewardId: number
): Promise<InviteRewardClaimResponse> {
  const res = await api.post(`/api/user/invite_rewards/${rewardId}/claim`)
  return res.data
}
