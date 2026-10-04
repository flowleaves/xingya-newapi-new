import { createFileRoute } from '@tanstack/react-router'

import { InviteRewardsPage } from '@/features/invite-rewards'

export const Route = createFileRoute('/_authenticated/invite-rewards')({
  component: InviteRewardsPage,
})
