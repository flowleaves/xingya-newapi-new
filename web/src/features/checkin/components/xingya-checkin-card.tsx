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
import { Link } from '@tanstack/react-router'
import { Check, KeyRound, Sparkles, Sprout } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Turnstile } from '@/components/turnstile'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { useStatus } from '@/hooks/use-status'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { useCheckin } from '../hooks/use-checkin'
import type { CheckinKeyResult } from '../types'
import { CheckinHistory } from './checkin-history'
import { CheckinKeyDialog } from './checkin-key-dialog'

export function XingyaCheckinCard(props: {
  compact?: boolean
  onClaimed?: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const { status } = useStatus()
  const [month, setMonth] = useState<string>()
  const [challenge, setChallenge] = useState(false)
  const [challengeKey, setChallengeKey] = useState(0)
  const [keyResult, setKeyResult] = useState<CheckinKeyResult>()
  const checkin = useCheckin(month)
  async function claim(token?: string) {
    try {
      await checkin.claim.mutateAsync(token)
      setChallenge(false)
      props.onClaimed?.()
    } catch (error) {
      setChallengeKey((key) => key + 1)
      handleServerError(error, t('Check-in failed'))
    }
  }
  async function createKey() {
    try {
      setKeyResult(await checkin.key.mutateAsync())
    } catch (error) {
      handleServerError(error)
    }
  }
  if (checkin.query.isPending) {
    return <Skeleton className='h-64 w-full rounded-xl' />
  }
  if (checkin.query.isError || !checkin.query.data) {
    return (
      <ErrorState
        title={t('Failed to fetch checkin status')}
        onRetry={() => {
          void checkin.query.refetch()
        }}
      />
    )
  }
  const data = checkin.query.data
  const claimed = data.today_record.checked_in
  const canClaim =
    data.claim_enabled && data.group_accessible && !claimed && !checkin.claiming
  let buttonLabel = t('Check in now')
  if (claimed) buttonLabel = t('Checked in · see you tomorrow')
  else if (!data.claim_enabled) buttonLabel = t('Check-in claims are paused')
  else if (!data.group_accessible) buttonLabel = t('Check-in group unavailable')
  else if (checkin.claiming) buttonLabel = t('Checking in...')
  return (
    <div className='min-w-0 space-y-4'>
      <Card
        data-card-hover='false'
        className='border-primary/20 from-primary/10 via-card to-info/5 overflow-hidden bg-gradient-to-br'
      >
        <CardContent className='space-y-5 p-4 sm:p-6'>
          <div className='flex items-start justify-between gap-3'>
            <div>
              <p className='text-primary flex items-center gap-2 text-sm font-semibold'>
                <Sparkles className='size-4' aria-hidden='true' /> XINGYA ·{' '}
                {t('Daily Check-in')}
              </p>
              <p className='text-muted-foreground mt-1 text-sm'>
                {t('A little sprout every day, a little more possibility.')}
              </p>
            </div>
            <Sprout
              className='text-primary/60 size-7 shrink-0'
              aria-hidden='true'
            />
          </div>
          <div aria-live='polite'>
            <p className='text-muted-foreground text-xs'>
              {claimed ? t('Today received') : t('Today’s reward range')}
            </p>
            <p className='mt-1 text-3xl font-semibold tracking-tight break-words tabular-nums sm:text-4xl'>
              {claimed
                ? formatQuotaWithCurrency(data.today_record.quota_awarded)
                : `🌱 ${formatNumber(data.min_reward, locale)}–${formatNumber(data.max_reward, locale)}`}
            </p>
            {claimed && data.today_record.source === 'legacy' && (
              <p className='text-muted-foreground mt-2 text-xs'>
                {t('Legacy check-in · ordinary balance')}
              </p>
            )}
          </div>
          <Button
            className='h-auto min-h-11 w-full gap-2 py-3 whitespace-normal'
            disabled={!canClaim}
            onClick={() => {
              if (status?.turnstile_check && status.turnstile_site_key) {
                setChallenge(true)
              } else {
                void claim()
              }
            }}
          >
            {claimed ? (
              <Check aria-hidden='true' />
            ) : (
              <Sparkles aria-hidden='true' />
            )}
            {buttonLabel}
          </Button>
          <div className='border-border/70 space-y-3 border-t pt-4'>
            <div className='flex flex-wrap items-start justify-between gap-3'>
              <div>
                <p className='text-muted-foreground text-xs'>
                  {t('Available check-in credit')}
                </p>
                <p className='mt-1 text-2xl font-semibold break-all tabular-nums'>
                  {formatQuotaWithCurrency(data.wallet.available)}
                </p>
              </div>
              <Badge
                variant={data.spend_enabled ? 'secondary' : 'warning'}
                className='max-w-full break-all whitespace-normal'
              >
                {data.group || t('Not configured')}
              </Badge>
            </div>
            {data.wallet.reserved > 0 && (
              <p className='text-muted-foreground text-xs'>
                {t('Reserved for active requests')}:{' '}
                {formatQuotaWithCurrency(data.wallet.reserved)}
              </p>
            )}
            {data.pending_review > 0 && (
              <p className='text-warning text-sm'>
                {t(
                  'A check-in request needs review. Contact your administrator or switch to a regular key.'
                )}
              </p>
            )}
            {!data.spend_enabled && (
              <p className='text-warning text-sm'>
                {t(
                  'Check-in spending is paused. Your credit and history are retained.'
                )}
              </p>
            )}
            <p className='text-muted-foreground text-xs'>
              {t(
                'Separate credit · fixed group · no self-refund or regular-balance fallback'
              )}
            </p>
            <div className='grid gap-2 sm:grid-cols-2'>
              <Button
                variant='outline'
                className='h-auto min-h-11 py-3 whitespace-normal'
                disabled={
                  !data.spend_enabled ||
                  !data.group_accessible ||
                  checkin.creatingKey
                }
                onClick={createKey}
              >
                <KeyRound aria-hidden='true' />
                {checkin.creatingKey
                  ? t('Creating...')
                  : t('Create check-in key')}
              </Button>
              <Button
                variant='ghost'
                className='h-auto min-h-11 py-3 whitespace-normal'
                disabled={!data.group || !data.group_accessible}
                render={<Link to='/pricing' search={{ group: data.group }} />}
              >
                {t('View available models')}
              </Button>
            </div>
          </div>
          {props.compact && (
            <Button
              variant='link'
              className='min-h-11 w-full'
              render={<Link to='/checkin' />}
            >
              {t('Check-in history')}
            </Button>
          )}
        </CardContent>
      </Card>
      {!props.compact && (
        <>
          <div className='grid grid-cols-2 gap-3'>
            <Card>
              <CardContent className='p-4'>
                <p className='text-muted-foreground text-xs'>
                  {t('New credit this month')}
                </p>
                <p className='mt-1 text-lg font-semibold break-all tabular-nums'>
                  {formatQuotaWithCurrency(data.month_quota)}
                </p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className='p-4'>
                <p className='text-muted-foreground text-xs'>
                  {t('Check-in credit consumed')}
                </p>
                <p className='mt-1 text-lg font-semibold break-all tabular-nums'>
                  {formatQuotaWithCurrency(data.wallet.consumed)}
                </p>
              </CardContent>
            </Card>
          </div>
          <CheckinHistory
            month={data.month}
            today={data.today}
            records={data.records}
            onMonthChange={setMonth}
          />
          <p className='text-muted-foreground px-1 text-xs leading-relaxed'>
            {t(
              'Once per day, Beijing time. Use a check-in key with an output limit. Unsupported tools and media require a regular key. Legacy rewards remain in your ordinary balance. Check-in spending does not qualify for invite rewards.'
            )}
          </p>
        </>
      )}
      <Dialog
        open={challenge}
        onOpenChange={setChallenge}
        title={t('Security verification')}
        description={t('Complete verification to check in.')}
        contentClassName='sm:max-w-md'
      >
        <div className='flex min-h-24 justify-center'>
          <Turnstile
            key={challengeKey}
            siteKey={String(status?.turnstile_site_key ?? '')}
            onVerify={(token) => {
              void claim(token)
            }}
          />
        </div>
      </Dialog>
      <CheckinKeyDialog
        key={keyResult?.token.id}
        result={keyResult}
        onClose={() => setKeyResult(undefined)}
      />
    </div>
  )
}
