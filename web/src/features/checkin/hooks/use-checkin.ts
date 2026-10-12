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
import {
  useIsMutating,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { formatQuotaWithCurrency } from '@/lib/currency'

import { claimXingyaCheckin, createCheckinKey, getXingyaCheckin } from '../api'

export function useCheckin(month?: string) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['xingya-checkin', month ?? 'current'],
    queryFn: () => getXingyaCheckin(month),
    staleTime: 15_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: 'always',
  })
  const claim = useMutation({
    mutationKey: ['xingya-checkin-claim'],
    mutationFn: claimXingyaCheckin,
    meta: { errorToast: false },
    onSuccess: async (award) => {
      if (!award.already_claimed) {
        toast.success(
          `${t('Check-in successful! Received')} ${formatQuotaWithCurrency(award.quota_awarded)}`
        )
      }
      await queryClient.invalidateQueries({ queryKey: ['xingya-checkin'] })
    },
  })
  const key = useMutation({
    mutationKey: ['xingya-checkin-key'],
    mutationFn: createCheckinKey,
    meta: { errorToast: false },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['api-keys'] }),
  })
  const claiming = useIsMutating({ mutationKey: ['xingya-checkin-claim'] }) > 0
  const creatingKey = useIsMutating({ mutationKey: ['xingya-checkin-key'] }) > 0
  return { query, claim, key, claiming, creatingKey }
}
