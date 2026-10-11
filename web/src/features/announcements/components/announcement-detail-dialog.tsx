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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { RichContent } from '@/components/rich-content'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatDateTimeObject } from '@/lib/time'

interface AnnouncementDetailModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  announcement: {
    title?: string
    content?: string
    tag?: string
    publishDate?: string
    extra?: string
  } | null
}

/**
 * The one rendering of an announcement's full text.
 *
 * Shared by the dashboard panel and the blocking popup so the two cannot drift; the
 * popup previously carried its own copy of this composition.
 */
export function AnnouncementDetailModal({
  open,
  onOpenChange,
  announcement,
}: AnnouncementDetailModalProps) {
  const { t } = useTranslation()
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={announcement?.title?.trim() || t('Announcement Details')}
      description={
        announcement?.tag || announcement?.publishDate ? (
          <span className='flex flex-wrap items-center gap-x-3 gap-y-2'>
            {announcement?.tag && (
              <Badge
                variant='secondary'
                className='h-auto max-w-full break-words whitespace-normal'
              >
                {announcement.tag}
              </Badge>
            )}
            {announcement?.publishDate && (
              <span>
                {t('Published:')}{' '}
                <time dateTime={announcement.publishDate}>
                  {formatDateTimeObject(new Date(announcement.publishDate))}
                </time>
              </span>
            )}
          </span>
        ) : undefined
      }
      contentClassName='rounded-2xl sm:max-w-xl'
      contentHeight='auto'
      headerClassName='gap-3'
      titleClassName='pr-6 text-lg leading-snug font-semibold break-words sm:text-xl'
      bodyClassName='flex flex-col gap-5 py-2'
      footerClassName='sm:py-4'
      footer={
        <Button
          className='min-w-24'
          aria-label={t('Close dialog')}
          onClick={() => onOpenChange(false)}
        >
          {t('Close')}
        </Button>
      }
    >
      {announcement?.content && (
        <RichContent
          breaks
          content={announcement.content}
          className='text-sm leading-7 break-words'
        />
      )}
      {announcement?.extra && (
        <section className='bg-muted/50 flex flex-col gap-2 rounded-xl p-3'>
          <h4 className='text-muted-foreground text-xs font-medium'>
            {t('Additional Information')}
          </h4>
          <RichContent
            breaks
            content={announcement.extra}
            className='text-muted-foreground text-sm leading-6 break-words'
          />
        </section>
      )}
    </Dialog>
  )
}
