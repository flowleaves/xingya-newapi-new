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

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { CheckinStatusResponse, CheckinTier } from '../types'

/**
 * Internal-quota units per unit of platform currency (1 🌱 = 5000 internal).
 * Mirrors model.checkinQuotaPerTier(); the server reports raw internal units in
 * `yesterday.quota` and `total_used_quota`, so the display has to convert.
 */
const QUOTA_PER_TIER = 5000

/** Highest number of rule-C tiers to render before stopping. */
const MAX_C_TIER_LEVELS = 10

function renderRange(min: number, max: number): string {
  return `🌱${Number(min).toFixed(2)} ~ 🌱${Number(max).toFixed(2)}`
}

function TierRow(props: {
  tier: CheckinTier
  reached: boolean
  flag: string
  unit: string
}) {
  const { t } = useTranslation()

  return (
    <div className='flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5'>
      <div className='text-sm'>
        {props.flag}
        {props.tier.threshold}
        {props.unit}
      </div>
      <div className='flex items-center gap-2'>
        <span className='text-muted-foreground text-xs tabular-nums'>
          {renderRange(props.tier.min_reward, props.tier.max_reward)}
        </span>
        {props.reached ? (
          <span className='rounded-md bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400'>
            {t('Reached')}
          </span>
        ) : (
          <span className='text-muted-foreground bg-muted rounded-md px-2 py-0.5 text-[11px] font-medium'>
            {t('Not reached')}
          </span>
        )}
      </div>
    </div>
  )
}

function RuleCard(props: {
  title: string
  value: string
  ruleText: string
  tiers: CheckinTier[]
  reachedFn: (tier: CheckinTier) => boolean
  flag: string
  unit: string
}) {
  const { t } = useTranslation()

  return (
    <div className='bg-card rounded-xl border p-4'>
      <div className='mb-1.5 text-sm font-medium'>{props.title}</div>
      <div className='text-muted-foreground mb-3 text-xs tabular-nums'>
        {props.value}
      </div>
      <div className='text-muted-foreground mb-2 text-xs'>
        {props.ruleText}
      </div>
      <div className='space-y-1.5'>
        {props.tiers.map((tier) => (
          <TierRow
            key={tier.threshold}
            tier={tier}
            reached={props.reachedFn(tier)}
            flag={props.flag}
            unit={props.unit}
          />
        ))}
        {props.tiers.length === 0 && (
          <div className='text-muted-foreground text-xs'>
            {t('No tiers configured')}
          </div>
        )}
      </div>
    </div>
  )
}

function CTierBadge(props: { current: boolean; reached: boolean }) {
  const { t } = useTranslation()

  if (props.current) {
    return (
      <span className='rounded-md bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400'>
        {t('Reached')}
      </span>
    )
  }
  return (
    <span className='text-muted-foreground bg-muted rounded-md px-2 py-0.5 text-[11px] font-medium'>
      {props.reached ? t('Passed') : t('Not reached')}
    </span>
  )
}

/**
 * Shows the usage-based tiered rules behind a check-in: what the user did
 * yesterday (rule A count / rule B spend), which tiers that reaches, and where
 * they sit on rule C (historical spend).
 *
 * This is display only. The award itself is computed server-side; the numbers
 * here are the same server-provided figures the rule cards explain, so nothing
 * is recalculated that could drift from the payout.
 */
