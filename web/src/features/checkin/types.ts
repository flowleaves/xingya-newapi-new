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
import type { ApiKey } from '@/features/keys/types'

export interface CheckinAward {
  checkin_date: string
  quota_awarded: number
  source: 'checkin' | 'legacy'
}

export interface XingyaCheckinStatus {
  today: string
  today_record: { checked_in: boolean; quota_awarded: number; source: string }
  month: string
  records: CheckinAward[]
  month_quota: number
  wallet: {
    available: number
    reserved: number
    granted: number
    consumed: number
  }
  claim_enabled: boolean
  spend_enabled: boolean
  min_reward: number
  max_reward: number
  group: string
  group_accessible: boolean
  pending_review: number
}

export interface XingyaCheckinSettings {
  claim_enabled: boolean
  spend_enabled: boolean
  min_reward: number
  max_reward: number
  group: string
  revision: number
}

export interface CheckinKeyResult {
  token: ApiKey
  reused: boolean
}
