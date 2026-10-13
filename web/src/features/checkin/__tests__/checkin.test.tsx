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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { XingyaCheckinSettingsSection } from '@/features/system-settings/general/xingya-checkin-settings-section'
import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { XingyaCheckinCard } from '../components/xingya-checkin-card'
import { checkinSettingsSchema } from '../lib/settings-schema'
import type { XingyaCheckinSettings, XingyaCheckinStatus } from '../types'

function fixture(): XingyaCheckinStatus {
  return {
    today: '2026-10-12',
    month: '2026-10',
    today_record: { checked_in: false, quota_awarded: 0, source: '' },
    records: [],
    month_quota: 0,
    wallet: {
      available: 5000,
      reserved: 0,
      granted: 5000,
      consumed: 0,
      expired: 0,
    },
    expiry: { expiring_soon: 0, next_expires_at: 0, next_expiring_quota: 0 },
    claim_enabled: true,
    spend_enabled: true,
    min_reward: 1,
    max_reward: 2,
    group: 'checkin-fixture',
    group_accessible: true,
    pending_review: 0,
  }
}

async function renderCards(props: { full?: boolean; double?: boolean } = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <>
          <XingyaCheckinCard compact={!props.full} />
          {props.double && <XingyaCheckinCard compact />}
        </>
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return screen.findByText('Available check-in credit', {}, { timeout: 10000 })
}

beforeEach(() => {
  useSystemConfigStore.setState((s) => ({
    config: {
      ...s.config,
      currency: { ...s.config.currency, quotaPerUnit: 500000 },
    },
  }))
})

test('all visible entry points refresh after a claim on the new endpoint', async () => {
  const data = fixture()
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/status'
          ? { turnstile_check: false }
          : structuredClone(data),
    },
  }))
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    data.today_record = {
      checked_in: true,
      quota_awarded: 7500,
      source: 'checkin',
    }
    data.wallet.available += 7500
    data.wallet.granted += 7500
    return {
      data: {
        success: true,
        data: {
          ...data.today_record,
          checkin_date: data.today,
          already_claimed: false,
        },
      },
    }
  })
  // Avoid ambiguous initial text while two cards intentionally share one query.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <>
          <XingyaCheckinCard compact />
          <XingyaCheckinCard compact />
        </>
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await waitFor(() =>
    expect(
      screen.getAllByRole('button', { name: 'Check in now' })
    ).toHaveLength(2)
  )
  await userEvent.click(
    screen.getAllByRole('button', { name: 'Check in now' })[0]
  )
  await waitFor(() =>
    expect(
      screen.getAllByRole('button', { name: 'Checked in · see you tomorrow' })
    ).toHaveLength(2)
  )
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/user/xingya-daily-claim',
    undefined,
    { params: undefined }
  )
  expect(post).not.toHaveBeenCalledWith('/api/user/checkin', expect.anything())
})

test('paused claims retain the balance, history and exact model-group link', async () => {
  const data = fixture()
  data.claim_enabled = false
  data.spend_enabled = false
  data.records = [
    { checkin_date: '2026-10-05', quota_awarded: 15000, source: 'legacy' },
  ]
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: { success: true, data: url === '/api/status' ? {} : data },
  }))
  await renderCards({ full: true })
  expect(
    screen.getByRole('button', { name: 'Check-in claims are paused' })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Create check-in key' })
  ).toBeDisabled()
  expect(screen.getByText('Check-in Calendar')).toBeVisible()
  expect(
    screen.getByText(
      'Check-in spending is paused. Existing credit still expires on its original date.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'View available models' })
  ).toHaveAttribute('href', '/pricing?group=checkin-fixture')
})

test('failed status never appears as a zero wallet', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') return { data: { success: true, data: {} } }
    throw new Error('fixture unavailable')
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <XingyaCheckinCard compact />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText('Failed to fetch checkin status')
  ).toBeVisible()
  expect(
    screen.queryByText('Available check-in credit')
  ).not.toBeInTheDocument()
})

