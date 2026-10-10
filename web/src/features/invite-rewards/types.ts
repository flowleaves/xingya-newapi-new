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
  /** Cumulative billable spend by the invitee, in internal quota units. */
  qualifying_quota: number
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
  /** How many successful billable calls the invitee must complete. */
  required_calls: number
  /** Spend gate as published in USD. */
  required_consume_usd: number
  /**
   * Spend gate in platform currency (芽点). Derived by the backend from the configured
   * exchange rate, so the UI never hardcodes the conversion.
   */
  required_consume_tier: number
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
