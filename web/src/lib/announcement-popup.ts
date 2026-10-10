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
import { getAnnouncementKey } from '@/lib/announcement-key'

/**
 * Announcements that may be shown in the blocking dialog right now, newest first.
 *
 * An announcement is eligible only when the operator marked it for the dialog, its
 * publish date has arrived, and this browser has not already dismissed it. The
 * publish-date gate is required because the announcements list sorts by date but never
 * filters on it, so without this check a future-dated announcement would pop early.
 */
export function dismissableAnnouncements(
  announcements: Record<string, unknown>[],
  readKeys: (key: string) => boolean,
  now: number = Date.now()
): Record<string, unknown>[] {
  return announcements
    .filter((item) => {
      if (!item || item.popup !== true) return false

      const publishDate = item.publishDate
      if (typeof publishDate !== 'string' || publishDate === '') return false
      const publishedAt = new Date(publishDate).getTime()
      if (Number.isNaN(publishedAt) || publishedAt > now) return false

      return !readKeys(getAnnouncementKey(item))
    })
    .sort(
      (left, right) =>
        new Date(String(right.publishDate)).getTime() -
        new Date(String(left.publishDate)).getTime()
    )
}

/**
 * Pick the single announcement the dialog should show.
 *
 * Only one is ever returned. The remaining due announcements are not queued behind it:
 * dismissing the dialog marks the whole due set as read, so several announcements can
 * never turn into a chain of blocking dialogs. They stay readable in the notification
 * bell, which shares the same read state.
 */
export function selectAnnouncementForPopup(
  announcements: Record<string, unknown>[],
  readKeys: (key: string) => boolean,
  now: number = Date.now()
): Record<string, unknown> | null {
  return dismissableAnnouncements(announcements, readKeys, now)[0] ?? null
}

/**
 * Storage keys for a set of announcements, used to record that they were seen.
 */
export function announcementKeys(
  announcements: Record<string, unknown>[]
): string[] {
  return announcements.map((item) => getAnnouncementKey(item))
}
