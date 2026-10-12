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
import { useState } from 'react'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Calendar } from '@/components/ui/calendar'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'

import type { CheckinAward } from '../types'

const locales: Record<string, typeof enUS> = {
  en: enUS,
  fr,
  ja,
  ru,
  vi,
  zh: zhCN,
  'zh-CN': zhCN,
  zhCN,
  zhTW,
  'zh-TW': zhTW,
}

function dateKey(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

interface CheckinHistoryProps {
  month: string
  today: string
  records: CheckinAward[]
  onMonthChange: (month: string) => void
}

export function CheckinHistory(props: CheckinHistoryProps) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const locale = toIntlLocale(language)
  const [selected, setSelected] = useState<string>()
  const record = props.records.find((item) => item.checkin_date === selected)
  const checked = props.records.map(
    (item) => new Date(`${item.checkin_date}T12:00:00`)
  )
  return (
    <Card data-card-hover='false'>
      <CardHeader>
        <CardTitle>{t('Check-in Calendar')}</CardTitle>
      </CardHeader>
      <CardContent className='space-y-3'>
        <Calendar
          mode='single'
          locale={locales[language] ?? enUS}
          today={new Date(`${props.today}T12:00:00`)}
          month={new Date(`${props.month}-01T12:00:00`)}
          onMonthChange={(date) => {
            setSelected(undefined)
            props.onMonthChange(dateKey(date).slice(0, 7))
          }}
          startMonth={new Date(2020, 0, 1)}
          endMonth={new Date(`${props.today.slice(0, 7)}-01T12:00:00`)}
          selected={selected ? new Date(`${selected}T12:00:00`) : undefined}
          onSelect={(date) => setSelected(date ? dateKey(date) : undefined)}
          modifiers={{ checked }}
          modifiersClassNames={{
            checked:
              'bg-success/15 text-success font-semibold after:absolute after:bottom-1 after:left-1/2 after:size-1 after:rounded-full after:bg-success',
          }}
          className='w-full p-0 [--cell-size:2.75rem] [&_button[data-day]]:aspect-auto [&_button[data-day]]:h-full [&_button[data-day]]:min-h-11 [&_button[data-day]]:min-w-0'
          classNames={{
            root: 'w-full',
            day: 'relative h-11 min-w-0 flex-1 text-center',
            button_previous: 'size-11',
            button_next: 'size-11',
          }}
          formatters={{
            formatCaption: (date) =>
              date.toLocaleDateString(locale, {
                month: 'long',
                year: 'numeric',
              }),
          }}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Marked dates include both new and legacy check-ins. Select a date to see its credit source.'
          )}
        </p>
      </CardContent>
      <Dialog
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) setSelected(undefined)
        }}
        title={selected || t('Check-in Calendar')}
      >
        {record ? (
          <div className='space-y-2'>
            <p className='text-2xl font-semibold tabular-nums'>
              {formatQuotaWithCurrency(record.quota_awarded)}
            </p>
            <p className='text-muted-foreground text-sm'>
              {record.source === 'checkin'
                ? t('Check-in credit')
                : t('Legacy check-in · ordinary balance')}
            </p>
          </div>
        ) : (
          <p>{t('No check-in record for this date.')}</p>
        )}
      </Dialog>
    </Card>
  )
}
