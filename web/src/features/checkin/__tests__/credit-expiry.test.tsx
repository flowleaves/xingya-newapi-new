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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'

import { CheckinHistory } from '../components/checkin-history'
import { formatCheckinDeadline } from '../lib/credit-expiry'
import type { CheckinAward } from '../types'

const expires = Date.parse('2026-11-04T02:03:04Z') / 1000

async function selectAward(record: CheckinAward) {
  render(
    <CheckinHistory
      month='2026-10'
      today='2026-10-12'
      records={[record]}
      onMonthChange={vi.fn()}
    />
  )
  await userEvent.click(
    screen.getByRole('button', { name: /October 5th, 2026/ })
  )
  return screen.findByRole('dialog', { name: '2026-10-05' })
}

test('partially used credit with an active reservation keeps separate available, consumed and expired amounts', async () => {
  const dialog = await selectAward({
    checkin_date: '2026-10-05',
    quota_awarded: 10000,
    source: 'checkin',
    credit: {
      source: 'grant',
      granted: 10000,
      available: 2000,
      reserved: 3000,
      consumed: 4000,
      expired: 1000,
      expires_at: expires,
    },
  })
  for (const label of [
    'Available check-in credit',
    'Reserved for active requests',
    'Check-in credit consumed',
    'Expired check-in credit',
  ]) {
    expect(within(dialog).getByText(label)).toBeVisible()
  }
  expect(
    within(dialog).getByText(/Expires at \(Beijing time\).*10:03:04/)
  ).toBeVisible()
  expect(
    within(dialog).getByText(
      'Requests reserved before expiry can finish. Any unused credit returned after its deadline expires immediately.'
    )
  ).toBeVisible()
  expect(
    within(dialog).queryByText('Expired', { exact: true })
  ).not.toBeInTheDocument()
})

test('historical dedicated credit is identified as part of the opening balance without inventing a daily expiry', async () => {
  const dialog = await selectAward({
    checkin_date: '2026-10-05',
    quota_awarded: 10000,
    source: 'checkin',
    opening_balance: true,
  })
  expect(
    within(dialog).getByText(
      'This award predates expiry tracking. Its remaining credit is included in the pre-upgrade balance, not tracked by day.'
    )
  ).toBeVisible()
  expect(within(dialog).queryByText(/Expires at/)).not.toBeInTheDocument()
})

test('legacy ordinary-balance rewards do not acquire check-in expiry or batch amounts', async () => {
  const dialog = await selectAward({
    checkin_date: '2026-10-05',
    quota_awarded: 10000,
    source: 'legacy',
  })
  expect(
    within(dialog).getByText('Legacy check-in · ordinary balance')
  ).toBeVisible()
  expect(
    within(dialog).queryByText(
      /Expires at|Expired check-in credit|Available check-in credit/
    )
  ).not.toBeInTheDocument()
})

test.each(['zhCN', 'zhTW', 'en', 'fr', 'ja', 'ru', 'vi'])(
  'deadlines use Beijing time with the valid Intl locale for %s',
  (language) => {
    const formatted = formatCheckinDeadline(expires, toIntlLocale(language))
    expect(formatted).toContain('10:03:04')
    expect(formatted).toContain('2026')
  }
)
