import { createFileRoute, redirect } from '@tanstack/react-router'

import { RegistrationRiskPage } from '@/features/registration-risk'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/registration-risk')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: RegistrationRiskPage,
})
