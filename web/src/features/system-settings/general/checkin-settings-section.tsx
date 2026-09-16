/*
 * Copyright (C) 2023-2026 QuantumNous
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <https://www.gnu.org/licenses/>.
 *
 * For commercial licensing, please contact support@quantumnous.com
 */
import { zodResolver } from '@hookform/resolvers/zod'
import { Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm, type Resolver, type Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

/** Server-side hard ceiling for a single award, in platform currency (🌱). */
const MAX_REWARD_TIER = 100000

export interface CheckinTierValue {
  threshold: number
  min_reward: number
  max_reward: number
}

/**
 * A tier row in the editor. `rowKey` is a client-only React identity: tiers are
 * keyed by their values otherwise, and a value-based key would remount the input
 * on every keystroke (losing focus). It is stripped before the config is saved.
 */
interface TierRow extends CheckinTierValue {
  rowKey: string
}

let tierRowSeq = 0

function toTierRows(tiers: CheckinTierValue[]): TierRow[] {
  return tiers.map((tier) => ({
    ...tier,
    rowKey: `tier-${(tierRowSeq += 1)}`,
  }))
}

function toTierValues(rows: TierRow[]): CheckinTierValue[] {
  return rows.map((row) => ({
    threshold: row.threshold,
    min_reward: row.min_reward,
    max_reward: row.max_reward,
  }))
}

export interface CheckinSettingsSectionProps {
  defaultValues: {
    enabled: boolean
    count_enabled: boolean
    count_tiers: CheckinTierValue[]
    quota_enabled: boolean
    quota_tiers: CheckinTierValue[]
    include_subscription: boolean
    c_enabled: boolean
    c_base_threshold: number
    c_base_reward: number
    c_step_quota: number
    c_step_reward: number
    c_max_reward: number
    fallback_reward: number
  }
}

// Bounds mirror the server's Sanitized()/MaxCheckinRewardTier so the form cannot
// submit something the backend would only have to clamp anyway.
const reward = z.coerce.number().min(0).max(MAX_REWARD_TIER)

const schema = z.object({
  enabled: z.boolean(),
  count_enabled: z.boolean(),
  quota_enabled: z.boolean(),
  include_subscription: z.boolean(),
  c_enabled: z.boolean(),
  c_base_threshold: z.coerce.number().min(0),
  c_base_reward: reward,
  c_step_quota: z.coerce.number().min(0),
  c_step_reward: reward,
  c_max_reward: reward,
  fallback_reward: reward,
})

type Values = z.infer<typeof schema>

export function CheckinSettingsSection(props: CheckinSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const resolver = useMemo(() => zodResolver(schema), []) as Resolver<Values>
  const form = useForm<Values>({
    resolver,
    defaultValues: {
      enabled: props.defaultValues.enabled,
      count_enabled: props.defaultValues.count_enabled,
      quota_enabled: props.defaultValues.quota_enabled,
      include_subscription: props.defaultValues.include_subscription,
      c_enabled: props.defaultValues.c_enabled,
      c_base_threshold: props.defaultValues.c_base_threshold,
      c_base_reward: props.defaultValues.c_base_reward,
      c_step_quota: props.defaultValues.c_step_quota,
      c_step_reward: props.defaultValues.c_step_reward,
      c_max_reward: props.defaultValues.c_max_reward,
      fallback_reward: props.defaultValues.fallback_reward,
    },
  })

  // Tier rows stay out of react-hook-form: they are variable-length rows and
  // plain state keeps the add/remove/edit path obvious.
  const [countTiers, setCountTiers] = useState<TierRow[]>(() =>
    toTierRows(props.defaultValues.count_tiers)
  )
  const [quotaTiers, setQuotaTiers] = useState<TierRow[]>(() =>
    toTierRows(props.defaultValues.quota_tiers)
  )
  const [isSaving, setIsSaving] = useState(false)

  async function onSave() {
    const valid = await form.trigger()
    if (!valid) return

    const values = form.getValues()
    setIsSaving(true)
    try {
      const updates = [
        { key: 'checkin_setting.enabled', value: values.enabled },
        { key: 'checkin_setting.count_enabled', value: values.count_enabled },
        {
          key: 'checkin_setting.count_tiers',
          value: JSON.stringify(toTierValues(countTiers)),
        },
        { key: 'checkin_setting.quota_enabled', value: values.quota_enabled },
        {
          key: 'checkin_setting.quota_tiers',
          value: JSON.stringify(toTierValues(quotaTiers)),
        },
        {
          key: 'checkin_setting.include_subscription',
          value: values.include_subscription,
        },
        { key: 'checkin_setting.c_enabled', value: values.c_enabled },
        {
          key: 'checkin_setting.c_base_threshold',
          value: values.c_base_threshold,
        },
        { key: 'checkin_setting.c_base_reward', value: values.c_base_reward },
        { key: 'checkin_setting.c_step_quota', value: values.c_step_quota },
        { key: 'checkin_setting.c_step_reward', value: values.c_step_reward },
        { key: 'checkin_setting.c_max_reward', value: values.c_max_reward },
        {
          key: 'checkin_setting.fallback_reward',
          value: values.fallback_reward,
        },
      ]
      for (const update of updates) {
        const result = await updateOption.mutateAsync(update)
        if (!result.success) {
          toast.error(t('Update failed'))
          return
        }
      }
      toast.success(t('Check-in settings updated'))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <SettingsSection title={t('Check-in Rewards')}>
      <Form {...form}>
        <form className='space-y-6'>
          <SettingsForm>
            <ToggleRow
              label={t('Check-in enabled')}
              description={t('Allow users to claim a daily reward.')}
              checked={form.watch('enabled')}
              onChange={(checked) => form.setValue('enabled', checked, { shouldDirty: true })}
            />

            <ToggleRow
              label={t('Rule A — by yesterday request count')}
              description={t(
                'Reward users whose billable request count yesterday reached a threshold.'
              )}
              checked={form.watch('count_enabled')}
              onChange={(checked) =>
                form.setValue('count_enabled', checked, { shouldDirty: true })
              }
            />
            <TierEditor
              tiers={countTiers}
              onChange={setCountTiers}
              thresholdLabel={t('Threshold (requests)')}
            />

            <ToggleRow
              label={t('Rule B — by yesterday spend')}
              description={t(
                'Reward users whose spend yesterday reached a threshold.'
              )}
              checked={form.watch('quota_enabled')}
              onChange={(checked) =>
                form.setValue('quota_enabled', checked, { shouldDirty: true })
              }
            />
            <TierEditor
              tiers={quotaTiers}
              onChange={setQuotaTiers}
              thresholdLabel={t('Threshold (芽点)')}
            />
            <ToggleRow
              label={t('Include subscription usage')}
              description={t(
                'Count subscription-funded requests in yesterday usage.'
              )}
              checked={form.watch('include_subscription')}
              onChange={(checked) =>
                form.setValue('include_subscription', checked, {
                  shouldDirty: true,
                })
              }
            />

            <ToggleRow
              label={t('Rule C — by historical spend')}
              description={t(
                'Tiered by lifetime spend; the highest of A, B and C is paid.'
              )}
              checked={form.watch('c_enabled')}
              onChange={(checked) =>
                form.setValue('c_enabled', checked, { shouldDirty: true })
              }
            />
            <div className='grid gap-3 sm:grid-cols-2'>
              <NumberField
                control={form.control}
                name='c_base_threshold'
                label={t('Base threshold (芽点)')}
              />
              <NumberField
                control={form.control}
                name='c_base_reward'
                label={t('Base reward (芽点)')}
              />
              <NumberField
                control={form.control}
                name='c_step_quota'
                label={t('Step increment (芽点)')}
              />
              <NumberField
                control={form.control}
                name='c_step_reward'
                label={t('Reward per step (芽点)')}
              />
              <NumberField
                control={form.control}
                name='c_max_reward'
                label={t('Maximum reward (芽点)')}
              />
              <NumberField
                control={form.control}
                name='fallback_reward'
                label={t('Fallback reward (芽点)')}
              />
            </div>
          </SettingsForm>

          <SettingsPageFormActions
            onSave={onSave}
            onReset={() => {
              form.reset()
              setCountTiers(toTierRows(props.defaultValues.count_tiers))
              setQuotaTiers(toTierRows(props.defaultValues.quota_tiers))
            }}
            isSaving={isSaving}
            isSaveDisabled={!form.formState.isDirty}
          />
        </form>
      </Form>
    </SettingsSection>
  )
}

function ToggleRow(props: {
  label: string
  description: string
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <FormItem className='flex items-center justify-between rounded-lg border p-3'>
      <div className='space-y-0.5'>
        <FormLabel>{props.label}</FormLabel>
        <FormDescription>{props.description}</FormDescription>
      </div>
      <Switch checked={props.checked} onCheckedChange={props.onChange} />
    </FormItem>
  )
}

/** The numeric subset of the form, so the field value is never a boolean. */
type NumericKey =
  | 'c_base_threshold'
  | 'c_base_reward'
  | 'c_step_quota'
  | 'c_step_reward'
  | 'c_max_reward'
  | 'fallback_reward'

function NumberField(props: {
  control: Control<Values>
  name: NumericKey
  label: string
}) {
  return (
    <FormField
      control={props.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <Input type='number' step='1' min='0' {...field} />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

function TierEditor(props: {
  tiers: TierRow[]
  onChange: (tiers: TierRow[]) => void
  thresholdLabel: string
}) {
  const { t } = useTranslation()

  function update(index: number, patch: Partial<CheckinTierValue>) {
    props.onChange(
      props.tiers.map((tier, i) => (i === index ? { ...tier, ...patch } : tier))
    )
  }

  function remove(index: number) {
    props.onChange(props.tiers.filter((_, i) => i !== index))
  }

  function add() {
    props.onChange([
      ...props.tiers,
      ...toTierRows([{ threshold: 0, min_reward: 0, max_reward: 0 }]),
    ])
  }

  return (
    <div className='space-y-2 rounded-lg border p-3'>
      {props.tiers.length === 0 ? (
        <p className='text-muted-foreground text-xs'>
          {t('No tiers yet — this rule will never trigger.')}
        </p>
      ) : (
        props.tiers.map((tier, index) => (
          <div
            key={tier.rowKey}
            className='grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-2'
          >
            <Input
              type='number'
              step='1'
              min='0'
              aria-label={props.thresholdLabel}
              value={tier.threshold}
              onChange={(event) =>
                update(index, { threshold: Number(event.target.value) })
              }
            />
            <Input
              type='number'
              step='0.1'
              min='0'
              aria-label={t('Min reward (芽点)')}
              value={tier.min_reward}
              onChange={(event) =>
                update(index, { min_reward: Number(event.target.value) })
              }
            />
            <Input
              type='number'
              step='0.1'
              min='0'
              aria-label={t('Max reward (芽点)')}
              value={tier.max_reward}
              onChange={(event) =>
                update(index, { max_reward: Number(event.target.value) })
              }
            />
            <Button
              type='button'
              size='sm'
              variant='ghost'
              aria-label={t('Remove tier')}
              onClick={() => remove(index)}
            >
              <Trash2 className='size-4' aria-hidden='true' />
            </Button>
          </div>
        ))
      )}
      <Button type='button' size='sm' variant='outline' onClick={add}>
        <Plus className='size-4' aria-hidden='true' />
        {t('Add tier')}
      </Button>
    </div>
  )
}
