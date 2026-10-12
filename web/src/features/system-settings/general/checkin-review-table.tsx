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
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePagination, StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useSystemConfigStore } from '@/stores/system-config-store'

interface Review {
  request_id: string
  user_id: number
  state: string
  reserved: number
  actual_quota: number
  consumed: number
  unpaid_quota: number
  updated_at: number
}
type Resolution = 'waive' | 'release' | 'settle'

export function CheckinReviewTable() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const quotaPerUnit = useSystemConfigStore(
    (s) => s.config.currency.quotaPerUnit
  )
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [selected, setSelected] = useState<Review>()
  const [action, setAction] = useState<Resolution>('release')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const query = useQuery({
    queryKey: ['checkin-reviews', pagination],
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data: { items: Review[]; total: number }
      }>('/api/xingya/checkin/reviews', {
        params: { p: pagination.pageIndex + 1, size: pagination.pageSize },
      })
      return requireServerSuccess(response.data).data
    },
  })
  const data = query.data?.items ?? []
  const table = useReactTable({
    data,
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: query.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
  })
  const actual =
    action === 'settle' ? Math.round((Number(amount) / 100) * quotaPerUnit) : 0
  const validAmount =
    action !== 'settle' ||
    (amount.trim() !== '' &&
      Number.isSafeInteger(actual) &&
      actual >= 0 &&
      actual <= (selected?.reserved ?? 0))
  const resolve = useMutation({
    mutationFn: async () => {
      if (!selected || !validAmount) throw new Error(t('Invalid amount'))
      const response = await api.post('/api/xingya/checkin/reviews/resolve', {
        request_id: selected.request_id,
        updated_at: selected.updated_at,
        action,
        actual_quota: actual,
        reason,
      })
      requireServerSuccess(response.data)
    },
    onSuccess: async () => {
      setSelected(undefined)
      setReason('')
      await queryClient.invalidateQueries({ queryKey: ['checkin-reviews'] })
      await queryClient.invalidateQueries({ queryKey: ['xingya-checkin'] })
    },
  })
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <section className='flex min-w-0 flex-col gap-3 border-t pt-6'>
      <h3 className='font-semibold'>
        {t('Check-in requests awaiting review')}
      </h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Verify upstream evidence before resolving requests. Reservations older than 24 hours can be settled or released. Unpaid differences can be waived. Regular balances are never charged.'
        )}
      </p>
      <StaticDataTable
        data={data}
        getRowKey={(row) => row.request_id}
        emptyContent={
          query.isPending
            ? t('Loading...')
            : t('No check-in requests need review.')
        }
        columns={[
          {
            id: 'request',
            header: t('Request ID'),
            cell: (row) => (
              <span className='block max-w-48 font-mono text-xs break-all'>
                {row.request_id}
                <br />#{row.user_id}
              </span>
            ),
          },
          {
            id: 'amount',
            header: t('Quota'),
            cell: (row) => (
              <span className='whitespace-nowrap'>
                {formatQuotaWithCurrency(
                  row.state === 'review' ? row.unpaid_quota : row.reserved
                )}
              </span>
            ),
          },
          {
            id: 'action',
            header: t('Actions'),
            cell: (row) => (
              <Button
                variant='outline'
                className='h-auto min-h-11 py-2 whitespace-normal'
                onClick={() => {
                  setSelected(row)
                  setAction(row.state === 'review' ? 'waive' : 'release')
                  setAmount('')
                  setReason('')
                }}
              >
                {t('Review')}
              </Button>
            ),
          },
        ]}
      />
      <DataTablePagination table={table} compact />
      <ConfirmDialog
        open={!!selected}
        onOpenChange={(open) => {
          if (!open && !resolve.isPending) setSelected(undefined)
        }}
        title={t('Confirm check-in reconciliation')}
        desc={t(
          'Only confirm after verifying this request. The original cost and audit history remain recorded.'
        )}
        handleConfirm={() => resolve.mutate()}
        disabled={reason.trim().length < 4 || !validAmount}
        isLoading={resolve.isPending}
      >
        <div className='flex flex-col gap-4'>
          {selected?.state === 'reserved' && (
            <div className='space-y-2'>
              <Label htmlFor='checkin-review-action'>{t('Action')}</Label>
              <NativeSelect
                id='checkin-review-action'
                value={action}
                onChange={(event) =>
                  setAction(event.target.value as Resolution)
                }
              >
                <NativeSelectOption value='release'>
                  {t('Release verified failed request')}
                </NativeSelectOption>
                <NativeSelectOption value='settle'>
                  {t('Settle verified charge')}
                </NativeSelectOption>
              </NativeSelect>
              {action === 'settle' && (
                <div className='space-y-2'>
                  <Label htmlFor='checkin-review-amount'>
                    {t('Verified charge (sprouts)')}
                  </Label>
                  <Input
                    id='checkin-review-amount'
                    type='number'
                    inputMode='decimal'
                    min='0'
                    step='0.01'
                    value={amount}
                    onChange={(event) => setAmount(event.target.value)}
                  />
                  <p className='text-muted-foreground text-xs'>
                    {t('Reserved for active requests')}:{' '}
                    {formatQuotaWithCurrency(selected.reserved)}
                  </p>
                </div>
              )}
            </div>
          )}
          {selected?.state === 'review' && (
            <p className='text-sm'>
              {t('Waive unpaid difference')}:{' '}
              {formatQuotaWithCurrency(selected.unpaid_quota)}
            </p>
          )}
          <div className='space-y-2'>
            <Label htmlFor='checkin-review-reason'>
              {t('Verified evidence')}
            </Label>
            <Textarea
              id='checkin-review-reason'
              value={reason}
              maxLength={500}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
        </div>
      </ConfirmDialog>
    </section>
  )
}
