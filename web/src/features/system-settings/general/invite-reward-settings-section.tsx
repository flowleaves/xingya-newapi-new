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
import { useMemo, useState } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

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
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// The spend gate is entered in USD because that is the unit the backend evaluates.
// Negative values are rejected here and ignored by the server, so an invalid entry
// cannot make every reward unreachable.
const schema = z.object({
  required_consume_usd: z.coerce.number().min(0).max(1000000),
})

type Values = z.infer<typeof schema>

interface InviteRewardSettingsSectionProps {
  defaultValues: {
    required_consume_usd: number
  }
}

export function InviteRewardSettingsSection(
  props: InviteRewardSettingsSectionProps
) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const configuredQuotaPerUnit = useSystemConfigStore(
    (s) => s.config.currency.quotaPerUnit
  )

  const resolver = useMemo(() => zodResolver(schema), []) as Resolver<Values>

  const form = useForm<Values>({
    resolver,
    defaultValues: props.defaultValues,
  })

  const [isSaving, setIsSaving] = useState(false)

  // Render the configured USD amount in platform currency (芽点) so an operator sees the
  // same figure users do. 芽点 per USD is derived from the configured rate rather than
  // assumed, and the fallback keeps the hint stable before that config has loaded.
  const quotaPerUnit =
    Number.isFinite(configuredQuotaPerUnit) && configuredQuotaPerUnit > 0
      ? configuredQuotaPerUnit
      : DEFAULT_CURRENCY_CONFIG.quotaPerUnit
  const tiersPerUsd = quotaPerUnit / (quotaPerUnit / 100)
  const usdValue = Number(form.watch('required_consume_usd'))
  const tierHint =
    Number.isFinite(usdValue) && usdValue > 0
      ? `🌱${Number((usdValue * tiersPerUsd).toFixed(2))}`
      : null
  async function onSave() {
    const values = form.getValues()
    setIsSaving(true)
    try {
      const result = await updateOption.mutateAsync({
        key: 'invite_reward_setting.required_consume_usd',
        value: Number(values.required_consume_usd),
      })
      if (!result.success) {
        toast.error(t('Update failed'))
        return
      }
      form.reset(values)
      toast.success(t('Invite reward settings updated'))
    } finally {
      setIsSaving(false)
    }
  }

  function onReset() {
    form.reset()
  }

  return (
    <SettingsSection title={t('Invite Reward Settings')}>
      <Form {...form}>
        <div className='space-y-6'>
          <SettingsForm>
            <FormField
              control={form.control}
              name='required_consume_usd'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Required cumulative spend (USD)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='0.01'
                      min='0'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'An invited account must complete 10 successful billable calls and spend this amount before the inviter is rewarded. Enter 0 to require only the call count.'
                    )}
                    {tierHint
                      ? ` ${t('Equivalent to {{tier}} in platform currency.', { tier: tierHint })}`
                      : ''}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsForm>

          <SettingsPageFormActions
            onSave={form.handleSubmit(onSave)}
            onReset={onReset}
            isSaving={isSaving}
            isSaveDisabled={!form.formState.isDirty}
          />
        </div>
      </Form>
    </SettingsSection>
  )
}
