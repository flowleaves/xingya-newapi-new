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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type i18n } from 'i18next'
import { useState, type ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { BotProtectionSection } from '../bot-protection-section'

let testI18n: i18n

const defaults = {
  TurnstileCheckEnabled: false,
  TurnstileSiteKey: '',
  TurnstileSecretKey: '',
  RegistrationDeviceLimitEnabled: false,
  RegistrationDeviceLimitWhitelist: '',
}

function Fixture(props: {
  children?: ReactNode
  overrides?: Partial<typeof defaults>
}) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  return (
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={queryClient}>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          {props.children ?? (
            <BotProtectionSection
              defaultValues={{ ...defaults, ...props.overrides }}
            />
          )}
        </SettingsPageProvider>
      </QueryClientProvider>
    </I18nextProvider>
  )
}

beforeEach(async () => {
  testI18n = createInstance()
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    resources: { en: { translation: {} }, zh },
    interpolation: { escapeValue: false },
  })
  localStorage.clear()
})

describe('Registration device limit settings', () => {
  it('offers the device limit switched off, with the exempt address list empty', () => {
    render(<Fixture />)

    const limit = screen.getByRole('switch', {
      name: 'Limit one registration per device',
    })
    expect(limit).not.toBeChecked()
    expect(
      screen.getByRole('textbox', { name: 'Exempt addresses' })
    ).toHaveValue('')
  })

  it('saves the enabled flag and the exempt address list under their own option keys', async () => {
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: { success: true, message: '' },
    })
    const user = userEvent.setup()
    render(<Fixture />)

    await user.click(
      screen.getByRole('switch', { name: 'Limit one registration per device' })
    )
    await user.type(
      screen.getByRole('textbox', { name: 'Exempt addresses' }),
      '10.0.0.0/8'
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await vi.waitFor(() => {
      expect(put).toHaveBeenCalledWith('/api/option/', {
        key: 'RegistrationDeviceLimitEnabled',
        value: true,
      })
    })
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'RegistrationDeviceLimitWhitelist',
      value: '10.0.0.0/8',
    })
  })

  it('loads an existing exempt address list back into the field', () => {
    render(
      <Fixture overrides={{ RegistrationDeviceLimitWhitelist: '1.2.3.4' }} />
    )

    expect(
      screen.getByRole('textbox', { name: 'Exempt addresses' })
    ).toHaveValue('1.2.3.4')
  })
})
