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
import { useTranslation } from 'react-i18next'

import logoUrl from '@/assets/logo.png'

export function AboutHero() {
  const { t } = useTranslation()

  return (
    <section className='relative overflow-hidden px-4 py-10 sm:py-14'>
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
          {t('About Xingya')}
        </h1>
        <p className='text-muted-foreground mx-auto mt-4 max-w-xl text-sm leading-7 sm:text-base'>
          {t(
            'One Key, every model. This is the official introduction to Xingya plus a basic usage tutorial.'
          )}
        </p>
      </div>
    </section>
  )
}
