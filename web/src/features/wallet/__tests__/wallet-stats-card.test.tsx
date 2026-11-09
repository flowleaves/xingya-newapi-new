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
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { WalletStatsCard } from '../components/wallet-stats-card'

const user = {
  id: 1,
  username: 'fixture',
  quota: 1000000,
  used_quota: 500000,
  request_count: 3,
  aff_quota: 0,
  aff_history_quota: 0,
  aff_count: 0,
  group: 'default',
}

test('the wallet shows only balance, usage and requests without check-in figures', () => {
  const get = vi.spyOn(api, 'get')
  render(<WalletStatsCard user={user} />)

  expect(screen.getByText('Current Balance')).toBeVisible()
  expect(screen.getByText('Total Usage')).toBeVisible()
  expect(screen.getByText('API Requests')).toBeVisible()
  expect(screen.queryByText('Available check-in credit')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})
