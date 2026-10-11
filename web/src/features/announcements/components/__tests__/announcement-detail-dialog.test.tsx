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
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { AnnouncementDetailModal } from '../announcement-detail-dialog'

describe('announcement detail presentation', () => {
  it('uses the announcement title as its accessible name and displays its tag', () => {
    render(
      <AnnouncementDetailModal
        open
        onOpenChange={vi.fn()}
        announcement={{
          title: 'New models are available',
          tag: 'Product update',
          publishDate: '2026-10-11T10:00:00+08:00',
          content: 'Read the **model list** for details.',
        }}
      />
    )

    expect(
      screen.getByRole('dialog', { name: 'New models are available' })
    ).toBeInTheDocument()
    expect(screen.getByText('Product update')).toBeInTheDocument()
    expect(screen.getByText('model list')).toHaveProperty('tagName', 'STRONG')
    expect(screen.queryByRole('heading', { name: 'Content' })).toBeNull()
  })

  it.each([undefined, '', '   '])(
    'keeps a descriptive accessible title when the announcement title is %j',
    (title) => {
      render(
        <AnnouncementDetailModal
          open
          onOpenChange={vi.fn()}
          announcement={{ title, content: 'Legacy announcement' }}
        />
      )

      expect(
        screen.getByRole('dialog', { name: 'Announcement Details' })
      ).toBeInTheDocument()
    }
  )

  it('provides a footer close action that uses the shared close callback', () => {
    const onOpenChange = vi.fn()
    render(
      <AnnouncementDetailModal
        open
        onOpenChange={onOpenChange}
        announcement={{ content: 'Maintenance has completed.' }}
      />
    )

    const close = screen.getByRole('button', { name: 'Close dialog' })
    expect(close.closest('[data-slot=dialog-footer]')).not.toBeNull()
    fireEvent.click(close)
    expect(onOpenChange).toHaveBeenCalledExactlyOnceWith(false)
  })

  it('keeps long content scrollable independently of the close footer', () => {
    render(
      <AnnouncementDetailModal
        open
        onOpenChange={vi.fn()}
        announcement={{
          title: 'An announcement title with enough text to wrap on mobile',
          content: 'Long announcement paragraph.\n\n'.repeat(60),
          extra: 'Keep your existing API configuration.',
        }}
      />
    )

    const dialog = screen.getByRole('dialog')
    const footer = dialog.querySelector('[data-slot=dialog-footer]')
    const content = screen.getAllByText('Long announcement paragraph.')[0]
    const scrollBody = content.closest('.overflow-y-auto')
    expect(scrollBody).toHaveClass('min-h-0', 'overscroll-contain')
    expect(scrollBody?.contains(footer)).toBe(false)
    expect(footer).toHaveClass('flex-shrink-0')
    expect(
      screen.getByRole('heading', {
        name: 'An announcement title with enough text to wrap on mobile',
      })
    ).toHaveClass('break-words', 'pr-6')
    expect(
      screen.getByText('Keep your existing API configuration.')
    ).toBeVisible()
  })
})
