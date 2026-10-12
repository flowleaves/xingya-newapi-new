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
import { z } from 'zod'

export function checkinSettingsSchema(t: (key: string) => string) {
  const amount = z.coerce.number().min(0).max(100000).multipleOf(0.01)
  return z
    .object({
      claim_enabled: z.boolean(),
      spend_enabled: z.boolean(),
      min_reward: amount,
      max_reward: amount,
      group: z.string(),
      revision: z.number().int().nonnegative(),
    })
    .refine((s) => s.min_reward <= s.max_reward, {
      path: ['max_reward'],
      message: t('The maximum must be at least the minimum.'),
    })
    .refine(
      (s) =>
        !s.claim_enabled ||
        (s.min_reward > 0 && s.group !== '' && s.spend_enabled),
      {
        path: ['claim_enabled'],
        message: t(
          'Configure positive rewards and enable group spending before claims.'
        ),
      }
    )
    .refine((s) => !s.spend_enabled || (s.group !== '' && s.group !== 'auto'), {
      path: ['group'],
      message: t('Select a group'),
    })
}
