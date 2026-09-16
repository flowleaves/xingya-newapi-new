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
import { Link } from '@tanstack/react-router'
import {
  ArrowRight,
  CreditCard,
  Info,
  MessageCircle,
  RefreshCw,
  Server,
  Wallet,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import logoUrl from '@/assets/logo.png'
import { Button } from '@/components/ui/button'

const API_BASE = 'https://xingya.site/v1'
const QQ_GROUP = '906445439'
const QQ_JOIN_URL = 'https://qm.qq.com/q/24BrdZ7sCQ'
const SHOP_URL = 'https://catfk.com/shop/X62LLH60'

interface XingyaHomeProps {
  isAuthenticated?: boolean
}

const cardClass =
  'rounded-2xl border border-border/60 bg-card p-4 shadow-sm sm:p-6'

function DisclaimerCard() {
  const { t } = useTranslation()

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className='rounded-2xl border border-amber-200/60 bg-amber-50/50 p-4 text-[13px] leading-6 text-amber-900 sm:p-6 sm:text-sm dark:border-amber-400/20 dark:bg-amber-500/10 dark:text-amber-100'>
        <div className='mb-2 flex items-center gap-2 font-semibold'>
          <Info className='size-4' aria-hidden='true' />
          <span>{t('Disclaimer')}</span>
        </div>
        <p>
          {t(
            'This service is for learning and technical evaluation only. It is restricted to adults aged 18 and over; minors must use it with a guardian fully informed, consenting and supervising throughout. All AI-generated content is triggered directly by user requests and does not represent this site. Please comply with your local laws; this site accepts no responsibility for any user action or its consequences.'
          )}
        </p>
      </div>
    </section>
  )
}

function GroupCard() {
  const { t } = useTranslation()

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-3 flex items-center gap-2 text-lg font-bold'>
          <MessageCircle className='text-primary size-5' aria-hidden='true' />
          {t('Xingya AI Group 2 (occasional perks)')}
        </h2>
        <div className='text-foreground/85 space-y-1.5 text-sm leading-6'>
          <p>
            <span className='font-semibold'>{t('QQ group number:')}</span>
            <span className='text-primary font-mono'>{QQ_GROUP}</span>
          </p>
          <p>
            <a
              href={QQ_JOIN_URL}
              target='_blank'
              rel='noopener noreferrer'
              className='text-primary hover:underline'
            >
              {t('Click to join the Xingya AI Group 2 chat')}
            </a>
          </p>
        </div>
      </div>
    </section>
  )
}

function RechargeCard() {
  const { t } = useTranslation()

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-3 flex items-center gap-2 text-lg font-bold'>
          <RefreshCw className='text-primary size-5' aria-hidden='true' />
          {t('Recharge methods')}
        </h2>
        <div className='grid gap-3 sm:grid-cols-2'>
          <a
            href={SHOP_URL}
            target='_blank'
            rel='noopener noreferrer'
            className='border-border/60 bg-muted/30 hover:border-primary/60 hover:bg-muted/50 block rounded-xl border p-4 transition'
          >
            <div className='mb-2 flex items-center gap-2 font-semibold'>
              <CreditCard
                className='text-primary size-4'
                aria-hidden='true'
              />
              {t('Automatic recharge (instant delivery)')}
            </div>
            <p className='text-foreground/85 text-sm leading-6'>
              {t(
                'Visit the official card shop, buy a code, then redeem it in your wallet.'
              )}
            </p>
            <p className='text-muted-foreground mt-2 text-xs'>
              {t('Amount paid = amount credited.')}
            </p>
            <span className='text-primary mt-2 flex items-center gap-1 text-xs font-medium'>
              {t('Go to the card shop')}
              <ArrowRight className='size-3.5' aria-hidden='true' />
            </span>
          </a>
          <div className='border-border/60 bg-muted/30 rounded-xl border p-4'>
            <div className='mb-2 flex items-center gap-2 font-semibold'>
              <Wallet className='text-primary size-4' aria-hidden='true' />
              {t('Manual recharge (contact an admin)')}
            </div>
            <p className='text-foreground/85 text-sm leading-6'>
              {t(
                'You may also ask an admin to top up manually. Include your username or user ID in the transfer note; the credit is applied after verification.'
              )}
            </p>
            <p className='text-muted-foreground mt-2 text-xs'>
              {t('Amount paid = amount credited.')}
            </p>
          </div>
        </div>
      </div>
    </section>
  )
}

