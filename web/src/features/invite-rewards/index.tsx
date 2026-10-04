import {
  ChevronLeft,
  ChevronRight,
  Clock3,
  Gift,
  HandCoins,
  Info,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { CopyButton } from '@/components/copy-button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { getAffiliateCode } from '@/features/wallet/api'
import { generateAffiliateLink } from '@/features/wallet/lib'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { claimInviteReward, getInviteRewards } from './api'
import type { InviteReward, InviteRewardState } from './types'

const PAGE_SIZE = 20

function stateVariant(state: InviteRewardState) {
  if (state === 'eligible') return 'warning' as const
  if (state === 'granted') return 'default' as const
  if (state === 'cancelled') return 'destructive' as const
  return 'secondary' as const
}

function stateLabel(state: InviteRewardState, t: (value: string) => string) {
  return {
    pending: t('Pending'),
    eligible: t('Ready to claim'),
    granted: t('Granted'),
    cancelled: t('Cancelled'),
  }[state]
}

function grantTimeLabel(reward: InviteReward, t: (value: string) => string) {
  if (reward.state === 'eligible' && reward.auto_grant_at) {
    return formatTimestampToDate(reward.auto_grant_at)
  }
  if (reward.state === 'granted' && reward.granted_at) {
    return formatTimestampToDate(reward.granted_at)
  }
  return t('At midnight')
}

function RewardRow({ reward, requiredCalls }: { reward: InviteReward; requiredCalls: number }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const claim = useMutation({
    mutationFn: () => claimInviteReward(reward.id),
    onSuccess: async (response) => {
      if (!response.success) {
        toast.error(response.message || t('Claim failed'))
        return
      }
      toast.success(
        response.data?.already_granted
          ? t('This reward was already granted.')
          : t('Reward claimed successfully.')
      )
      await queryClient.invalidateQueries({ queryKey: ['invite-rewards'] })
    },
    onError: (error) => handleServerError(error, t('Claim failed')),
  })

  const canClaim = reward.state === 'eligible'
  const label = reward.invitee_display_name || reward.invitee_username || `#${reward.invitee_id}`

  return (
    <TableRow>
      <TableCell>
        <div className='min-w-32'>
          <div className='font-medium'>{label}</div>
          <div className='text-muted-foreground text-xs'>#{reward.invitee_id}</div>
        </div>
      </TableCell>
      <TableCell>
        <span className='font-mono tabular-nums'>
          {Math.min(reward.qualifying_calls, requiredCalls)} / {requiredCalls}
        </span>
      </TableCell>
      <TableCell className='font-mono'>{formatQuota(reward.inviter_quota)}</TableCell>
      <TableCell>
        <Badge variant={stateVariant(reward.state)}>{stateLabel(reward.state, t)}</Badge>
      </TableCell>
      <TableCell className='text-muted-foreground text-xs'>
        {grantTimeLabel(reward, t)}
      </TableCell>
      <TableCell className='text-right'>
        {canClaim ? (
          <Button
            size='sm'
            onClick={() => claim.mutate()}
            disabled={claim.isPending}
            aria-label={t('Claim reward')}
          >
            {claim.isPending ? <Loader2 className='animate-spin' /> : <HandCoins />}
            {t('Claim now')}
          </Button>
        ) : null}
      </TableCell>
    </TableRow>
  )
}

export function InviteRewardsPage() {
  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const codeQuery = useQuery({
    queryKey: ['affiliate-code'],
    queryFn: getAffiliateCode,
  })
  const rewardsQuery = useQuery({
    queryKey: ['invite-rewards', page],
    queryFn: () => getInviteRewards(page, PAGE_SIZE),
  })
  const data = rewardsQuery.data?.data
  const code = codeQuery.data?.data || ''
  const link = generateAffiliateLink(code)
  const totalPages = Math.max(1, Math.ceil((data?.total || 0) / PAGE_SIZE))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Invite Rewards')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            void rewardsQuery.refetch()
            void codeQuery.refetch()
          }}
          disabled={rewardsQuery.isFetching || codeQuery.isFetching}
          aria-label={t('Refresh')}
        >
          <RefreshCw className={rewardsQuery.isFetching ? 'animate-spin' : undefined} />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><Gift />{t('Your referral link')}</CardTitle>
              <CardDescription>{t('Share this link to invite new users.')}</CardDescription>
            </CardHeader>
            <CardContent className='grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'>
              <div className='flex min-w-0 items-center gap-2'>
                <Input readOnly value={link} className='min-w-0 font-mono text-xs' aria-label={t('Referral link')} />
                <CopyButton
                  value={link}
                  variant='outline'
                  className='size-9 shrink-0'
                  tooltip={t('Copy referral link')}
                  aria-label={t('Copy referral link')}
                />
              </div>
              <div className='flex items-center gap-2 text-sm'>
                <span className='text-muted-foreground'>{t('Invite code')}</span>
                <code className='rounded bg-muted px-2 py-1 font-mono'>{code || t('Loading')}</code>
                <CopyButton
                  value={code}
                  variant='ghost'
                  className='size-8'
                  tooltip={t('Copy invite code')}
                  aria-label={t('Copy invite code')}
                />
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Reward progress')}</CardTitle>
              <CardDescription>{t('{{count}} invited accounts', { count: data?.total || 0 })}</CardDescription>
            </CardHeader>
            <CardContent className='p-0'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Invited account')}</TableHead>
                    <TableHead>{t('Successful calls')}</TableHead>
                    <TableHead>{t('Reward')}</TableHead>
                    <TableHead>{t('Status')}</TableHead>
                    <TableHead>{t('Automatic grant')}</TableHead>
                    <TableHead className='text-right'>{t('Action')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data?.items?.length ? data.items.map((reward) => (
                    <RewardRow key={reward.id} reward={reward} requiredCalls={data.required_calls} />
                  )) : (
                    <TableRow><TableCell colSpan={6} className='text-muted-foreground py-10 text-center'>{t('No invite rewards yet.')}</TableCell></TableRow>
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card size='sm'>
            <CardContent className='grid gap-3 text-sm sm:grid-cols-2'>
              <div className='flex gap-2'><Info className='text-muted-foreground mt-0.5 size-4 shrink-0' /><span>{t('Only successful billable model calls count. Free, failed, zero-quota, refunded, and violation charges do not count.')}</span></div>
              <div className='flex gap-2'><Clock3 className='text-muted-foreground mt-0.5 size-4 shrink-0' /><span>{t('After 10 calls, you can claim immediately. Otherwise the reward is granted automatically at local midnight.')}</span></div>
              <div className='text-muted-foreground sm:col-span-2'>{t('After the invitee completes 10 calls, the reward can be claimed after it is granted or will be automatically granted at 24:00 local time.')}</div>
            </CardContent>
          </Card>

          <div className='flex items-center justify-end gap-2'>
            <Button variant='outline' size='sm' onClick={() => setPage((value) => Math.max(1, value - 1))} disabled={page <= 1 || rewardsQuery.isFetching} aria-label={t('Previous page')}><ChevronLeft />{t('Previous')}</Button>
            <span className='text-muted-foreground text-sm tabular-nums'>{page} / {totalPages}</span>
            <Button variant='outline' size='sm' onClick={() => setPage((value) => Math.min(totalPages, value + 1))} disabled={page >= totalPages || rewardsQuery.isFetching} aria-label={t('Next page')}>{t('Next')}<ChevronRight /></Button>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
