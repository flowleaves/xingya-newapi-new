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
import type { ApiResponse } from '@/features/keys/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  CheckinAward,
  CheckinEligibility,
  CheckinKeyResult,
  XingyaCheckinSettings,
  XingyaCheckinStatus,
} from './types'

function checkinData<T>(response: ApiResponse<T>): T {
  const checked = requireServerSuccess(response)
  if (!checked.data) throw new Error('Check-in response is incomplete')
  return checked.data
}

export async function getXingyaCheckin(
  month?: string
): Promise<XingyaCheckinStatus> {
  const response = await api.get<ApiResponse<XingyaCheckinStatus>>(
    '/api/user/xingya-checkin',
    { params: month ? { month } : undefined }
  )
  return checkinData(response.data)
}

export async function claimXingyaCheckin(
  turnstile?: string
): Promise<CheckinAward & { already_claimed: boolean }> {
  const response = await api.post<
    ApiResponse<CheckinAward & { already_claimed: boolean }>
  >('/api/user/xingya-daily-claim', undefined, {
    params: turnstile ? { turnstile } : undefined,
  })
  return checkinData(response.data)
}

export async function createCheckinKey(): Promise<CheckinKeyResult> {
  const response = await api.post<ApiResponse<CheckinKeyResult>>(
    '/api/user/xingya-checkin/key'
  )
  return checkinData(response.data)
}

export async function getCheckinSettings(): Promise<{
  settings: XingyaCheckinSettings
  groups: Record<string, number>
  eligibility: CheckinEligibility
}> {
  const response = await api.get<
    ApiResponse<{
      settings: XingyaCheckinSettings
      groups: Record<string, number>
      eligibility: CheckinEligibility
    }>
  >('/api/xingya/checkin/settings')
  return checkinData(response.data)
}

export async function saveCheckinSettings(
  settings: XingyaCheckinSettings
): Promise<XingyaCheckinSettings> {
  const response = await api.put<ApiResponse<XingyaCheckinSettings>>(
    '/api/xingya/checkin/settings',
    settings
  )
  return checkinData(response.data)
}

export async function refreshCheckinEligibility(
  expected: CheckinEligibility
): Promise<CheckinEligibility> {
  const response = await api.post<ApiResponse<CheckinEligibility>>(
    '/api/xingya/checkin/eligibility/refresh',
    expected
  )
  return checkinData(response.data)
}
