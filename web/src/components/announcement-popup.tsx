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
import { useCallback, useMemo } from 'react'

import { AnnouncementDetailModal } from '@/features/announcements/components/announcement-detail-dialog'
import { useNotifications } from '@/hooks/use-notifications'
import {
  announcementKeys,
  dismissableAnnouncements,
} from '@/lib/announcement-popup'
import { useNotificationStore } from '@/stores/notification-store'

/**
 * Shows the one due announcement as a blocking dialog, once per browser.
 *
 * Dismissing records every announcement that was due, not just the one on screen. The
 * dialog re-renders from the notification store, so marking only the visible item would
 * immediately surface the next one and turn a batch of announcements into a chain of
 * blocking dialogs. Everything dismissed this way stays readable in the notification
 * bell, which reads the same store.
 */
export function AnnouncementPopup() {
  const { popupAnnouncements } = useNotifications()
  const isAnnouncementRead = useNotificationStore(
    (state) => state.isAnnouncementRead
  )
  const markAnnouncementsRead = useNotificationStore(
    (state) => state.markAnnouncementsRead
  )

  const due = useMemo(
    () => dismissableAnnouncements(popupAnnouncements, isAnnouncementRead),
    [popupAnnouncements, isAnnouncementRead]
  )

  const handleOpenChange = useCallback(
    (open: boolean) => {
      if (open) return
      markAnnouncementsRead(announcementKeys(due))
    },
    [due, markAnnouncementsRead]
  )

  const current = due[0]
  if (!current) return null

  return (
    <AnnouncementDetailModal
      open
      onOpenChange={handleOpenChange}
      announcement={{
        title: typeof current.title === 'string' ? current.title : undefined,
        content: typeof current.content === 'string' ? current.content : '',
        tag: typeof current.tag === 'string' ? current.tag : undefined,
        publishDate:
          typeof current.publishDate === 'string'
            ? current.publishDate
            : undefined,
        extra: typeof current.extra === 'string' ? current.extra : undefined,
      }}
    />
  )
}
