export type InviteRewardState =
  | 'pending'
  | 'eligible'
  | 'granted'
  | 'cancelled'

export interface InviteReward {
  id: number
  invitee_id: number
  inviter_id: number
  invitee_quota: number
  inviter_quota: number
  qualifying_calls: number
  eligible_at: number
  auto_grant_at: number
  grant_method: 'auto' | 'manual' | ''
  state: InviteRewardState
  created_at: number
  granted_at: number
  settled_at: number
  cancelled_note?: string
  invitee_username: string
  invitee_display_name: string
}

export interface InviteRewardsResponse {
  items: InviteReward[]
  total: number
  page: number
  page_size: number
  required_calls: number
}

export interface InviteRewardApiResponse {
  success: boolean
  message?: string
  data?: InviteRewardsResponse
}

export interface InviteRewardClaimResponse {
  success: boolean
  message?: string
  data?: {
    granted: boolean
    already_granted: boolean
  }
}
