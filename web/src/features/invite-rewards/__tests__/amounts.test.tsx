import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, within } from '@testing-library/react'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { InviteRewardsPage } from '@/features/invite-rewards'
import { useSystemConfigStore } from '@/stores/system-config-store'

const originalConfig = useSystemConfigStore.getState().config
const clients: QueryClient[] = []

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      quotaDisplayType: 'CUSTOM',
      quotaPerUnit: 500000,
      usdExchangeRate: 1,
      customCurrencySymbol: '🌱',
      customCurrencyExchangeRate: 100,
    },
  })
})

afterEach(async () => {
  clients.splice(0).forEach((client) => client.clear())
  useSystemConfigStore.setState({ config: originalConfig })
  await i18next.changeLanguage('en')
})

function renderRewards(tier = 100, quota = 500000) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: 0 },
    },
  })
  clients.push(client)
  client.setQueryData(['affiliate-code'], { success: true, data: 'test-code' })
  client.setQueryData(['invite-rewards', 1], {
    success: true,
    data: {
      items: [
        {
          id: 1,
          invitee_id: 2,
          inviter_id: 1,
          invitee_username: 'Invited user',
          state: 'pending',
          qualifying_calls: 5,
          qualifying_quota: quota,
          inviter_quota: 500000,
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      required_calls: 10,
      required_consume_usd: 1,
      required_consume_tier: tier,
    },
  })
  render(
    <QueryClientProvider client={client}>
      <InviteRewardsPage />
    </QueryClientProvider>
  )
}

describe('invite reward amounts', () => {
  it('converts internal spend once and displays the published bud gate once', () => {
    renderRewards()
    expect(
      screen.getByText(/An invited account qualifies after/)
    ).toHaveTextContent('🌱100 of cumulative spend')
    const row = screen.getByRole('row', { name: /Invited user/ })
    expect(within(row).getAllByRole('cell')[1]).toHaveTextContent(
      '🌱100 / 🌱100'
    )
    expect(within(row).getAllByRole('cell')[2]).toHaveTextContent('🌱 100')
  })

  it.each([
    ['zhCN', '1,234.56'],
    ['zhTW', '1,234.56'],
    ['en', '1,234.56'],
    ['fr', '1 234,56'],
    ['ru', '1 234,56'],
    ['ja', '1,234.56'],
    ['vi', '1.234,56'],
  ])(
    'formats progress and gate for interface language %s',
    async (language, expected) => {
      await i18next.changeLanguage(language)
      renderRewards(1234.56, 6172800)
      const row = screen.getByRole('row', { name: /Invited user/ })
      expect(within(row).getAllByRole('cell')[1]).toHaveTextContent(
        `🌱${expected} / 🌱${expected}`
      )
      expect(
        screen.getByText(/An invited account qualifies after/)
      ).toHaveTextContent(`🌱${expected} of cumulative spend`)
    }
  )

  it('updates bud amounts when the interface language changes', async () => {
    renderRewards(1234.56, 6172800)
    await act(async () => {
      await i18next.changeLanguage('vi')
    })
    expect(
      screen.getByText(/An invited account qualifies after/)
    ).toHaveTextContent('🌱1.234,56 of cumulative spend')
  })

  it('falls back safely for an invalid interface language', async () => {
    await i18next.changeLanguage('invalid_locale')
    renderRewards()
    expect(
      screen.getByText(/An invited account qualifies after/)
    ).toHaveTextContent('🌱100 of cumulative spend')
  })
})
