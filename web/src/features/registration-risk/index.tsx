import { CalendarDays, ChevronLeft, ChevronRight, RefreshCw, ShieldAlert } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatTimestampToDate } from '@/lib/format'

import { getRegistrationRisk } from './api'

const PAGE_SIZE = 20

function dateInputValue(date: Date) {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function dayStart(value: string) {
  return Math.floor(new Date(`${value}T00:00:00`).getTime() / 1000)
}

function dayEnd(value: string) {
  return Math.floor(new Date(`${value}T23:59:59`).getTime() / 1000)
}

export function RegistrationRiskPage() {
  const { t } = useTranslation()
  const today = new Date()
  const [page, setPage] = useState(1)
  const [fromDate, setFromDate] = useState(() => {
    const date = new Date(today)
    date.setDate(date.getDate() - 90)
    return dateInputValue(date)
  })
  const [toDate, setToDate] = useState(() => dateInputValue(today))
  const [minRepeats, setMinRepeats] = useState(2)
  const query = useQuery({
    queryKey: ['registration-risk', page, fromDate, toDate, minRepeats],
    queryFn: () => getRegistrationRisk({
      page,
      pageSize: PAGE_SIZE,
      from: dayStart(fromDate),
      to: dayEnd(toDate),
      minRepeats,
    }),
  })
  const data = query.data?.data
  const totalPages = Math.max(1, Math.ceil((data?.total || 0) / PAGE_SIZE))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Registration Risk')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' size='sm' onClick={() => void query.refetch()} disabled={query.isFetching} aria-label={t('Refresh')}><RefreshCw className={query.isFetching ? 'animate-spin' : undefined} />{t('Refresh')}</Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'><ShieldAlert />{t('Repeated registration fingerprints')}</CardTitle>
              <CardDescription>{t('Same IP and user-agent registrations are allowed, but only the first registration receives trial quota. This view shows the audit record without raw IP addresses.')}</CardDescription>
            </CardHeader>
            <CardContent className='grid gap-3 sm:grid-cols-[minmax(180px,1fr)_minmax(180px,1fr)_120px_auto] sm:items-end'>
              <label className='grid gap-1 text-sm'><span className='text-muted-foreground'>{t('From')}</span><div className='relative'><CalendarDays className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2' /><Input type='date' value={fromDate} onChange={(event) => { setPage(1); setFromDate(event.target.value) }} className='pl-8' /></div></label>
              <label className='grid gap-1 text-sm'><span className='text-muted-foreground'>{t('To')}</span><div className='relative'><CalendarDays className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2' /><Input type='date' value={toDate} onChange={(event) => { setPage(1); setToDate(event.target.value) }} className='pl-8' /></div></label>
              <label className='grid gap-1 text-sm'><span className='text-muted-foreground'>{t('Minimum repeats')}</span><Input type='number' min={2} value={minRepeats} onChange={(event) => { setPage(1); setMinRepeats(Math.max(2, Number(event.target.value) || 2)) }} /></label>
              <div className='text-muted-foreground text-sm'>{t('{{count}} repeated groups', { count: data?.total || 0 })}</div>
            </CardContent>
          </Card>

          <Card>
            <CardContent className='p-0'>
              <Table>
                <TableHeader><TableRow><TableHead>{t('Fingerprint')}</TableHead><TableHead>{t('Registrations')}</TableHead><TableHead>{t('User agent')}</TableHead><TableHead>{t('Accounts')}</TableHead></TableRow></TableHeader>
                <TableBody>
                  {data?.items?.length ? data.items.map((group) => (
                    <TableRow key={`${group.ip_fingerprint}-${group.ua_fingerprint}-${group.secret_version}`}>
                      <TableCell><div className='grid gap-1 text-xs'><Badge variant='outline'>x{group.repeat_count}</Badge><code>{group.ip_fingerprint}</code><code>{group.ua_fingerprint}</code><span className='text-muted-foreground'>{t('Latest')}: {formatTimestampToDate(group.latest_reg_time)}</span></div></TableCell>
                      <TableCell className='min-w-56'><div className='grid gap-2'>{group.registrations.map((registration) => <div key={`${registration.user_id}-${registration.reg_time}`} className='text-xs'><span className='font-medium'>{registration.username || `#${registration.user_id}`}</span><span className='text-muted-foreground ml-2'>#{registration.user_id} · {formatTimestampToDate(registration.reg_time)}</span></div>)}</div></TableCell>
                      <TableCell className='max-w-md whitespace-normal break-all text-xs'>{group.user_agent || t('Unavailable')}</TableCell>
                      <TableCell className='font-mono tabular-nums'>{group.repeat_count}</TableCell>
                    </TableRow>
                  )) : <TableRow><TableCell colSpan={4} className='text-muted-foreground py-10 text-center'>{t('No repeated registrations found.')}</TableCell></TableRow>}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <div className='flex items-center justify-end gap-2'><Button variant='outline' size='sm' onClick={() => setPage((value) => Math.max(1, value - 1))} disabled={page <= 1 || query.isFetching} aria-label={t('Previous page')}><ChevronLeft />{t('Previous')}</Button><span className='text-muted-foreground text-sm tabular-nums'>{page} / {totalPages}</span><Button variant='outline' size='sm' onClick={() => setPage((value) => Math.min(totalPages, value + 1))} disabled={page >= totalPages || query.isFetching} aria-label={t('Next page')}>{t('Next')}<ChevronRight /></Button></div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
