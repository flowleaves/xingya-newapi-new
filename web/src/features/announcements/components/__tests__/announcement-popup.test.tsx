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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  afterAll,
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from 'vitest'

import { AnnouncementPopup } from '@/components/announcement-popup'
import { useNotifications } from '@/hooks/use-notifications'
import { getAnnouncementKey } from '@/lib/announcement-key'
import { useNotificationStore } from '@/stores/notification-store'

const NOW = Date.parse('2026-05-10T12:00:00Z')

const { announcementsRef } = vi.hoisted(() => ({
  announcementsRef: { current: [] as Record<string, unknown>[] },
}))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({
    status: {
      announcements_enabled: true,
      announcements: announcementsRef.current,
    },
    loading: false,
  }),
}))

function UnreadCount() {
  const notifications = useNotifications()
  return (
    <output aria-label='Unread announcements'>
      {notifications.unreadAnnouncementsCount}
    </output>
  )
}

const clients: QueryClient[] = []

function renderPopup() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity, gcTime: 0 },
    },
  })
  clients.push(client)
  client.setQueryData(['notice'], { success: true, data: '' })
  return render(
    <QueryClientProvider client={client}>
      <AnnouncementPopup />
      <UnreadCount />
    </QueryClientProvider>
  )
}

function announcement(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    content: 'Scheduled maintenance',
    publishDate: '2026-05-01T00:00:00Z',
    type: 'default',
    popup: true,
    ...overrides,
  }
}

// jsdom does not implement Web Animations, which the dialog's close path queries.
// Same shim the other dialog tests in this repo install.
const originalGetAnimations = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (originalGetAnimations) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      originalGetAnimations
    )
    return
  }
  Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
})

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(NOW)
  useNotificationStore.setState({ readAnnouncementKeys: [] })
  announcementsRef.current = []
})

afterEach(() => {
  vi.useRealTimers()
  clients.splice(0).forEach((client) => client.clear())
  useNotificationStore.setState({ readAnnouncementKeys: [] })
  localStorage.removeItem('notification-storage')
})

describe('announcement popup dialog', () => {
  it('renders the due announcement and never more than one dialog', () => {
    announcementsRef.current = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' }),
    ]

    renderPopup()

    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    // The newer of the two due announcements is the one on screen.
    expect(screen.getByText('Scheduled maintenance')).toBeInTheDocument()
  })

  it('renders nothing when the only announcement is not yet due', () => {
    announcementsRef.current = [
      announcement({ id: 1, publishDate: '2026-06-01T00:00:00Z' }),
    ]

    renderPopup()

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('dismissing records every due announcement as read', async () => {
    const older = announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' })
    const newer = announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' })
    announcementsRef.current = [older, newer]

    renderPopup()
    // fireEvent rather than userEvent: the latter drives animation APIs that jsdom
    // does not implement (`viewport.getAnimations`).
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))

    // Both keys, not only the visible one: otherwise the dialog would immediately
    // reappear for the older announcement.
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      expect(screen.getByLabelText('Unread announcements')).toHaveTextContent(
        '0'
      )
      expect(useNotificationStore.getState().readAnnouncementKeys).toEqual(
        expect.arrayContaining([
          getAnnouncementKey(newer),
          getAnnouncementKey(older),
        ])
      )
    })
  })

  it('Escape dismisses the popup while leaving future announcements unread', async () => {
    announcementsRef.current = [
      announcement(),
      announcement({ id: 2, publishDate: '2026-06-01T00:00:00Z' }),
    ]
    renderPopup()
    fireEvent.keyDown(screen.getByRole('dialog'), {
      key: 'Escape',
      code: 'Escape',
    })

    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(screen.getByLabelText('Unread announcements')).toHaveTextContent('1')
  })

  it('the footer closes the popup and marks due announcements read without consuming future ones', async () => {
    const older = announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' })
    const newer = announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' })
    const future = announcement({ id: 3, publishDate: '2026-06-01T00:00:00Z' })
    announcementsRef.current = [older, newer, future]
    renderPopup()

    fireEvent.click(screen.getByRole('button', { name: 'Close dialog' }))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      expect(screen.getByLabelText('Unread announcements')).toHaveTextContent(
        '1'
      )
    })
    expect(useNotificationStore.getState().readAnnouncementKeys).toEqual(
      expect.arrayContaining([
        getAnnouncementKey(older),
        getAnnouncementKey(newer),
      ])
    )
    expect(useNotificationStore.getState().readAnnouncementKeys).not.toContain(
      getAnnouncementKey(future)
    )
  })
})
