/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Card, CardContent } from '@/components/ui/card'
import { CheckinCalendarCard } from '@/features/profile/components/checkin-calendar-card'
import { useStatus } from '@/hooks/use-status'

/**
 * Standalone check-in page.
 *
 * The profile page already hosts the same calendar card; this page exists as a
 * direct entry point from the sidebar. It deliberately reuses that card rather
 * than reimplementing the calendar, so both surfaces share one implementation
 * (and one API contract).
 */
export function CheckinPage() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const checkinEnabled = status?.checkin_enabled === true
  // The card can gate its own submit with Turnstile, so it needs the same
  // site-key wiring the profile page passes.
  const turnstileEnabled = !!(
    status?.turnstile_check && status?.turnstile_site_key
  )
  const turnstileSiteKey = status?.turnstile_site_key || ''

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Daily Check-in')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-3xl space-y-4'>
          {checkinEnabled ? (
            <CheckinCalendarCard
              checkinEnabled={checkinEnabled}
              turnstileEnabled={turnstileEnabled}
              turnstileSiteKey={turnstileSiteKey}
            />
          ) : (
            <Card data-card-hover='false'>
              <CardContent className='text-muted-foreground flex items-center gap-2 py-10 text-sm'>
                <Gift className='size-4' aria-hidden='true' />
                {t('Check-in is not enabled by the administrator.')}
              </CardContent>
            </Card>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
