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
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
  FormDescription,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  getCheckinSettings,
  saveCheckinSettings,
  refreshCheckinEligibility,
} from '@/features/checkin/api'
import { checkinSettingsSchema } from '@/features/checkin/lib/settings-schema'
import type {
  CheckinEligibility,
  XingyaCheckinSettings,
} from '@/features/checkin/types'
import { ApiKeyGroupCombobox } from '@/features/keys/components/api-key-group-combobox'

import { SettingsForm } from '../components/settings-form-layout'
import {
  SettingsPageFormActions,
  useSuppressSettingsSectionHeader,
} from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { CheckinReviewTable } from './checkin-review-table'

export function XingyaCheckinSettingsSection() {
  const { t } = useTranslation()
  const schema = useMemo(() => checkinSettingsSchema(t), [t])
  const pageActions = useSuppressSettingsSectionHeader()
  const queryClient = useQueryClient()
  const [refreshEligibility, setRefreshEligibility] =
    useState<CheckinEligibility | null>(null)
  const query = useQuery({
    queryKey: ['xingya-checkin-settings'],
    queryFn: getCheckinSettings,
  })
  const form = useForm<XingyaCheckinSettings>({
    resolver: zodResolver(schema) as Resolver<XingyaCheckinSettings>,
    defaultValues: {
      claim_enabled: false,
      spend_enabled: false,
      min_reward: 0,
      max_reward: 0,
      consume_threshold: 5000,
      fixed_reward: 30,
      group: '',
      revision: 0,
    },
  })
  useEffect(() => {
    if (query.data && !form.formState.isDirty) form.reset(query.data.settings)
  }, [query.data, form, form.formState.isDirty])
  const save = useMutation({
    mutationFn: saveCheckinSettings,
    onSuccess: async (settings) => {
      queryClient.setQueryData(['xingya-checkin-settings'], {
        ...query.data,
        settings,
      })
      form.reset(settings)
      await queryClient.invalidateQueries({
        queryKey: ['xingya-checkin-settings'],
      })
      await queryClient.invalidateQueries({ queryKey: ['xingya-checkin'] })
      toast.success(t('Check-in settings updated'))
    },
  })
  const refresh = useMutation({
    mutationFn: refreshCheckinEligibility,
    onSuccess: async () => {
      setRefreshEligibility(null)
      await queryClient.invalidateQueries({ queryKey: ['xingya-checkin'] })
      toast.success(t('Today’s check-in eligibility refreshed'))
    },
    onSettled: () => {
      setRefreshEligibility(null)
      return queryClient.invalidateQueries({
        queryKey: ['xingya-checkin-settings'],
      })
    },
  })
  if (query.isPending) return <Skeleton className='h-72 rounded-xl' />
  if (query.isError || !query.data) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  const groups = Object.keys(query.data.groups)
    .filter((group) => group !== 'auto')
    .map((group) => ({
      value: group,
      label: group,
      ratio: query.data.groups[group],
    }))
  const onSave = form.handleSubmit((values) => save.mutate(values))
  return (
    <SettingsSection title={t('Xingya Check-in · Dedicated Credit')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Create the check-in group normally, then select it here. Changing the group moves dedicated keys; existing credit and expiry dates are retained. Native token and request pricing are supported.'
        )}
      </p>
      <div className='bg-muted flex flex-col gap-1 rounded-lg p-4 text-sm'>
        <p className='font-medium'>{t('Credit validity · fixed 30 days')}</p>
        <p className='text-muted-foreground'>
          {t(
            'Each award expires 30 × 24 hours after receipt. New check-ins do not renew old credit, and pausing claims or spending does not pause expiry.'
          )}
        </p>
      </div>
      <div className='flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center sm:justify-between'>
        <p className='text-muted-foreground text-sm leading-relaxed'>
          {t(
            'Let everyone check in once more today. Issued rewards are retained; the normal daily limit resumes tomorrow.'
          )}
        </p>
        <Button
          variant='outline'
          className='min-h-11 shrink-0'
          disabled={
            refresh.isPending || save.isPending || !query.data.eligibility
          }
          onClick={() => {
            if (query.data?.eligibility) {
              setRefreshEligibility({ ...query.data.eligibility })
            }
          }}
        >
          {t('Refresh today’s check-in eligibility')}
        </Button>
      </div>
      <ConfirmDialog
        open={refreshEligibility !== null}
        onOpenChange={(open) => {
          if (!open) setRefreshEligibility(null)
        }}
        title={t('Refresh today’s check-in eligibility')}
        desc={t(
          'Every user can claim one extra reward today, including users who have already checked in. Existing rewards will be kept. This action cannot be undone.'
        )}
        confirmText={t('Allow one more check-in')}
        isLoading={refresh.isPending}
        handleConfirm={() => {
          if (refreshEligibility && !refresh.isPending) {
            refresh.mutate(refreshEligibility)
          }
        }}
      />
      <Form {...form}>
        <SettingsForm>
          <FormField
            control={form.control}
            name='min_reward'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Daily minimum (sprouts)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='decimal'
                    step='0.01'
                    min='0'
                    max='100000'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='max_reward'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Daily maximum (sprouts)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='decimal'
                    step='0.01'
                    min='0'
                    max='100000'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='consume_threshold'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Cumulative spending threshold (sprouts)')}
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='decimal'
                    step='0.01'
                    min='0'
                    max='100000'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'At or above this threshold, daily claims use the fixed reward. Below it, the random range applies. Set 0 to disable.'
                  )}
                </FormDescription>
                <FormDescription>
                  {t(
                    'Uses cumulative non-check-in consumption minus self-refunded amounts. Historical consumption counts; check-in spending does not.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='fixed_reward'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Fixed daily reward after threshold (sprouts)')}
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='decimal'
                    step='0.01'
                    min='0'
                    max='100000'
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='group'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Check-in group')}</FormLabel>
                <FormControl>
                  <ApiKeyGroupCombobox
                    options={groups}
                    value={field.value}
                    onValueChange={field.onChange}
                    placeholder={t('Select a group')}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Only dedicated keys can spend check-in credit. Regular keys keep their existing payment source.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='spend_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4 rounded-lg border p-4'>
                <div>
                  <FormLabel>{t('Allow check-in spending')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Turning this off blocks new requests. Existing credit still expires on its original date; history is retained.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='claim_enabled'
            render={({ field }) => (
              <FormItem className='flex flex-row items-center justify-between gap-4 rounded-lg border p-4'>
                <div>
                  <FormLabel>{t('Allow daily claims')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Claims require an active web login session. Check-in spending does not count toward invite rewards.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
        {!pageActions && (
          <div className='mt-6 flex justify-end'>
            <Button
              className='min-h-11'
              onClick={onSave}
              disabled={save.isPending || !form.formState.isDirty}
            >
              {save.isPending ? t('Saving...') : t('Save Changes')}
            </Button>
          </div>
        )}
        <SettingsPageFormActions
          onSave={onSave}
          onReset={() => {
            if (query.data) form.reset(query.data.settings)
          }}
          isSaving={save.isPending}
          isSaveDisabled={!form.formState.isDirty}
        />
      </Form>
      <CheckinReviewTable />
    </SettingsSection>
  )
}
