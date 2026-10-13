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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { WalletStatsCard } from '../components/wallet-stats-card'

test('the wallet displays and refreshes check-in overdrafts separately from regular funds', async () => {
  const previous = useSystemConfigStore.getState().config
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: '🌱',
      customCurrencyExchangeRate: 100,
    },
  })
  try {
    let available = -50000
    vi.spyOn(api, 'get').mockImplementation(async () => ({
      data: { success: true, data: { wallet: { available } } },
    }))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <WalletStatsCard
          checkinEnabled
          user={{
            id: 1,
            username: 'fixture',
            quota: 1000000,
            used_quota: 500000,
            request_count: 3,
            aff_quota: 0,
            aff_history_quota: 0,
            aff_count: 0,
            group: 'default',
          }}
        />
      </QueryClientProvider>
    )
    expect(await screen.findByText('🌱 -10')).toBeVisible()
    expect(screen.getByText('🌱 200')).toBeVisible()
    available = 100000
    await act(() => client.invalidateQueries({ queryKey: ['xingya-checkin'] }))
    expect(await screen.findByText('🌱 20')).toBeVisible()
    expect(screen.queryByText('🌱 -10')).not.toBeInTheDocument()
    expect(screen.getByText('🌱 200')).toBeVisible()
    client.clear()
  } finally {
    useSystemConfigStore.getState().setConfig(previous)
  }
})
