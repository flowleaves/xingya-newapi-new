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
import { describe, expect, it } from 'vitest'

import { getAnnouncementKey } from '@/lib/announcement-key'
import {
  announcementKeys,
  dismissableAnnouncements,
  selectAnnouncementForPopup,
} from '@/lib/announcement-popup'

const NOW = Date.parse('2026-05-10T12:00:00Z')

function announcement(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    content: 'content',
    publishDate: '2026-05-01T00:00:00Z',
    type: 'default',
    popup: true,
    ...overrides,
  }
}

const nothingRead = () => false

describe('popup announcement eligibility', () => {
  it('ignores an unmarked announcement even when it is the newest', () => {
    const items = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({
        id: 2,
        popup: false,
        publishDate: '2026-05-09T00:00:00Z',
      }),
    ]

    // The unmarked item is strictly newer, so returning it here would mean the flag
    // was not being consulted at all.
    expect(selectAnnouncementForPopup(items, nothingRead, NOW)).toMatchObject({
      id: 1,
    })
  })

  it('returns nothing when no announcement is marked', () => {
    const items = [
      announcement({ popup: false }),
      announcement({ id: 2, popup: false }),
    ]

    expect(selectAnnouncementForPopup(items, nothingRead, NOW)).toBeNull()
  })

  it('withholds an announcement whose publish date has not arrived', () => {
    // The announcements list sorts by date but never filters on it, so a future-dated
    // announcement would otherwise pop before it is meant to be visible.
    const future = announcement({
      id: 9,
      publishDate: '2026-06-01T00:00:00Z',
    })

    expect(selectAnnouncementForPopup([future], nothingRead, NOW)).toBeNull()

    const due = announcement({
      id: 9,
      publishDate: '2026-05-10T11:59:59Z',
    })
    expect(selectAnnouncementForPopup([due], nothingRead, NOW)).toMatchObject({
      id: 9,
    })
  })

  it('skips an announcement this browser already dismissed', () => {
    const alreadyRead = announcement({ id: 4 })
    const readKeys = new Set([getAnnouncementKey(alreadyRead)])

    expect(
      selectAnnouncementForPopup([alreadyRead], (key) => readKeys.has(key), NOW)
    ).toBeNull()
  })

  it('shows the newest of several due announcements', () => {
    const items = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' }),
      announcement({ id: 3, publishDate: '2026-05-05T00:00:00Z' }),
    ]

    expect(selectAnnouncementForPopup(items, nothingRead, NOW)).toMatchObject({
      id: 2,
    })
    expect(dismissableAnnouncements(items, nothingRead, NOW).map((i) => i.id))
      .toEqual([2, 3, 1])
  })
})

describe('popup announcement dismissal', () => {
  it('dismissing the dialog silences every announcement that was due', () => {
    // This is the contract that keeps a batch of announcements from becoming a chain of
    // blocking dialogs: the dialog re-reads the store after closing, so the dismissal
    // has to cover the whole due set, not only the item that was on screen.
    const items = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' }),
    ]

    const due = dismissableAnnouncements(items, nothingRead, NOW)
    expect(due).toHaveLength(2)

    // Replay the dismissal exactly as the component does, then re-evaluate.
    const readKeys = new Set(announcementKeys(due))
    expect(readKeys).toEqual(new Set(['id:2', 'id:1']))
    expect(
      selectAnnouncementForPopup(items, (key) => readKeys.has(key), NOW)
    ).toBeNull()
  })

  it('leaves announcements that are not yet due untouched by a dismissal', () => {
    const items = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({ id: 2, publishDate: '2026-06-01T00:00:00Z' }),
    ]

    const due = dismissableAnnouncements(items, nothingRead, NOW)
    expect(announcementKeys(due)).toEqual(['id:1'])

    // The future one must still be able to pop once its date arrives.
    const later = Date.parse('2026-06-02T00:00:00Z')
    const readKeys = new Set(announcementKeys(due))
    expect(
      selectAnnouncementForPopup(items, (key) => readKeys.has(key), later)
    ).toMatchObject({ id: 2 })
  })
})

describe('popup announcement robustness', () => {
  it('ignores a malformed publish date instead of throwing', () => {
    const items = [announcement({ id: 1, publishDate: 'not-a-date' })]

    expect(selectAnnouncementForPopup(items, nothingRead, NOW)).toBeNull()
  })

  it('ignores an empty or missing publish date', () => {
    expect(
      selectAnnouncementForPopup(
        [announcement({ id: 1, publishDate: '' })],
        nothingRead,
        NOW
      )
    ).toBeNull()
    expect(
      selectAnnouncementForPopup(
        [announcement({ id: 1, publishDate: undefined })],
        nothingRead,
        NOW
      )
    ).toBeNull()
  })

  it('ignores a non-boolean popup flag', () => {
    // The backend rejects a non-boolean flag, so this only guards against hand-edited
    // stored JSON; a truthy string must not be treated as opt-in.
    const items = [announcement({ id: 1, popup: 'true' })]

    expect(selectAnnouncementForPopup(items, nothingRead, NOW)).toBeNull()
  })

  it('does not reorder the caller array', () => {
    const items = [
      announcement({ id: 1, publishDate: '2026-05-01T00:00:00Z' }),
      announcement({ id: 2, publishDate: '2026-05-08T00:00:00Z' }),
    ]
    const original = items.map((item) => item.id)

    dismissableAnnouncements(items, nothingRead, NOW)

    expect(items.map((item) => item.id)).toEqual(original)
  })
})
