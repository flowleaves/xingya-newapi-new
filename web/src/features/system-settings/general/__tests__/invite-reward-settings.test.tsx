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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { InviteRewardSettingsSection } from '../invite-reward-settings-section'

function Fixture(props: { requiredConsumeUsd: number }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <InviteRewardSettingsSection
          defaultValues={{ required_consume_usd: props.requiredConsumeUsd }}
        />
      </SettingsPageProvider>
    </>
  )
}

async function renderSettings(requiredConsumeUsd = 1) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => <Fixture requiredConsumeUsd={requiredConsumeUsd} />,
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return screen.findByRole('spinbutton', {
    name: 'Required cumulative spend (USD)',
  })
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  useSystemConfigStore.setState((state) => ({
    config: { ...state.config, currency: { ...state.config.currency, quotaPerUnit: 500000 } },
  }))
})

test('the spend gate is entered in USD and saves under its own option key', async () => {
  const user = userEvent.setup()
  const input = await renderSettings(1)

  expect(input).toHaveValue(1)
  await user.clear(input)
  await user.type(input, '2.5')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'invite_reward_setting.required_consume_usd',
      value: 2.5,
    })
  )
})

test('the platform-currency equivalent follows the entered amount, not a fixed label', async () => {
  // 1 USD is 100 芽点 at the platform's 100 芽点-per-USD rate, and the hint has to track
  // whatever the operator types rather than being a static string.
  const user = userEvent.setup()
  const input = await renderSettings(1)
  expect(
    screen.getByText(/Equivalent to 🌱100 in platform currency\./)
  ).toBeInTheDocument()

  await user.clear(input)
  await user.type(input, '2.5')
  await waitFor(() =>
    expect(
      screen.getByText(/Equivalent to 🌱250 in platform currency\./)
    ).toBeInTheDocument()
  )

  // Zero disables the spend gate, so no equivalent is shown.
  await user.clear(input)
  await user.type(input, '0')
  await waitFor(() =>
    expect(screen.queryByText(/Equivalent to 🌱/)).not.toBeInTheDocument()
  )
})
