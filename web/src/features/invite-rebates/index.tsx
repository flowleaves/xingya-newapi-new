import { useQuery } from '@tanstack/react-query'
import {
  ArrowDownToLine,
  Clock3,
  Coins,
  Sprout,
  Sparkles,
  Users,
  WalletCards,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { TransferDialog } from '@/features/wallet/components/dialogs/transfer-dialog'
import { useAffiliate, useTopupInfo } from '@/features/wallet/hooks'
import { formatNumber, formatQuota } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { formatDateTimeObject } from '@/lib/time'

import { getInviteRebates } from './api'
import { buildInviteLink } from './lib'
import type {
  InviteRebateItem,
  InviteRebateSourceType,
  InviteRebateStatus,
} from './types'

const PAGE_SIZE = 20

function SourceBadge(props: { source: InviteRebateSourceType }) {
  const { t } = useTranslation()
  const isRedemption = props.source === 'redemption'
  return (
    <span className='inline-flex items-center gap-1.5'>
      <span
        className={
          isRedemption
            ? 'size-1.5 rounded-full bg-amber-500'
            : 'bg-primary size-1.5 rounded-full'
        }
      />
      {isRedemption ? t('Redemption code') : t('Wallet top-up')}
    </span>
  )
}

function StatusBadge(props: { status: InviteRebateStatus }) {
  const { t } = useTranslation()
  if (props.status === 'capped') {
    return (
      <Badge
        variant='outline'
        className='border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-300'
      >
        {t('Cap reached')}
      </Badge>
    )
  }
  return <Badge variant='secondary'>{t('Paid')}</Badge>
}

function RebateMobileCard(props: { item: InviteRebateItem }) {
  const { t } = useTranslation()
  return (
    <div className='bg-muted/20 rounded-lg border p-3 text-xs'>
      <div className='flex items-center justify-between gap-3'>
        <span className='font-medium'>{props.item.invitee_username}</span>
        <StatusBadge status={props.item.status} />
      </div>
      <div className='text-muted-foreground mt-2 grid grid-cols-2 gap-1.5'>
        <span>{t('Source')}</span>
        <span className='text-right'>
          <SourceBadge source={props.item.source_type} />
        </span>
        <span>{t('Recharge amount')}</span>
        <span className='text-right tabular-nums'>
          {formatQuota(props.item.base_quota)}
        </span>
        <span>{t('Rebate')}</span>
        <span className='text-primary text-right font-semibold tabular-nums'>
          {formatQuota(props.item.rebate_quota)}
        </span>
        <span>{t('Time')}</span>
        <span className='text-right'>
          {formatDateTimeObject(new Date(props.item.occurred_at * 1000))}
        </span>
      </div>
    </div>
  )
}

export function InviteRebates() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [transferOpen, setTransferOpen] = useState(false)
  const query = useQuery({
    queryKey: ['invite-rebates', page],
    queryFn: async () =>
      requireServerSuccess(await getInviteRebates(page, PAGE_SIZE)),
    staleTime: 30_000,
  })
  const { affiliateCode, transferring, transferQuota } = useAffiliate()
  const { topupInfo } = useTopupInfo()

  const payload = query.data?.data
  const summary = payload?.summary
  const items = payload?.items ?? []
  const total = payload?.total ?? 0
  const code = affiliateCode || summary?.aff_code || ''
  const inviteLink = buildInviteLink(code)
  const complianceConfirmed =
    topupInfo?.payment_compliance_confirmed !== false
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  if (query.isLoading) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Invite Rebates')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-5xl flex-col gap-3'>
            <div className='grid gap-3 sm:grid-cols-3'>
              {[1, 2, 3].map((key) => (
                <Skeleton key={key} className='h-24 rounded-xl' />
              ))}
            </div>
            <Skeleton className='h-48 rounded-xl' />
            <Skeleton className='h-64 rounded-xl' />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  if (query.isError || !summary) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Invite Rebates')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <ErrorState onRetry={() => void query.refetch()} />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Invite Rebates')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {inviteLink && (
          <CopyButton
            value={inviteLink}
            variant='outline'
            tooltip={t('Copy invite link')}
            aria-label={t('Copy invite link')}
          />
        )}
        <Button
          size='sm'
          onClick={() => setTransferOpen(true)}
          disabled={summary.aff_quota <= 0 || !complianceConfirmed}
        >
          <ArrowDownToLine className='size-4' />
          {t('Transfer to wallet')}
        </Button>
      </SectionPageLayout.Actions>

      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-5xl flex-col gap-3'>
          <div className='grid gap-3 sm:grid-cols-3'>
            <Card className='py-0'>
              <CardContent className='p-4'>
                <div className='flex items-center gap-2'>
                  <IconBadge tone='success' size='sm'>
                    <Sparkles />
                  </IconBadge>
                  <span className='text-muted-foreground text-xs font-medium'>
                    {t('Total rebates')}
                  </span>
                </div>
                <div className='mt-2 font-mono text-xl font-bold tabular-nums'>
                  {formatQuota(summary.total_rebate_quota)}
                </div>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Since the invite rebate launch')}
                </p>
              </CardContent>
            </Card>

            <Card className='py-0'>
              <CardContent className='p-4'>
                <div className='flex items-center gap-2'>
                  <IconBadge tone='chart-3' size='sm'>
                    <WalletCards />
                  </IconBadge>
                  <span className='text-muted-foreground text-xs font-medium'>
                    {t('Available to transfer')}
                  </span>
                </div>
                <div className='mt-2 font-mono text-xl font-bold tabular-nums'>
                  {formatQuota(summary.aff_quota)}
                </div>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('Transfer to your wallet anytime')}
                </p>
              </CardContent>
            </Card>

            <Card className='py-0'>
              <CardContent className='p-4'>
                <div className='flex items-center gap-2'>
                  <IconBadge tone='info' size='sm'>
                    <Users />
                  </IconBadge>
                  <span className='text-muted-foreground text-xs font-medium'>
                    {t('Invited users')}
                  </span>
                </div>
                <div className='mt-2 font-mono text-xl font-bold tabular-nums'>
                  {formatNumber(summary.invited_count)}
                </div>
                <p className='text-muted-foreground mt-1 text-xs'>
                  {t('{{count}} still in the window', {
                    count: formatNumber(summary.active_window_count),
                  })}
                </p>
              </CardContent>
            </Card>
          </div>

          <Card className='border-emerald-100 bg-gradient-to-br from-emerald-50/80 via-background to-amber-50/60 py-0 dark:border-emerald-900/40 dark:from-emerald-950/20 dark:to-amber-950/10'>
            <CardContent className='p-4 sm:p-5'>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div>
                  <div className='flex items-center gap-2'>
                    <img
                      src='/logo.png'
                      alt=''
                      className='size-6 rounded-md object-contain'
                    />
                    <h3 className='text-sm font-semibold'>
                      {t('Your invite code')}
                    </h3>
                    <Badge variant='outline' className='text-[10px]'>
                      {t('Grow together')}
                    </Badge>
                  </div>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t('Share your code or link with friends.')}
                  </p>
                </div>
                <Badge variant='secondary' className='max-w-full text-[10px]'>
                  {t('Max {{value}} sprouts per invitee', {
                    value: formatNumber(summary.max_rebate_per_invitee),
                  })}
                </Badge>
              </div>

              <div className='mt-4 grid gap-2 sm:grid-cols-2'>
                <div className='bg-background/70 flex items-center gap-2 rounded-lg border p-2'>
                  <span className='text-muted-foreground text-xs'>
                    {t('Code')}
                  </span>
                  <span className='min-w-0 flex-1 truncate font-mono text-sm font-semibold'>
                    {code || '-'}
                  </span>
                  {code && (
                    <CopyButton
                      value={code}
                      variant='ghost'
                      className='size-7 shrink-0'
                      tooltip={t('Copy invite code')}
                      aria-label={t('Copy invite code')}
                    />
                  )}
                </div>
                <div className='bg-background/70 flex items-center gap-2 rounded-lg border p-2'>
                  <span className='text-muted-foreground text-xs'>
                    {t('Link')}
                  </span>
                  <span className='min-w-0 flex-1 truncate text-xs'>
                    {inviteLink || '-'}
                  </span>
                  {inviteLink && (
                    <CopyButton
                      value={inviteLink}
                      variant='ghost'
                      className='size-7 shrink-0'
                      tooltip={t('Copy invite link')}
                      aria-label={t('Copy invite link')}
                    />
                  )}
                </div>
              </div>

              <div className='mt-3 flex flex-wrap gap-2'>
                <Badge variant='outline' className='gap-1'>
                  <Clock3 className='size-3' />
                  {t('{{days}}-day window', {
                    days: formatNumber(summary.window_days),
                  })}
                </Badge>
                <Badge variant='outline' className='gap-1'>
                  <Coins className='size-3' />
                  {t('{{ratio}}% rebate', {
                    ratio: formatNumber(summary.ratio_percent),
                  })}
                </Badge>
                <Badge variant='outline' className='gap-1'>
                  <Sprout className='size-3' />
                  {t('Wallet top-ups and redemption codes')}
                </Badge>
              </div>

              <p className='text-muted-foreground mt-3 text-xs'>
                {t(
                  'Rebates go to your invite balance and can be transferred to your wallet. Minimum transfer: 1.'
                )}
              </p>
              {!complianceConfirmed && (
                <p className='mt-2 text-xs text-amber-700 dark:text-amber-300'>
                  {t(
                    'Transfers are disabled until the administrator confirms payment compliance terms.'
                  )}
                </p>
              )}
            </CardContent>
          </Card>

          <Card className='py-0'>
            <CardContent className='p-4 sm:p-5'>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div>
                  <h3 className='text-sm font-semibold'>
                    {t('Rebate details')}
                  </h3>
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {t('Only the most recent rebates are shown.')}
                  </p>
                </div>
                <Badge variant='outline' className='text-[10px]'>
                  {t('{{count}} records', {
                    count: formatNumber(total),
                  })}
                </Badge>
              </div>

              {items.length === 0 ? (
                <EmptyState
                  icon={Sprout}
                  title={t('No rebates yet')}
                  description={t(
                    'When a friend recharges or redeems a code within 15 days of signing up, the rebate appears here.'
                  )}
                  bordered
                  className='mt-4 min-h-[220px]'
                />
              ) : (
                <>
                  <div className='mt-4 space-y-2 sm:hidden'>
                    {items.map((item) => (
                      <RebateMobileCard key={item.id} item={item} />
                    ))}
                  </div>

                  <div className='mt-4 hidden sm:block'>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t('Invited user')}</TableHead>
                          <TableHead>{t('Source')}</TableHead>
                          <TableHead>{t('Recharge amount')}</TableHead>
                          <TableHead>{t('Rebate')}</TableHead>
                          <TableHead>{t('Time')}</TableHead>
                          <TableHead>{t('Status')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {items.map((item) => (
                          <TableRow key={item.id}>
                            <TableCell>{item.invitee_username}</TableCell>
                            <TableCell>
                              <SourceBadge source={item.source_type} />
                            </TableCell>
                            <TableCell className='tabular-nums'>
                              {formatQuota(item.base_quota)}
                            </TableCell>
                            <TableCell className='text-primary font-semibold tabular-nums'>
                              {formatQuota(item.rebate_quota)}
                            </TableCell>
                            <TableCell className='text-muted-foreground tabular-nums'>
                              {formatDateTimeObject(
                                new Date(item.occurred_at * 1000)
                              )}
                            </TableCell>
                            <TableCell>
                              <StatusBadge status={item.status} />
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                </>
              )}

              {totalPages > 1 && (
                <div className='mt-4 flex flex-wrap items-center justify-between gap-2'>
                  <span className='text-muted-foreground text-xs'>
                    {t('Page {{page}} of {{total}}', {
                      page: formatNumber(page),
                      total: formatNumber(totalPages),
                    })}
                  </span>
                  <div className='flex gap-2'>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={page <= 1}
                      onClick={() => setPage((current) => current - 1)}
                    >
                      {t('Previous')}
                    </Button>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={page >= totalPages}
                      onClick={() => setPage((current) => current + 1)}
                    >
                      {t('Next')}
                    </Button>
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </SectionPageLayout.Content>

      <TransferDialog
        open={transferOpen}
        onOpenChange={setTransferOpen}
        onConfirm={async (quota) => {
          const success = await transferQuota(quota)
          if (success) {
            await query.refetch()
          }
          return success
        }}
        availableQuota={summary.aff_quota}
        transferring={transferring}
      />
    </SectionPageLayout>
  )
}
