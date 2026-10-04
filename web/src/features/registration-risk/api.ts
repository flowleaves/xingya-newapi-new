import { api } from '@/lib/api'

export interface RegistrationRiskRegistration {
  user_id: number
  username: string
  reg_time: number
}

export interface RegistrationRiskGroup {
  ip_fingerprint: string
  ua_fingerprint: string
  secret_version: string
  repeat_count: number
  latest_reg_time: number
  user_agent: string
  registrations: RegistrationRiskRegistration[]
}

export interface RegistrationRiskResponse {
  success: boolean
  message?: string
  data?: {
    items: RegistrationRiskGroup[]
    total: number
    page: number
    page_size: number
    from: number
    to: number
  }
}

export async function getRegistrationRisk(params: {
  page: number
  pageSize: number
  from: number
  to: number
  minRepeats: number
}): Promise<RegistrationRiskResponse> {
  const res = await api.get('/api/user/registration_devices/risk', {
    params: {
      p: params.page,
      page_size: params.pageSize,
      from: params.from,
      to: params.to,
      min_repeats: params.minRepeats,
    },
  })
  return res.data
}
