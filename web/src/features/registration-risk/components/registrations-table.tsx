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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  DataTableView,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { formatTimestampToDate } from '@/lib/format'

import { getRegistrationDevices, type RegistrationDeviceItem } from '../api'
import { registrationAgentSummary } from '../lib/user-agent'

export function RegistrationsTable(props: {
  from: number
  to: number
  enabled: boolean
}) {
  const { t } = useTranslation()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const query = useQuery({
    queryKey: [
      'registration-devices',
      props.from,
      props.to,
      pagination.pageIndex,
      pagination.pageSize,
    ],
    queryFn: () =>
      getRegistrationDevices({
        page: pagination.pageIndex + 1,
        pageSize: pagination.pageSize,
        from: props.from,
        to: props.to,
      }),
    enabled: props.enabled,
    placeholderData: keepPreviousData,
    gcTime: 0,
  })
  useEffect(() => {
    if (!query.data || query.isPlaceholderData) return
    const lastPage = Math.max(
      0,
      Math.ceil(query.data.total / pagination.pageSize) - 1
    )
    if (pagination.pageIndex > lastPage) {
      setPagination((current) => ({ ...current, pageIndex: lastPage }))
    }
  }, [
    query.data,
    query.isPlaceholderData,
    pagination.pageIndex,
    pagination.pageSize,
  ])
  const columns = useMemo<ColumnDef<RegistrationDeviceItem>[]>(
    () => [
      {
        accessorKey: 'username',
        header: t('User'),
        cell: ({ row }) => (
          <div className='min-w-32'>
            <div className='max-w-48 font-medium break-all whitespace-normal'>
              {row.original.username || t('Unavailable')}
            </div>
            <span className='text-muted-foreground text-xs'>
              #{row.original.user_id}
            </span>
          </div>
        ),
      },
      {
        accessorKey: 'client_ip',
        header: t('IP Address'),
        cell: ({ row }) => (
          <span className='font-mono text-xs'>
            {row.original.client_ip || t('Not recorded')}
          </span>
        ),
      },
      {
        accessorKey: 'user_agent',
        header: t('User agent'),
        cell: ({ row }) => (
          <TruncatedCell
            className='max-w-64 text-xs'
            tabIndex={0}
            tooltipContent={row.original.user_agent || t('Unavailable')}
          >
            {registrationAgentSummary(row.original.user_agent) ||
              t('Unavailable')}
          </TruncatedCell>
        ),
      },
      {
        accessorKey: 'reg_time',
        header: t('Registration time'),
        cell: ({ row }) => formatTimestampToDate(row.original.reg_time),
      },
    ],
    [t]
  )
  const { table } = useDataTable({
    data: query.data?.items || [],
    columns,
    pagination,
    onPaginationChange: (updater) =>
      setPagination((current) => {
        const next = typeof updater === 'function' ? updater(current) : updater
        return next.pageSize === current.pageSize
          ? next
          : { ...next, pageIndex: 0 }
      }),
    manualPagination: true,
    enableSorting: false,
    enableRowSelection: false,
    totalCount: query.data?.total || 0,
    getRowId: (row) => String(row.user_id),
  })

  return (
    <Card role='region' aria-label={t('Successful registrations')}>
      <CardHeader>
        <CardTitle>{t('Successful registrations')}</CardTitle>
        <CardDescription>
          {t(
            'Recorded successful registrations, newest first. Exact IP addresses are available for new records; historical addresses cannot be recovered. Records are retained for 180 days.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-4' aria-busy={query.isFetching}>
        {query.isError ? (
          <ErrorState
            title={t('Failed to load registration records')}
            onRetry={() => void query.refetch()}
          />
        ) : (
          <DataTableView
            table={table}
            isLoading={query.isLoading}
            emptyTitle={t('No registration records found.')}
            emptyDescription={t('Try adjusting the date range.')}
          />
        )}
        <fieldset
          disabled={query.isFetching || query.isError || !props.enabled}
          className='min-w-0'
        >
          <legend className='sr-only'>
            {t('Registration records pagination')}
          </legend>
          <DataTablePagination table={table} />
        </fieldset>
      </CardContent>
    </Card>
  )
}