export function CheckinRules(props: {
  checkinData: CheckinStatusResponse | undefined
  checkedToday: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const count = props.checkinData?.yesterday?.count ?? 0
  const quota = props.checkinData?.yesterday?.quota ?? 0
  const quotaTier = Math.floor(quota / QUOTA_PER_TIER)
  const countTiers = props.checkinData?.count_tiers ?? []
  const quotaTiers = props.checkinData?.quota_tiers ?? []
  const fallback = props.checkinData?.fallback_reward ?? 0

  const anyCountReached = countTiers.some((tier) => count >= tier.threshold)
  const anyQuotaReached = quotaTiers.some((tier) => quotaTier >= tier.threshold)

  const usedQuotaTier = Math.floor(
    (props.checkinData?.total_used_quota ?? 0) / QUOTA_PER_TIER
  )
  const cEnabled = props.checkinData?.c_enabled ?? false
  const cBaseThreshold = props.checkinData?.c_base_threshold ?? 1000
  const cBaseReward = props.checkinData?.c_base_reward ?? 10
  const cStepQuota = props.checkinData?.c_step_quota ?? 1000
  const cStepReward = props.checkinData?.c_step_reward ?? 5
  const cMaxReward = props.checkinData?.c_max_reward ?? 30

  const cHit = cEnabled && usedQuotaTier >= cBaseThreshold
  const cTierIndex = cHit
    ? Math.floor((usedQuotaTier - cBaseThreshold) / Math.max(cStepQuota, 1))
    : -1
  const cReward = cHit
    ? Math.min(cBaseReward + cTierIndex * cStepReward, cMaxReward)
    : 0

  const cTierLevels: { threshold: number; reward: number }[] = []
  if (cEnabled && cStepQuota > 0 && cStepReward > 0) {
    let reward = cBaseReward
    let threshold = cBaseThreshold
    while (reward <= cMaxReward && cTierLevels.length < MAX_C_TIER_LEVELS) {
      cTierLevels.push({ threshold, reward })
      reward += cStepReward
      threshold += cStepQuota
    }
  }

  return (
    <div className='grid gap-3 border-b p-4 sm:grid-cols-2 sm:p-6'>
      <RuleCard
        title={t('By yesterday call count')}
        value={`${t('Yesterday')}: ${count}`}
        ruleText={t('Reward rises with yesterday call count')}
        tiers={countTiers}
        reachedFn={(tier) => count >= tier.threshold}
        flag=''
        unit=' '
      />
      <RuleCard
        title={t('By yesterday consumed quota')}
        value={`${t('Yesterday')}: 🌱${formatNumber(quotaTier, locale)}`}
        ruleText={t('Reward rises with yesterday consumed quota')}
        tiers={quotaTiers}
        reachedFn={(tier) => quotaTier >= tier.threshold}
        flag=''
        unit=' 🌱'
      />

      {cEnabled && (
        <div className='bg-card rounded-xl border p-4 sm:col-span-2'>
          <div className='mb-1.5 flex items-center justify-between gap-2 text-sm font-medium'>
            <span>{t('By historical total consumed quota')}</span>
            {cHit && (
              <span className='rounded-md bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-600 dark:text-emerald-400'>
                {t('Current reward')}: 🌱{cReward}
              </span>
            )}
          </div>
          <div className='text-muted-foreground mb-1 text-xs tabular-nums'>
            {t('Historical total consumed')}: 🌱
            {formatNumber(usedQuotaTier, locale)}
          </div>
          <div className='text-muted-foreground mb-2 text-xs'>
            {t(
              'Reward rises with historical consumption (includes subscription usage), capped at'
            )}{' '}
            🌱{cMaxReward}
          </div>
          <div className='space-y-1.5'>
            {cTierLevels.map((level, index) => {
              const reached = cHit && index <= cTierIndex
              const current = cHit && index === cTierIndex
              return (
                <div
                  key={level.threshold}
                  className={`flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5 ${
                    current ? 'border-emerald-500/50 bg-emerald-500/5' : ''
                  }`}
                >
                  <div className='text-sm'>
                    ≥ {level.threshold} {t('Bud')}
                  </div>
                  <div className='flex items-center gap-2'>
                    <span className='text-muted-foreground text-xs tabular-nums'>
                      🌱{level.reward}
                    </span>
                    <CTierBadge current={current} reached={reached} />
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      <div className='text-muted-foreground bg-muted/30 rounded-lg p-3 text-xs sm:col-span-2'>
        {props.checkedToday
          ? t('Checked in today')
          : t('Only the higher of the three rules applies (no stacking)')}
        {!anyCountReached &&
          !anyQuotaReached &&
          !cHit &&
          !props.checkedToday &&
          fallback > 0 && (
            <span className='text-muted-foreground'>
              {' · '}
              {t('Not reached, reward')} {`🌱${fallback}`}
            </span>
          )}
      </div>
    </div>
  )
}