test('key reveal uses the returned ID and clears the secret on close', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') return { data: { success: true, data: {} } }
    return { data: { success: true, data: fixture() } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/user/xingya-checkin/key') {
      return {
        data: {
          success: true,
          data: {
            token: {
              id: 778,
              name: '签到-fixture',
              group: 'checkin-fixture',
              quota_source: 'checkin',
            },
            reused: true,
          },
        },
      }
    }
    return {
      data: { success: true, data: { key: 'sk-fixture-only-not-a-real-key' } },
    }
  })
  await renderCards()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Create check-in key' }))
  const dialog = await screen.findByRole('dialog', { name: 'Check-in key' })
  expect(
    within(dialog).getByText(
      'An existing check-in key was reused. Its limits remain in effect.'
    )
  ).toBeVisible()
  await user.click(within(dialog).getByRole('button', { name: 'Show API Key' }))
  expect(
    await within(dialog).findByText('sk-fixture-only-not-a-real-key')
  ).toBeVisible()
  expect(post).toHaveBeenCalledWith('/api/token/778/key')
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(
      screen.queryByText('sk-fixture-only-not-a-real-key')
    ).not.toBeInTheDocument()
  )
})

test('loaded settings populate the form and a complete save uses the newest revision', async () => {
  let settings = {
    claim_enabled: true,
    spend_enabled: true,
    min_reward: 1.2,
    max_reward: 2.8,
    consume_threshold: 5000,
    fixed_reward: 30,
    group: 'checkin-fixture',
    revision: 1,
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/xingya/checkin/reviews'
          ? { items: [], total: 0 }
          : { settings, groups: { 'checkin-fixture': 1 } },
    },
  }))
  const put = vi
    .spyOn(api, 'put')
    .mockImplementation(async (_url, submitted) => {
      settings = {
        ...(submitted as XingyaCheckinSettings),
        revision: settings.revision + 1,
      }
      return { data: { success: true, data: settings } }
    })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <XingyaCheckinSettingsSection />
    </QueryClientProvider>
  )
  const minimum = await screen.findByLabelText('Daily minimum (sprouts)')
  await waitFor(() => expect(minimum).toHaveValue(1.2))
  expect(
    screen.getByRole('switch', { name: 'Allow daily claims' })
  ).toBeChecked()
  expect(screen.getByText('Credit validity · fixed 30 days')).toBeVisible()
  const threshold = screen.getByLabelText(
    'Cumulative spending threshold (sprouts)'
  )
  const fixed = screen.getByLabelText(
    'Fixed daily reward after threshold (sprouts)'
  )
  expect(threshold).toHaveValue(5000)
  expect(fixed).toHaveValue(30)
  const user = userEvent.setup()
  await user.clear(minimum)
  await user.type(minimum, '1.5')
  await user.clear(threshold)
  await user.type(threshold, '6000')
  await user.clear(fixed)
  await user.type(fixed, '35')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/xingya/checkin/settings', {
      ...settings,
      min_reward: 1.5,
      consume_threshold: 6000,
      fixed_reward: 35,
      revision: 1,
    })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  )
  expect(minimum).toHaveValue(1.5)
  expect(threshold).toHaveValue(6000)
  expect(fixed).toHaveValue(35)
})

test('fixed reward validation rejects enabled zero rewards and invalid amounts', () => {
  const schema = checkinSettingsSchema((key) => key)
  const settings = {
    claim_enabled: true,
    spend_enabled: true,
    min_reward: 1,
    max_reward: 2,
    consume_threshold: 5000,
    fixed_reward: 30,
    group: 'checkin-fixture',
    revision: 1,
  }
  expect(schema.safeParse(settings).success).toBe(true)
  expect(schema.safeParse({ ...settings, fixed_reward: 0 }).success).toBe(false)
  expect(
    schema.safeParse({ ...settings, consume_threshold: 0, fixed_reward: 0 })
      .success
  ).toBe(true)
  expect(schema.safeParse({ ...settings, consume_threshold: -1 }).success).toBe(
    false
  )
  expect(schema.safeParse({ ...settings, fixed_reward: 100001 }).success).toBe(
    false
  )
  expect(
    schema.safeParse({ ...settings, consume_threshold: 5000.001 }).success
  ).toBe(false)
})

