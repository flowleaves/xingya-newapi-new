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

const schema = z.object({
  enabled: z.boolean(),
  ratio_percent: z.coerce.number().min(0).max(100),
  window_days: z.coerce.number().int().min(1).max(3650),
  max_rebate_per_invitee: z.coerce.number().min(0).max(1_000_000),
  include_topup: z.boolean(),
  include_redemption: z.boolean(),
})

type Values = z.infer<typeof schema>

interface InviteRebateSettingsSectionProps {
  defaultValues: Values
}

export function InviteRebateSettingsSection(
  props: InviteRebateSettingsSectionProps
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
        { key: 'invite_rebate_setting.enabled', value: values.enabled },
        {
          key: 'invite_rebate_setting.ratio_percent',
          value: values.ratio_percent,
        },
        {
          key: 'invite_rebate_setting.window_days',
          value: values.window_days,
        },
        {
          key: 'invite_rebate_setting.max_rebate_per_invitee',
          value: values.max_rebate_per_invitee,
        },
        {
          key: 'invite_rebate_setting.include_topup',
          value: values.include_topup,
        },
        {
          key: 'invite_rebate_setting.include_redemption',
          value: values.include_redemption,
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
      toast.success(t('Invite rebate settings updated'))
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <SettingsSection title={t('Invite Rebate Settings')}>
      <Form {...form}>
        <div className='space-y-6'>
          <SettingsForm>
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Invite rebate enabled')}</FormLabel>
                    <FormDescription>
                      {t('Pay rebates for invited recharges')}
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

            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='ratio_percent'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Rebate ratio (%)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='100'
                        step='0.1'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Percentage of the credited recharge amount')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='window_days'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Window (days)')}</FormLabel>
                    <FormControl>
                      <Input type='number' min='1' max='3650' {...field} />
                    </FormControl>
                    <FormDescription>
                      {t('Days counted from the invitee registration')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='max_rebate_per_invitee'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Max rebate per invitee')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min='0'
                        max='1000000'
                        step='1'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Unit: sprouts; 100 sprouts = 1 unit')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name='include_topup'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Include wallet top-ups')}</FormLabel>
                    <FormDescription>
                      {t('Count paid wallet recharges')}
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
              name='include_redemption'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between rounded-lg border p-3'>
                  <div className='space-y-0.5'>
                    <FormLabel>{t('Include redemption codes')}</FormLabel>
                    <FormDescription>
                      {t('Count quota redeemed with a code')}
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
          </SettingsForm>

          <SettingsPageFormActions
            onSave={form.handleSubmit(onSave)}
            onReset={() => form.reset()}
            isSaving={isSaving}
            isSaveDisabled={!form.formState.isDirty}
          />
        </div>
      </Form>
    </SettingsSection>
  )
}
