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
const SHOP_URL = 'https://www.16688.com.cn/shop/XINGYA'

interface XingyaHomeProps {
  isAuthenticated?: boolean
}

const cardClass =
  'rounded-2xl border border-border/60 bg-card p-4 shadow-sm sm:p-6'

function DisclaimerCard() {
  const { t } = useTranslation()

  return (
    <section className='w-full min-w-0'>
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
    <section className='w-full min-w-0'>
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
    <section className='w-full min-w-0'>
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
    <section className='w-full min-w-0'>
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

function GatewayVisual() {
  const { t } = useTranslation()

  return (
    <div
      role='img'
      aria-label={t('Xingya · Unified AI API Gateway')}
      className='border-primary/15 bg-card relative mx-auto h-72 w-full max-w-lg overflow-hidden rounded-3xl border shadow-[0_24px_60px_rgb(50_90_155/0.1)] sm:h-96'
    >
      <div
        aria-hidden='true'
        className='absolute inset-0 opacity-40'
        style={{
          backgroundImage:
            'linear-gradient(color-mix(in srgb, var(--primary) 9%, transparent) 1px, transparent 1px), linear-gradient(90deg, color-mix(in srgb, var(--primary) 9%, transparent) 1px, transparent 1px)',
          backgroundSize: '32px 32px',
        }}
      />
      <div className='text-muted-foreground relative z-10 flex items-center justify-between p-5 text-xs'>
        <span className='flex items-center gap-2'>
          <span className='bg-success size-2 rounded-full shadow-[0_0_10px_var(--success)]' />
          {t('Xingya · Unified AI API Gateway')}
        </span>
        <span className='text-success text-[10px] font-bold tracking-widest'>
          LIVE
        </span>
      </div>
      <div
        aria-hidden='true'
        className='border-primary/20 absolute top-1/2 left-1/2 size-44 -translate-x-1/2 -translate-y-1/2 rounded-full border border-dashed sm:size-56'
      />
      <div className='bg-card border-primary/15 absolute top-1/2 left-1/2 z-10 flex size-36 -translate-x-1/2 -translate-y-1/2 flex-col items-center justify-center rounded-full border shadow-[0_16px_45px_rgb(53_109_243/0.14)] sm:size-44'>
        <img
          src={logoUrl}
          alt=''
          className='size-16 rounded-2xl object-contain sm:size-20'
        />
        <span className='mt-1 text-xs font-bold sm:text-sm'>{t('Xingya')}</span>
        <span className='text-muted-foreground mt-0.5 text-[9px]'>API</span>
      </div>
      <div className='bg-card absolute top-20 left-3 z-20 rounded-xl border p-2.5 shadow-md sm:top-24 sm:left-6'>
        <b className='text-primary text-xs'>GPT</b>
        <span className='text-muted-foreground ml-2 text-[10px]'>OpenAI</span>
      </div>
      <div className='bg-card absolute top-36 right-3 z-20 rounded-xl border p-2.5 shadow-md sm:top-40 sm:right-6'>
        <b className='text-xs text-orange-600'>CL</b>
        <span className='text-muted-foreground ml-2 text-[10px]'>Claude</span>
      </div>
      <div className='bg-card absolute bottom-16 left-4 z-20 rounded-xl border p-2.5 shadow-md sm:bottom-20 sm:left-8'>
        <b className='text-xs text-cyan-600'>GM</b>
        <span className='text-muted-foreground ml-2 text-[10px]'>Gemini</span>
      </div>
      <div className='bg-card absolute right-4 bottom-4 left-4 z-20 flex items-center gap-2 rounded-lg border px-3 py-2.5 font-mono text-[10px] shadow-sm'>
        <span className='text-success'>$</span>
        <code className='min-w-0 flex-1 truncate'>xingya.site/v1/chat</code>
        <span className='text-success font-sans font-bold'>200 OK</span>
      </div>
    </div>
  )
}

export function XingyaHome(props: XingyaHomeProps) {
  const { t } = useTranslation()
  const consoleLabel = props.isAuthenticated
    ? t('Go to Dashboard')
    : t('Sign In')
  const consoleTo = props.isAuthenticated ? '/dashboard' : '/sign-in'

  return (
    <div className='w-full space-y-8 pb-4 sm:space-y-10'>
      <section className='relative overflow-hidden px-4 pt-28 pb-8 sm:pt-36 sm:pb-12'>
        <div
          aria-hidden='true'
          className='absolute inset-0 -z-10 opacity-70 dark:opacity-20'
          style={{
            background:
              'radial-gradient(ellipse 55% 55% at 15% 15%, color-mix(in srgb, var(--primary) 15%, transparent), transparent 72%)',
          }}
        />
        <div className='mx-auto grid max-w-7xl items-center gap-10 lg:grid-cols-[1.03fr_.97fr] lg:gap-14'>
          <div className='min-w-0 text-center lg:text-left'>
            <span className='border-primary/15 bg-card text-primary inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-[10px] font-bold tracking-wider shadow-sm'>
              <span className='bg-success size-1.5 rounded-full' />
              {t('Xingya · Unified AI API Gateway')}
            </span>
            <h1 className='mt-5 text-4xl leading-[1.14] font-extrabold tracking-tight text-balance sm:text-5xl lg:text-6xl'>
              {t('One Key, every model')}
            </h1>
            <p className='text-muted-foreground mx-auto mt-5 max-w-xl text-sm leading-7 sm:text-base lg:mx-0'>
              {t(
                'One endpoint, OpenAI-compatible, three lines of code to get started.'
              )}
            </p>
            <div className='mt-7 flex w-full flex-col justify-center gap-3 sm:flex-row lg:justify-start'>
              <Button
                size='lg'
                className='h-11 rounded-xl px-5'
                render={<Link to='/sign-up' />}
              >
                {t('Get Started')}
                <ArrowRight className='size-4' aria-hidden='true' />
              </Button>
              <Button
                size='lg'
                variant='outline'
                className='h-11 rounded-xl px-5'
                render={<Link to={consoleTo} />}
              >
                {consoleLabel}
              </Button>
            </div>
            <div className='bg-card mx-auto mt-6 flex max-w-md min-w-0 items-center gap-3 rounded-xl border px-3.5 py-3 text-left shadow-sm lg:mx-0'>
              <span className='text-muted-foreground hidden text-[9px] font-bold tracking-widest sm:inline'>
                API
              </span>
              <code className='min-w-0 flex-1 truncate text-xs'>{API_BASE}</code>
              <Server className='text-primary size-4' aria-hidden='true' />
            </div>
          </div>
          <GatewayVisual />
        </div>
        <div className='mx-auto mt-10 grid max-w-7xl grid-cols-2 border-y lg:grid-cols-4'>
          {[
            ['40+', t('Available Models')],
            ['OpenAI', t('API usage guide')],
            ['1 = 100', t('Recharge methods')],
            ['7 × 24h', t('Usage and billing guide')],
          ].map(([value, label]) => (
            <div
              key={label}
              className='flex min-w-0 flex-col items-center gap-1 border-r px-2 py-5 text-center last:border-r-0 even:max-lg:border-r-0 max-lg:[&:nth-child(-n+2)]:border-b'
            >
              <strong className='truncate text-lg tracking-tight sm:text-xl'>
                {value}
              </strong>
              <span className='text-muted-foreground truncate text-[11px]'>
                {label}
              </span>
            </div>
          ))}
        </div>
      </section>

      <div className='mx-auto grid w-full max-w-7xl gap-5 px-4 md:grid-cols-2'>
        <DisclaimerCard />
        <GroupCard />
        <RechargeCard />
        <ApiGuideCard />
      </div>

      <section className='px-4 pb-12'>
        <div className='border-primary/15 bg-card mx-auto max-w-7xl rounded-3xl border bg-[linear-gradient(135deg,var(--secondary),var(--card))] p-6 text-center shadow-[0_16px_45px_rgb(50_90_155/0.07)] sm:p-10'>
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