test('the shared card shows upcoming expiry and opens reconciled totals with the opening deadline', async () => {
  const data = fixture()
  const expires = Date.parse('2026-11-01T02:03:04Z') / 1000
  data.wallet = {
    available: 5000,
    reserved: 1000,
    consumed: 2000,
    expired: 3000,
    granted: 11000,
  }
  data.expiry = {
    expiring_soon: 5000,
    next_expires_at: expires,
    next_expiring_quota: 5000,
    opening: { ...data.wallet, source: 'opening', expires_at: expires },
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: { success: true, data: url === '/api/status' ? {} : data },
  }))
  await renderCards()
  expect(screen.getByText(/Expiring within 7 days/)).toBeVisible()
  expect(
    screen.getByText(/Next expiry \(Beijing time\).*10:03:04/)
  ).toBeVisible()
  expect(screen.queryByText('Expired check-in credit')).not.toBeInTheDocument()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Credit details' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Check-in credit details',
  })
  expect(
    within(dialog).getByText('Total check-in credit awarded')
  ).toBeVisible()
  expect(within(dialog).getAllByText('Expired check-in credit')).toHaveLength(2)
  expect(within(dialog).getByText('Pre-upgrade check-in credit')).toBeVisible()
  expect(
    within(dialog).getByText(/Expires at \(Beijing time\).*10:03:04/)
  ).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(screen.getByRole('button', { name: 'Credit details' })).toHaveFocus()
})

test('a wallet without available expiring lots does not display a false expiry reminder', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: { success: true, data: url === '/api/status' ? {} : fixture() },
  }))
  await renderCards()
  expect(screen.queryByText(/Expiring within 7 days/)).not.toBeInTheDocument()
  expect(
    screen.queryByText(/Next expiry \(Beijing time\)/)
  ).not.toBeInTheDocument()
})

test('negative check-in balances and an extra daily round are visible', async () => {
  const data = fixture()
  data.wallet.available = -25000
  data.wallet.borrowed = 25000
  data.claim_round = 1
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: { success: true, data: url === '/api/status' ? {} : data },
  }))
  await renderCards()
  expect(
    screen.getByText('An extra check-in is available today.')
  ).toBeVisible()
  expect(
    screen.getByText(
      'Check-in balance can reach −10 sprouts. New rewards repay overdrafts first.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Check in now' })).toBeEnabled()
  await userEvent.click(screen.getByRole('button', { name: 'Credit details' }))
  expect(await screen.findByText('Check-in overdraft')).toBeVisible()
})

test('refresh confirmation retains its displayed round after a background refetch', async () => {
  const settings: XingyaCheckinSettings = {
    claim_enabled: true,
    spend_enabled: true,
    min_reward: 1,
    max_reward: 2,
    consume_threshold: 5000,
    fixed_reward: 30,
    group: 'checkin-fixture',
    revision: 1,
  }
  const eligibility = { day: '2026-10-13', round: 0 }
  let currentEligibility = eligibility
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/xingya/checkin/reviews'
          ? { items: [], total: 0 }
          : {
              settings,
              eligibility: currentEligibility,
              groups: { 'checkin-fixture': 1 },
            },
    },
  }))
  const post = vi.spyOn(api, 'post').mockImplementation(async () => ({
    data: { success: true, data: { ...eligibility, round: 1 } },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <XingyaCheckinSettingsSection />
    </QueryClientProvider>
  )
  const button = await screen.findByRole('button', {
    name: 'Refresh today’s check-in eligibility',
  })
  await userEvent.click(button)
  const dialog = await screen.findByRole('alertdialog')
  expect(
    within(dialog).getByText(/Existing rewards will be kept/)
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  // Another administrator refreshed while this confirmation was open.
  currentEligibility = { ...eligibility, round: 1 }
  await act(() =>
    client.invalidateQueries({ queryKey: ['xingya-checkin-settings'] })
  )
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Allow one more check-in' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledExactlyOnceWith(
      '/api/xingya/checkin/eligibility/refresh',
      eligibility
    )
  )
})
