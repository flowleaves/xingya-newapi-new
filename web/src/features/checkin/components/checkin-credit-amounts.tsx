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

import { formatQuotaWithCurrency } from '@/lib/currency'

import type { CheckinCreditAmounts as CreditAmounts } from '../types'

export function CheckinCreditAmounts(props: { credit: CreditAmounts }) {
  const { t } = useTranslation()
  return (
    <dl className='grid grid-cols-2 gap-3 text-sm'>
      <div className='min-w-0 rounded-lg border p-3'>
        <dt className='text-muted-foreground text-xs'>
          {t('Available check-in credit')}
        </dt>
        <dd className='mt-1 font-semibold break-all tabular-nums'>
          {formatQuotaWithCurrency(props.credit.available)}
        </dd>
      </div>
      <div className='min-w-0 rounded-lg border p-3'>
        <dt className='text-muted-foreground text-xs'>
          {t('Reserved for active requests')}
        </dt>
        <dd className='mt-1 font-semibold break-all tabular-nums'>
          {formatQuotaWithCurrency(props.credit.reserved)}
        </dd>
      </div>
      <div className='min-w-0 rounded-lg border p-3'>
        <dt className='text-muted-foreground text-xs'>
          {t('Check-in credit consumed')}
        </dt>
        <dd className='mt-1 font-semibold break-all tabular-nums'>
          {formatQuotaWithCurrency(props.credit.consumed)}
        </dd>
      </div>
      <div className='min-w-0 rounded-lg border p-3'>
        <dt className='text-muted-foreground text-xs'>
          {t('Expired check-in credit')}
        </dt>
        <dd className='mt-1 font-semibold break-all tabular-nums'>
          {formatQuotaWithCurrency(props.credit.expired)}
        </dd>
      </div>
      {(props.credit.borrowed ?? 0) > 0 && (
        <div className='min-w-0 rounded-lg border p-3'>
          <dt className='text-muted-foreground text-xs'>
            {t('Check-in overdraft')}
          </dt>
          <dd className='mt-1 font-semibold break-all tabular-nums'>
            {formatQuotaWithCurrency(props.credit.borrowed ?? 0)}
          </dd>
        </div>
      )}
    </dl>
  )
}
