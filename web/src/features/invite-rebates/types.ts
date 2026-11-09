export type InviteRebateSourceType = 'topup' | 'redemption'
export type InviteRebateStatus = 'granted' | 'capped'

export interface InviteRebateSummary {
  aff_code: string
  aff_quota: number
  aff_history_quota: number
  total_rebate_quota: number
  rebate_count: number
  invited_count: number
  active_window_count: number
  ratio_percent: number
  window_days: number
  max_rebate_per_invitee: number
  max_rebate_quota: number
}

export interface InviteRebateItem {
  id: number
  inviter_id: number
  invitee_id: number
  invitee_username: string
  invitee_created_at: number
  source_type: InviteRebateSourceType
  source_id: number
  base_quota: number
  rebate_quota: number
  ratio_bp: number
  window_start_at: number
  window_end_at: number
  occurred_at: number
  status: InviteRebateStatus
  note: string
  created_at: number
}

export interface InviteRebatePayload {
  summary: InviteRebateSummary
  items: InviteRebateItem[]
  page: number
  page_size: number
  total: number
}

export interface InviteRebateResponse {
  success: boolean
  message?: string
  data: InviteRebatePayload
}
