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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BadgeCheck, Loader2, RefreshCcw, Wand2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { getRefundable, postRefund } from '@/features/usage-logs/api'
import type { RefundableLogItem } from '@/features/usage-logs/types'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { cn } from '@/lib/utils'

/** Refund reasons the backend can report, kept in sync with JudgeSelfRefund. */
const REFUND_REASON_KEYS: Record<string, string> = {
  empty_response: 'Empty response',
  stream_truncated: 'Stream truncated',
}

function formatTimestamp(ts: number): string {
  if (!ts) return '-'
  const date = new Date(ts * 1000)
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function formatQuota(quota: number): string {
  if (!quota) return '-'
  return formatQuotaWithCurrency(quota)
}

export function SelfRefundPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: ['self-refund', 'refundable'],
    queryFn: () => getRefundable(),
  })

  // The server is the only authority on what has been refunded; this set merely
  // hides rows the user just refunded so the list does not need a full refetch.
  const [refundedLogIds, setRefundedLogIds] = useState<ReadonlySet<number>>(
    new Set()
  )
  const [bulkRefunding, setBulkRefunding] = useState(false)

  // This page receives real primary keys from /api/log/self/refundable, so it
  // selects by logId. The inline card in the details dialog must use request_id
  // instead, because the log *list* only carries a page-relative display index
  // as its `id`.
  const refundMutation = useMutation({
    mutationFn: (logId: number) => postRefund({ logId }),
    onSuccess: (response, logId) => {
      if (response.success) {
        setRefundedLogIds((previous) => new Set(previous).add(logId))
        toast.success(t('Refund successful'))
        return
      }
      toast.error(response.message || t('Refund failed'))
    },
    onError: () => toast.error(t('Refund failed')),
  })

  const setting = query.data?.data?.setting
  const logs = useMemo(
    () => query.data?.data?.logs ?? [],
    [query.data?.data?.logs]
  )

  const visibleLogs = useMemo(
    () => logs.filter((log) => !refundedLogIds.has(log.log_id)),
    [logs, refundedLogIds]
  )

  async function handleRefundAll() {
    setBulkRefunding(true)
    let succeeded = 0
    let failed = 0
    for (const log of visibleLogs) {
      try {
        const response = await postRefund({ logId: log.log_id })
        if (response.success) {
          succeeded += 1
          setRefundedLogIds((previous) => new Set(previous).add(log.log_id))
        } else {
          failed += 1
        }
      } catch {
        failed += 1
      }
    }
    setBulkRefunding(false)
    if (succeeded > 0) {
      toast.success(t('{{count}} requests refunded', { count: succeeded }))
    }
    if (failed > 0) {
      toast.error(t('{{count}} refunds failed', { count: failed }))
    }
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Refund Request')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-3xl space-y-4'>
          <Card data-card-hover='false'>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'>
                <Wand2 className='size-4' aria-hidden='true' />
                {t('Self refund')}
              </CardTitle>
              <CardDescription>
                {t(
                  setting?.only_per_request
                    ? 'Refund per-request charges for empty responses or truncated streams.'
                    : 'Refund usage where you paid but received no content (empty responses or truncated streams).'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-4'>
              {!setting?.enabled ? (
                <p className='text-muted-foreground text-sm'>
                  {t('Self-refund is not enabled by the administrator.')}
                </p>
              ) : (
                <>
                  <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
                    <Stat
                      label={t('Ratio')}
                      value={
                        setting ? `${Math.round(setting.ratio * 100)}%` : '-'
                      }
                    />
                    <Stat
                      label={t('Window')}
                      value={setting ? `${setting.window_hours}h` : '-'}
                    />
                    <Stat
                      label={t('Refund times')}
                      value={String(setting.total_used?.count ?? 0)}
                    />
                    <Stat
                      label={t('Total refunded')}
                      value={formatQuota(setting.total_used?.quota ?? 0)}
                    />
                  </div>

                  <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => {
                        void queryClient.invalidateQueries({
                          queryKey: ['self-refund', 'refundable'],
                        })
                        setRefundedLogIds(new Set())
                      }}
                    >
                      <RefreshCcw className='mr-1 size-4' aria-hidden='true' />
                      {t('Refresh')}
                    </Button>
                    <Button
                      size='sm'
                      disabled={visibleLogs.length === 0 || bulkRefunding}
                      onClick={() => void handleRefundAll()}
                    >
                      {bulkRefunding ? (
                        <Loader2
                          className='mr-1 size-4 animate-spin'
                          aria-hidden='true'
                        />
                      ) : (
                        <BadgeCheck
                          className='mr-1 size-4'
                          aria-hidden='true'
                        />
                      )}
                      {t('Refund all ({{count}})', {
                        count: visibleLogs.length,
                      })}
                    </Button>
                  </div>
                </>
              )}
            </CardContent>
          </Card>

          {setting?.enabled && (
            <Card data-card-hover='false'>
              <CardHeader>
                <CardTitle>{t('Eligible logs')}</CardTitle>
                <CardDescription>
                  {t('Requests within the window that qualify for a refund.')}
                </CardDescription>
              </CardHeader>
              <CardContent>{renderEligibleLogs()}</CardContent>
            </Card>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )

  function renderEligibleLogs() {
    if (query.isLoading) {
      return (
        <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
          <Loader2 className='size-4 animate-spin' aria-hidden='true' />
          {t('Loading...')}
        </div>
      )
    }
    if (query.isError) {
      return (
        <p className='text-muted-foreground py-8 text-center text-sm'>
          {t('Failed to load refundable logs.')}
        </p>
      )
    }
    if (visibleLogs.length === 0) {
      return (
        <p className='text-muted-foreground py-8 text-center text-sm'>
          {t('No eligible logs right now.')}
        </p>
      )
    }
    return (
      <ul className='divide-y'>
        {visibleLogs.map((log) => (
          <RefundableRow
            key={log.log_id}
            log={log}
            loading={
              refundMutation.isPending &&
              refundMutation.variables === log.log_id
            }
            onRefund={() => refundMutation.mutate(log.log_id)}
          />
        ))}
      </ul>
    )
  }
}

function Stat(props: { label: string; value: string }) {
  return (
    <div className='bg-muted/40 rounded-lg p-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='mt-1 text-lg font-semibold'>{props.value}</div>
    </div>
  )
}

function RefundableRow(props: {
  log: RefundableLogItem
  loading: boolean
  onRefund: () => void
}) {
  const { t } = useTranslation()
  const isSubscription = props.log.funding_source === 'subscription'
  const reasonKey = REFUND_REASON_KEYS[props.log.reason]

  return (
    <li className='flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between'>
      <div className='min-w-0'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='truncate font-mono text-sm'>
            {props.log.model_name || '-'}
          </span>
          <span
            className={cn(
              'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs',
              props.log.reason === 'empty_response'
                ? 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300'
                : 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300'
            )}
          >
            {reasonKey ? t(reasonKey) : props.log.reason}
          </span>
          <span
            className={cn(
              'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs',
              isSubscription
                ? 'bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-300'
                : 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300'
            )}
          >
            {isSubscription ? t('Subscription') : t('Wallet')}
          </span>
        </div>
        <div className='text-muted-foreground mt-1 text-xs'>
          {formatTimestamp(props.log.created_at)}
        </div>
        <div className='text-muted-foreground mt-0.5 truncate text-xs'>
          {t('Base')}: {formatQuota(props.log.base_quota)} · {t('Refund')}:{' '}
          {formatQuota(props.log.refund_amount)}
        </div>
      </div>
      <div className='shrink-0'>
        <Button
          size='sm'
          variant='outline'
          disabled={props.loading}
          onClick={props.onRefund}
        >
          {props.loading ? (
            <Loader2 className='mr-1 size-4 animate-spin' aria-hidden='true' />
          ) : (
            <BadgeCheck className='mr-1 size-4' aria-hidden='true' />
          )}
          {t('Refund')}
        </Button>
      </div>
    </li>
  )
}
