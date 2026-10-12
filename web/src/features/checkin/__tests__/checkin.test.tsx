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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { XingyaCheckinSettingsSection } from '@/features/system-settings/general/xingya-checkin-settings-section'
import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { XingyaCheckinCard } from '../components/xingya-checkin-card'
import type { XingyaCheckinSettings, XingyaCheckinStatus } from '../types'

function fixture(): XingyaCheckinStatus {
  return {
    today: '2026-10-12',
    month: '2026-10',
    today_record: { checked_in: false, quota_awarded: 0, source: '' },
    records: [],
    month_quota: 0,
    wallet: { available: 5000, reserved: 0, granted: 5000, consumed: 0 },
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
  const user = userEvent.setup()
  await user.clear(minimum)
  await user.type(minimum, '1.5')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/xingya/checkin/settings', {
      ...settings,
      min_reward: 1.5,
      revision: 1,
    })
  )
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  )
  expect(minimum).toHaveValue(1.5)
})
