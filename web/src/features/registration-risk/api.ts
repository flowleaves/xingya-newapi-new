import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface RegistrationRiskRegistration {
  user_id: number
  username: string
  reg_time: number
}

export interface RegistrationRiskGroup {
  client_ip: string
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
  return requireServerSuccess(res.data)
}

export interface RegistrationDeviceItem {
  user_id: number
  username: string
  client_ip: string
  user_agent: string
  reg_time: number
}

export async function getRegistrationDevices(params: {
  page: number
  pageSize: number
  from: number
  to: number
}): Promise<{ items: RegistrationDeviceItem[]; total: number }> {
  const res = await api.get('/api/user/registration_devices', {
    params: {
      p: params.page,
      page_size: params.pageSize,
      from: params.from,
      to: params.to,
    },
  })
  return requireServerSuccess(res.data).data
}
