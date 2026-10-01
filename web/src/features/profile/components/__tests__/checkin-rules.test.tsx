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
import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } from '@/stores/system-config-store'

import type { CheckinStatusResponse } from '../../types'
import { CheckinRules } from '../checkin-rules'

/**
 * Rule B/C compare a tier count the client derives from server-reported raw
 * internal units. The server awards through model.checkinQuotaPerTier()
 * (common.QuotaPerUnit / 100), so the client must derive the same rate from the
 * configured QuotaPerUnit instead of assuming it; otherwise the displayed tier
 * and the payout disagree whenever the admin changes QuotaPerUnit.
 */
const checkinData: CheckinStatusResponse = {
  enabled: true,
  stats: {
    checked_in_today: false,
    total_checkins: 0,
    total_quota: 0,
    checkin_count: 0,
    records: [],
  },
  yesterday: { count: 0, quota: 30000 },
  count_tiers: [],
  quota_tiers: [],
  c_enabled: false,
  fallback_reward: 0,
}

function setQuotaPerUnit(quotaPerUnit: number): void {
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: { ...state.config.currency, quotaPerUnit },
    },
  }))
}

afterEach(() => {
  setQuotaPerUnit(DEFAULT_CURRENCY_CONFIG.quotaPerUnit)
})

describe('check-in rule B tier derivation follows the configured QuotaPerUnit', () => {
  it('derives one platform currency unit per QuotaPerUnit / 100 internal units', () => {
    setQuotaPerUnit(500000)
    render(<CheckinRules checkinData={checkinData} checkedToday={false} />)

    // 30000 / (500000 / 100) = 6 tiers
    expect(screen.getByText('Yesterday: 🌱6')).toBeInTheDocument()
  })

  it('re-derives the tier when the admin raises QuotaPerUnit', () => {
    setQuotaPerUnit(1500000)
    render(<CheckinRules checkinData={checkinData} checkedToday={false} />)

    // 30000 / (1500000 / 100) = 2 tiers, not the 6 a hardcoded rate would show
    expect(screen.getByText('Yesterday: 🌱2')).toBeInTheDocument()
  })

  it('falls back to the default rate when QuotaPerUnit is not a usable number', () => {
    setQuotaPerUnit(0)
    render(<CheckinRules checkinData={checkinData} checkedToday={false} />)

    expect(screen.getByText('Yesterday: 🌱6')).toBeInTheDocument()
  })
})
