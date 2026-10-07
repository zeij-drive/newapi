import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getUsageRanking, type UsageRankingPeriod } from '../api'

const periods: Array<{ value: UsageRankingPeriod; label: string }> = [
  { value: 'today', label: 'Today' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
  { value: 'year', label: 'Year' },
  { value: 'all', label: 'All time' },
]

export function UserUsageSection() {
  const { t, i18n } = useTranslation()
  const [period, setPeriod] = useState<UsageRankingPeriod>('week')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const ranking = useQuery({
    queryKey: ['usage-ranking', period],
    queryFn: async () => requireServerSuccess(await getUsageRanking(period)),
    staleTime: 60_000,
  })
  const snapshot = ranking.data?.data

  let content: ReactNode
  if (ranking.isPending) {
    content = <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
  } else if (ranking.isError) {
    content = (
      <p className='text-destructive text-sm'>{t('Unable to load rankings')}</p>
    )
  } else if (!snapshot?.users.length) {
    content = (
      <p className='text-muted-foreground text-sm'>{t('No usage data yet')}</p>
    )
  } else {
    content = (
      <ol className='divide-y'>
        {snapshot.users.map((user, index) => (
          <li
            key={user.user_id}
            className='flex min-w-0 items-center gap-4 py-3'
          >
            <span className='text-muted-foreground w-7 shrink-0 text-right font-mono text-sm'>
              {index + 1}.
            </span>
            <span className='min-w-0 flex-1 truncate font-medium'>
              {user.name}
            </span>
            <span className='shrink-0 font-mono text-sm tabular-nums'>
              {formatNumber(user.total_tokens, locale)}
            </span>
          </li>
        ))}
      </ol>
    )
  }

  return (
    <section className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h2 className='text-lg font-semibold'>{t('User token leaderboard')}</h2>
        <div
          className='flex flex-wrap gap-1'
          role='group'
          aria-label={t('Period')}
        >
          {periods.map((item) => (
            <Button
              key={item.value}
              type='button'
              size='sm'
              variant={period === item.value ? 'secondary' : 'ghost'}
              aria-pressed={period === item.value}
              onClick={() => setPeriod(item.value)}
            >
              {t(item.label)}
            </Button>
          ))}
        </div>
      </div>
      {snapshot && (
        <div className='border-y py-3 text-sm'>
          <span className='text-muted-foreground'>
            {t('Site-wide token usage')}
          </span>
          <strong className='ml-3 font-mono text-base tabular-nums'>
            {formatNumber(snapshot.total_tokens, locale)}
          </strong>
        </div>
      )}
      {content}
    </section>
  )
}
