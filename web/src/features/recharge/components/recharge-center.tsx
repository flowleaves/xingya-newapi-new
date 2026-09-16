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
import { Banknote, ExternalLink, RotateCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

interface RechargeCenterProps {
  shopUrl: string
}

/**
 * Recharge centre: the third-party card shop is embedded in an iframe.
 *
 * The sandbox deliberately keeps `allow-same-origin` together with
 * `allow-scripts` because the shop needs its own session to survive the embed
 * (users sign in on the shop page itself). When the shop sends
 * `X-Frame-Options`/`frame-ancestors` that forbid embedding, the frame stays
 * blank — the "open in new window" action is the fallback, which is why it sits
 * in the header rather than in a menu.
 */
export function RechargeCenter(props: RechargeCenterProps) {
  const { t } = useTranslation()
  const [frameKey, setFrameKey] = useState(0)

  return (
    <div className='flex h-full flex-col'>
      <div className='bg-background/95 border-border/60 flex flex-wrap items-center justify-between gap-2 border-b px-4 py-2.5 backdrop-blur-sm'>
        <div className='flex items-center gap-2 text-sm font-semibold'>
          <Banknote className='text-primary size-4' aria-hidden='true' />
          {t('Recharge Center')}
        </div>
        <div className='flex items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            render={
              <a
                href={props.shopUrl}
                target='_blank'
                rel='noopener noreferrer'
              />
            }
          >
            {t('Open in new window')}
            <ExternalLink className='size-3.5' aria-hidden='true' />
          </Button>
          <Button
            variant='ghost'
            size='sm'
            onClick={() => setFrameKey((key) => key + 1)}
          >
            <RotateCw className='size-3.5' aria-hidden='true' />
            {t('Refresh')}
          </Button>
        </div>
      </div>

      {/* `allow-scripts` together with `allow-same-origin` normally lets framed
          content drop its own sandbox, because it could then reach the parent.
          That only holds for a same-origin frame. This frame is the third-party
          card shop on its own origin, so it cannot touch this document, while
          keeping its own origin is exactly what lets the shop's session and
          cookies work inside the embed. The rule is a same-origin heuristic, so
          it is suppressed here rather than weakening the shop's embed. */}
      <iframe
        key={frameKey}
        src={props.shopUrl}
        title={t('Recharge Center')}
        className='h-full min-h-0 w-full flex-1 border-0 bg-white'
        // oxlint-disable-next-line react/iframe-missing-sandbox
        sandbox='allow-forms allow-same-origin allow-popups allow-popups-to-escape-sandbox allow-scripts'
      />

      <p className='text-muted-foreground/70 flex items-center gap-1.5 px-4 py-2 text-xs'>
        <Banknote className='size-3.5' aria-hidden='true' />
        {t(
          'The shop is embedded below. If it refuses to load, use "Open in new window" above.'
        )}
      </p>
    </div>
  )
}
