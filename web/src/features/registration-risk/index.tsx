import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CalendarDays,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  ShieldAlert,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatTimestampToDate } from '@/lib/format'

import { getRegistrationRisk } from './api'
import { RegistrationsTable } from './components/registrations-table'

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
  const queryClient = useQueryClient()
  const today = new Date()
  const [page, setPage] = useState(1)
  const [fromDate, setFromDate] = useState(() => {
    const date = new Date(today)
    date.setDate(date.getDate() - 90)
    return dateInputValue(date)
  })
  const [toDate, setToDate] = useState(() => dateInputValue(today))
  const [minRepeats, setMinRepeats] = useState(2)
  const from = dayStart(fromDate)
  const to = dayEnd(toDate)
  const validRange = Number.isFinite(from) && Number.isFinite(to) && from <= to
  const query = useQuery({
    queryKey: ['registration-risk', page, fromDate, toDate, minRepeats],
    queryFn: () =>
      getRegistrationRisk({
        page,
        pageSize: PAGE_SIZE,
        from,
        to,
        minRepeats,
      }),
    enabled: validRange,
    gcTime: 0,
  })
  const data = query.data?.data
  const totalPages = Math.max(1, Math.ceil((data?.total || 0) / PAGE_SIZE))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Registration Risk')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          onClick={() => {
            void query.refetch()
            void queryClient.refetchQueries({
              queryKey: ['registration-devices'],
              type: 'active',
            })
          }}
          disabled={query.isFetching || !validRange}
          aria-label={t('Refresh')}
        >
          <RefreshCw
            className={query.isFetching ? 'animate-spin' : undefined}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Registration Risk')}</CardTitle>
            </CardHeader>
            <CardContent className='grid gap-3 sm:grid-cols-[minmax(180px,1fr)_minmax(180px,1fr)_120px_auto] sm:items-end'>
              <label className='grid gap-1 text-sm'>
                <span className='text-muted-foreground'>{t('From')}</span>
                <div className='relative'>
                  <CalendarDays className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2' />
                  <Input
                    type='date'
                    value={fromDate}
                    onChange={(event) => {
                      setPage(1)
                      setFromDate(event.target.value)
                    }}
                    className='pl-8'
                  />
                </div>
              </label>
              <label className='grid gap-1 text-sm'>
                <span className='text-muted-foreground'>{t('To')}</span>
                <div className='relative'>
                  <CalendarDays className='text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2' />
                  <Input
                    type='date'
                    value={toDate}
                    onChange={(event) => {
                      setPage(1)
                      setToDate(event.target.value)
                    }}
                    className='pl-8'
                  />
                </div>
              </label>
              <label className='grid gap-1 text-sm'>
                <span className='text-muted-foreground'>
                  {t('Minimum repeats')}
                </span>
                <Input
                  type='number'
                  min={2}
                  value={minRepeats}
                  onChange={(event) => {
                    setPage(1)
                    setMinRepeats(Math.max(2, Number(event.target.value) || 2))
                  }}
                />
              </label>
              <div className='text-muted-foreground text-sm'>
                {t('{{count}} repeated groups', { count: data?.total || 0 })}
              </div>
            </CardContent>
          </Card>

          {!validRange && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Please select a valid date range.')}
            </p>
          )}

          <RegistrationsTable
            key={`${fromDate}-${toDate}`}
            from={from}
            to={to}
            enabled={validRange}
          />

          <Card>
            <CardHeader>
              <CardTitle className='flex items-center gap-2'>
                <ShieldAlert />
                {t('Repeated registration fingerprints')}
              </CardTitle>
              <CardDescription>
                {t(
                  'This view shows repeated IP and user-agent fingerprints. Exact IP addresses are available for new records; historical addresses cannot be recovered.'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='p-0'>
              {query.isError ? (
                <ErrorState
                  title={t('Failed to load registration records')}
                  onRetry={() => void query.refetch()}
                />
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('IP Address')}</TableHead>
                      <TableHead>{t('Fingerprint')}</TableHead>
                      <TableHead>{t('Registrations')}</TableHead>
                      <TableHead>{t('User agent')}</TableHead>
                      <TableHead>{t('Accounts')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data?.items?.length ? (
                      data.items.map((group) => (
                        <TableRow
                          key={`${group.ip_fingerprint}-${group.ua_fingerprint}-${group.secret_version}`}
                        >
                          <TableCell className='font-mono text-xs'>
                            {group.client_ip || t('Not recorded')}
                          </TableCell>
                          <TableCell>
                            <div className='grid gap-1 text-xs'>
                              <Badge variant='outline'>
                                x{group.repeat_count}
                              </Badge>
                              <code>{group.ip_fingerprint}</code>
                              <code>{group.ua_fingerprint}</code>
                              <span className='text-muted-foreground'>
                                {t('Latest')}:{' '}
                                {formatTimestampToDate(group.latest_reg_time)}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell className='min-w-56'>
                            <div className='grid gap-2'>
                              {group.registrations.map((registration) => (
                                <div
                                  key={`${registration.user_id}-${registration.reg_time}`}
                                  className='text-xs'
                                >
                                  <span className='font-medium'>
                                    {registration.username ||
                                      `#${registration.user_id}`}
                                  </span>
                                  <span className='text-muted-foreground ml-2'>
                                    #{registration.user_id} ·{' '}
                                    {formatTimestampToDate(
                                      registration.reg_time
                                    )}
                                  </span>
                                </div>
                              ))}
                            </div>
                          </TableCell>
                          <TableCell className='max-w-md text-xs break-all whitespace-normal'>
                            {group.user_agent || t('Unavailable')}
                          </TableCell>
                          <TableCell className='font-mono tabular-nums'>
                            {group.repeat_count}
                          </TableCell>
                        </TableRow>
                      ))
                    ) : (
                      <TableRow>
                        <TableCell
                          colSpan={5}
                          className='text-muted-foreground py-10 text-center'
                        >
                          {query.isFetching
                            ? t('Loading...')
                            : t('No repeated registrations found.')}
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          <div className='flex items-center justify-end gap-2'>
            <Button
              variant='outline'
              size='sm'
              onClick={() => setPage((value) => Math.max(1, value - 1))}
              disabled={
                page <= 1 || query.isFetching || query.isError || !validRange
              }
              aria-label={t('Previous page')}
            >
              <ChevronLeft />
              {t('Previous')}
            </Button>
            <span className='text-muted-foreground text-sm tabular-nums'>
              {page} / {totalPages}
            </span>
            <Button
              variant='outline'
              size='sm'
              onClick={() =>
                setPage((value) => Math.min(totalPages, value + 1))
              }
              disabled={
                page >= totalPages ||
                query.isFetching ||
                query.isError ||
                !validRange
              }
              aria-label={t('Next page')}
            >
              {t('Next')}
              <ChevronRight />
            </Button>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
