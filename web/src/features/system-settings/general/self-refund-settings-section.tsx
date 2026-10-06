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
import { Switch } from '@/components/ui/switch'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

// The ratio decides how much of a charge is paid back, so the form bounds it to
// the only range the server can honor (SafeRatio clamps to [0,1] as well).
const schema = z.object({
  enabled: z.boolean(),
  only_per_request: z.boolean(),
  ratio: z.coerce.number().min(0).max(1),
  window_hours: z.coerce.number().int().min(1).max(720),
  daily_max_count: z.coerce.number().int().min(0).max(100),
  daily_max_quota: z.coerce.number().int().min(0),
  min_refund_quota: z.coerce.number().int().min(0),
})

type Values = z.infer<typeof schema>

interface SelfRefundSettingsSectionProps {
  defaultValues: {
    enabled: boolean
    only_per_request: boolean
    ratio: number
    window_hours: number
    daily_max_count: number
    daily_max_quota: number
    min_refund_quota: number
  }
}

export function SelfRefundSettingsSection(
  props: SelfRefundSettingsSectionProps
) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const resolver = useMemo(() => zodResolver(schema), []) as Resolver<Values>

  const form = useForm<Values>({
    resolver,
    defaultValues: props.defaultValues,
  })

  const [isSaving, setIsSaving] = useState(false)

  async function onSave() {
    const values = form.getValues()
    setIsSaving(true)
    try {
      const updates = [
        { key: 'self_refund_setting.enabled', value: values.enabled },
        {
          key: 'self_refund_setting.only_per_request',
          value: values.only_per_request,
        },
        { key: 'self_refund_setting.ratio', value: values.ratio },
        { key: 'self_refund_setting.window_hours', value: values.window_hours },
        {
          key: 'self_refund_setting.daily_max_count',
          value: values.daily_max_count,
        },
        {
          key: 'self_refund_setting.daily_max_quota',
          value: values.daily_max_quota,
        },
        {
          key: 'self_refund_setting.min_refund_quota',
          value: values.min_refund_quota,
        },
      ]
      for (const update of updates) {
        const result = await updateOption.mutateAsync(update)
        if (!result.success) {
          toast.error(t('Update failed'))
          return
        }
      }
      form.reset(values)
      toast.success(t('Self refund settings updated'))
    } finally {
      setIsSaving(false)
    }
  }

  function onReset() {
    form.reset()
  }

  return (
    <SettingsSection title={t('Self-Refund Settings')}>
      <Form {...form}>
        <div className='space-y-6'>
          <SettingsForm>
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Self-refund enabled')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Auto-refund for empty responses and truncated streams'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='only_per_request'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between gap-3 rounded-lg border p-3'>
                  <div className='min-w-0 space-y-0.5'>
                    <FormLabel>
                      {t('Only refund per-request billing')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'When enabled, only request-priced logs are eligible for self-refund.'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='ratio'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Refund Ratio')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      step='0.1'
                      min='0'
                      max='1'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Refund ratio (0.5 = 50%)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='window_hours'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Window Hours')}</FormLabel>
                  <FormControl>
                    <Input type='number' min='1' max='720' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Time window (hours) within which a log can be refunded'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='daily_max_count'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Daily Max Refund Count')}</FormLabel>
                  <FormControl>
                    <Input type='number' min='0' max='100' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Maximum number of refunds per day')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='daily_max_quota'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Daily Max Refund Quota')}</FormLabel>
                  <FormControl>
                    <Input type='number' min='0' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Maximum total refund quota per day (0 = unlimited)')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='min_refund_quota'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Min Refund Quota')}</FormLabel>
                  <FormControl>
                    <Input type='number' min='0' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t('Minimum refund quota per request')}
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
