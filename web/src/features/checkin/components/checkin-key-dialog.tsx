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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { fetchTokenKey } from '@/features/keys/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { CheckinKeyResult } from '../types'

export function CheckinKeyDialog(props: {
  result?: CheckinKeyResult
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [secret, setSecret] = useState('')
  const [loading, setLoading] = useState(false)
  async function reveal() {
    if (!props.result) return
    setLoading(true)
    try {
      const response = requireServerSuccess(
        await fetchTokenKey(props.result.token.id)
      )
      setSecret(response.data?.key ?? '')
    } catch (error) {
      handleServerError(error)
    } finally {
      setLoading(false)
    }
  }
  return (
    <Dialog
      open={!!props.result}
      onOpenChange={(open) => {
        if (!open) {
          setSecret('')
          props.onClose()
        }
      }}
      title={t('Check-in key')}
      description={t(
        'This key uses check-in credit only. If credit runs out, switch to a regular key.'
      )}
    >
      <div className='space-y-4'>
        <p className='font-medium break-all'>{props.result?.token.name}</p>
        <p className='text-muted-foreground text-sm'>
          {t('Group')}:{' '}
          <span className='break-all'>{props.result?.token.group}</span>
        </p>
        {props.result?.reused && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'An existing check-in key was reused. Its limits remain in effect.'
            )}
          </p>
        )}
        {secret ? (
          <div className='flex min-w-0 items-center gap-2'>
            <code className='bg-muted min-w-0 flex-1 rounded-md p-3 break-all'>
              {secret}
            </code>
            <CopyButton
              value={secret}
              aria-label={t('Copy API key')}
              className='size-11'
            />
          </div>
        ) : (
          <Button
            className='min-h-11 w-full'
            disabled={loading}
            onClick={reveal}
          >
            {loading ? t('Loading...') : t('Show API Key')}
          </Button>
        )}
        <Button
          variant='outline'
          className='min-h-11 w-full'
          render={<Link to='/keys' />}
        >
          {t('Manage API Keys')}
        </Button>
      </div>
    </Dialog>
  )
}