function ApiGuideCard() {
  const { t } = useTranslation()

  return (
    <section className='mx-auto w-full max-w-3xl px-4'>
      <div className={cardClass}>
        <h2 className='mb-3 flex items-center gap-2 text-lg font-bold'>
          <Server className='text-primary size-5' aria-hidden='true' />
          {t('Usage and billing guide')}
        </h2>
        <p className='text-foreground/90 mb-2 text-sm font-semibold'>
          {t('API usage guide')}
        </p>
        <ul className='text-foreground/85 space-y-1.5 text-sm leading-6'>
          <li>
            <span className='font-semibold'>{t('Endpoint:')}</span>
            <span className='text-primary font-mono break-all'>
              {API_BASE}
            </span>
          </li>
          <li>
            <span className='font-semibold'>{t('Recommended group:')}</span>
            {t('Pick the group that matches the models you need.')}
          </li>
        </ul>
      </div>
    </section>
  )
}

export function XingyaHome(props: XingyaHomeProps) {
  const { t } = useTranslation()
  const consoleLabel = props.isAuthenticated
    ? t('Go to Dashboard')
    : t('Sign In')
  const consoleTo = props.isAuthenticated ? '/dashboard' : '/sign-in'

  return (
    <div className='w-full space-y-6 sm:space-y-8'>
      <section className='relative overflow-hidden px-4 py-10 sm:py-16'>
        <div
          aria-hidden='true'
          className='absolute inset-0 -z-10 opacity-25 dark:opacity-[0.08]'
          style={{
            background: [
              'radial-gradient(ellipse 60% 60% at 20% 30%, oklch(0.62 0.14 250 / 55%) 0%, transparent 70%)',
              'radial-gradient(ellipse 50% 50% at 80% 60%, oklch(0.68 0.12 200 / 45%) 0%, transparent 70%)',
            ].join(', '),
          }}
        />
        <div className='mx-auto max-w-2xl text-center'>
          <img
            src={logoUrl}
            alt={t('Xingya')}
            className='mx-auto mb-5 h-20 w-20 rounded-xl bg-white object-contain shadow-md sm:mb-6 sm:h-24 sm:w-24'
          />
          <span className='border-border/60 bg-card/70 text-muted-foreground inline-flex items-center rounded-full border px-3 py-1 text-xs font-medium'>
            {t('Xingya · Unified AI API Gateway')}
          </span>
          <h1 className='mt-4 text-3xl leading-tight font-bold tracking-tight sm:text-5xl'>
            {t('One Key, every model')}
          </h1>
          <p className='text-muted-foreground mx-auto mt-4 max-w-xl text-sm leading-7 sm:text-base'>
            {t(
              'One endpoint, OpenAI-compatible, three lines of code to get started.'
            )}
          </p>
          <div className='mt-7 flex w-full flex-col justify-center gap-3 sm:flex-row'>
            <Button size='lg' render={<Link to='/sign-up' />}>
              {t('Get Started')}
            </Button>
            <Button
              size='lg'
              variant='outline'
              render={<Link to={consoleTo} />}
            >
              {consoleLabel}
              <ArrowRight className='size-4' aria-hidden='true' />
            </Button>
          </div>
        </div>
      </section>

      <DisclaimerCard />
      <GroupCard />
      <RechargeCard />
      <ApiGuideCard />

      <section className='px-4 pb-12'>
        <div className='border-border/60 bg-card mx-auto max-w-2xl rounded-2xl border p-6 text-center shadow-sm sm:p-8'>
          <h2 className='text-xl leading-tight font-bold sm:text-2xl'>
            {t('Start using Xingya')}
          </h2>
          <p className='text-muted-foreground mt-2 text-sm'>
            {t('Free sign-up; one Key for every model.')}
          </p>
          <div className='mt-5 flex w-full flex-col justify-center gap-3 sm:flex-row'>
            <Button size='lg' render={<Link to='/sign-up' />}>
              {t('Get Started')}
            </Button>
            <Button
              size='lg'
              variant='outline'
              render={<Link to={consoleTo} />}
            >
              {consoleLabel}
            </Button>
          </div>
          <Link
            to='/about'
            className='text-muted-foreground hover:text-primary mt-4 inline-flex items-center gap-1 text-xs transition'
          >
            {t('About Xingya · Tutorial')}
            <ArrowRight className='size-3' aria-hidden='true' />
          </Link>
        </div>
      </section>
    </div>
  )
}
