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
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import type { ChannelStatus, ChannelStatusSummary } from '../api'

export function ChannelHealthBadge(props: { status: ChannelStatus }) {
  const { t } = useTranslation()
  let label = t('Not checked')
  let variant: 'success' | 'warning' | 'danger' | 'neutral' = 'neutral'
  if (props.status === 'operational') {
    label = t('Operational')
    variant = 'success'
  } else if (props.status === 'degraded') {
    label = t('Degraded')
    variant = 'warning'
  } else if (props.status === 'outage') {
    label = t('Outage')
    variant = 'danger'
  }
  return (
    <StatusBadge label={label} variant={variant} copyable={false} showDot />
  )
}

export function StatusSummary(props: {
  summary?: ChannelStatusSummary
  isLoading: boolean
  error: unknown
  onRetry: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  if (props.isLoading) return <LoadingState />
  if (props.error) {
    return (
      <ErrorState
        description={getServerErrorMessage(props.error)}
        onRetry={props.onRetry}
      />
    )
  }
  if (!props.summary?.groups.length) {
    return (
      <EmptyState
        title={t('No channel groups')}
        description={t('Group status appears after channels are configured.')}
      />
    )
  }

  return (
    <section aria-label={t('Group status')} className='space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Availability is summarized by group. Channel details are visible only to root administrators.'
        )}
      </p>
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-3'>
        {props.summary.groups.map((group) => (
          <Card key={group.group} className='min-w-0'>
            <CardHeader>
              <CardTitle className='break-words'>
                {group.group || t('Default')}
              </CardTitle>
              <CardDescription>
                <ChannelHealthBadge status={group.status} />
              </CardDescription>
            </CardHeader>
            <CardContent>
              <dl className='space-y-2 text-sm'>
                <div className='flex justify-between gap-3'>
                  <dt className='text-muted-foreground'>
                    {t('Available channels')}
                  </dt>
                  <dd className='tabular-nums'>
                    {formatNumber(group.available_channels, locale)} /{' '}
                    {formatNumber(group.total_channels, locale)}
                  </dd>
                </div>
                <div className='flex justify-between gap-3'>
                  <dt className='text-muted-foreground'>
                    {t('Response time')}
                  </dt>
                  <dd className='tabular-nums'>
                    {group.response_time_ms == null
                      ? '-'
                      : `${formatNumber(group.response_time_ms, locale)} ms`}
                  </dd>
                </div>
                <div className='flex flex-wrap justify-between gap-2'>
                  <dt className='text-muted-foreground'>{t('Last checked')}</dt>
                  <dd className='tabular-nums'>
                    {formatTimestampToDate(group.last_checked_at ?? undefined)}
                  </dd>
                </div>
              </dl>
            </CardContent>
          </Card>
        ))}
      </div>
    </section>
  )
}
