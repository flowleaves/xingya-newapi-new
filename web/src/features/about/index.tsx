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
import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Footer } from '@/components/layout/components/footer'
import { Button } from '@/components/ui/button'

import { AboutHero } from './components/about-hero'
import {
  BrandSection,
  FaqSection,
  TutorialSection,
} from './components/about-sections'

export function About() {
  const { t } = useTranslation()

  return (
    <PublicLayout showMainContainer={false}>
      <div className='w-full space-y-6 sm:space-y-8'>
        <AboutHero />
        <BrandSection />
        <TutorialSection />
        <FaqSection />

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
                render={<Link to='/sign-in' />}
              >
                {t('Sign In')}
                <ArrowRight className='size-4' aria-hidden='true' />
              </Button>
            </div>
          </div>
        </section>
      </div>
      <Footer />
    </PublicLayout>
  )
}
