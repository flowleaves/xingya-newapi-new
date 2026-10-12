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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { RegistrationRiskPage } from '@/features/registration-risk'
import { api } from '@/lib/api'

import { RegistrationsTable } from '../components/registrations-table'
import { registrationAgentSummary } from '../lib/user-agent'

const clients: QueryClient[] = []
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
})

function renderTable(
  node = <RegistrationsTable from={100} to={300} enabled />
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

const agent =
  'Mozilla/5.0 (Linux; Android 16; Test) AppleWebKit/537.36 Chrome/121.0 MQQBrowser/20.6 Mobile Safari/537.36'
const record = {
  user_id: 10,
  username: 'Registered user',
  client_ip: '2001:db8::10',
  user_agent: agent,
  reg_time: 200,
}

describe('registration audit', () => {
  it('summarizes branded browsers before their compatibility tokens', () => {
    expect(registrationAgentSummary(agent)).toBe('Android · QQ Browser 20.6')
    expect(
      registrationAgentSummary(
        'Mozilla/5.0 (Windows NT 10.0) Chrome/153.0 Edg/153.1'
      )
    ).toBe('Windows NT · Edge 153.1')
    expect(registrationAgentSummary('custom-agent')).toBe('custom-agent')
    expect(registrationAgentSummary('')).toBe('')
  })

  it('displays complete IPv6, concise UA with accessible original, and requests the next server page', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockImplementation(async (_url, config) => ({
        data: {
          success: true,
          data: {
            total: 21,
            items:
              config?.params.p === 2
                ? [
                    {
                      ...record,
                      user_id: 11,
                      username: 'Second page',
                      client_ip: '',
                    },
                  ]
                : [record],
          },
        },
      }))
    renderTable()
    const region = screen.getByRole('region', {
      name: 'Successful registrations',
    })
    expect(await within(region).findByText('2001:db8::10')).toBeVisible()
    const ua = within(region).getByText('Android · QQ Browser 20.6')
    expect(ua).toBeVisible()
    await userEvent.tab()
    expect(ua.closest('[tabindex="0"]')).toHaveFocus()
    expect(await screen.findByText(agent)).toBeVisible()
    fireEvent.click(
      within(region).getByRole('button', { name: 'Go to next page' })
    )
    expect(await within(region).findByText('Second page')).toBeVisible()
    expect(within(region).getByText('Not recorded')).toBeVisible()
    expect(get).toHaveBeenLastCalledWith('/api/user/registration_devices', {
      params: { p: 2, page_size: 20, from: 100, to: 300 },
    })
    expect(
      within(region).getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
  })

  it('renders an empty result and converts a business failure to an error with retry', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockResolvedValueOnce({
        data: { success: false, message: 'Database unavailable' },
      })
      .mockResolvedValue({
        data: { success: true, data: { total: 0, items: [] } },
      })
    renderTable()
    expect(
      await screen.findByText('Failed to load registration records')
    ).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByText('No registration records found.')
    ).toBeVisible()
    expect(get).toHaveBeenCalledTimes(2)
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeDisabled()
  })

  it('shows exact IP in duplicate fingerprints and applies shared date filters to successful registrations', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
      data: {
        success: true,
        data: {
          total: 1,
          items: url.endsWith('/risk')
            ? [
                {
                  client_ip: '203.0.113.42',
                  ip_fingerprint: 'hash',
                  ua_fingerprint: 'ua-hash',
                  secret_version: 'v1',
                  repeat_count: 2,
                  latest_reg_time: 200,
                  user_agent: agent,
                  registrations: [
                    { user_id: 10, username: 'Risk user', reg_time: 200 },
                  ],
                },
              ]
            : [record],
        },
      },
    }))
    renderTable(<RegistrationRiskPage />)
    expect(await screen.findByText('203.0.113.42')).toBeVisible()
    expect(await screen.findByText('Registered user')).toBeVisible()
    fireEvent.change(screen.getByLabelText('From'), {
      target: { value: '2026-10-01' },
    })
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        '/api/user/registration_devices',
        expect.objectContaining({
          params: expect.objectContaining({
            p: 1,
            from: Math.floor(new Date('2026-10-01T00:00:00').getTime() / 1000),
          }),
        })
      )
    )
  })
})
