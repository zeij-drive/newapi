import { useQuery } from '@tanstack/react-query'
import { Medal, Trophy } from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Card, CardContent } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { requireServerSuccess } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { getUsageRanking } from '../api'
import { formatTokens } from '../lib/format'
import type { RankingPeriod } from '../types'

type UserUsageSectionProps = {
  period: RankingPeriod
}

export function UserUsageSection(props: UserUsageSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const ranking = useQuery({
    queryKey: ['usage-ranking', props.period],
    queryFn: async () =>
      requireServerSuccess(await getUsageRanking(props.period)),
    staleTime: 60_000,
  })
  const snapshot = ranking.data?.data

  let content: ReactNode
  if (ranking.isPending) {
    content = <LoadingState />
  } else if (ranking.isError) {
    content = (
      <ErrorState
        title={t('Unable to load rankings')}
        onRetry={() => void ranking.refetch()}
      />
    )
  } else if (!snapshot?.users.length) {
    content = <EmptyState icon={Trophy} title={t('No usage data yet')} />
  } else {
    content = (
      <div className='flex flex-col gap-5'>
        <ol
          aria-label={t('User token leaderboard')}
          className={cn(
            'grid grid-cols-1 items-end gap-3 sm:gap-4',
            snapshot.users.length === 1 && 'mx-auto w-full max-w-md',
            snapshot.users.length === 2 && 'sm:grid-cols-2',
            snapshot.users.length >= 3 && 'sm:grid-cols-3'
          )}
        >
          {snapshot.users.slice(0, 3).map((user, index) => (
            <li
              key={user.user_id}
              aria-label={t('Rank {{rank}}', { rank: index + 1 })}
              className={cn(
                'min-w-0',
                snapshot.users.length >= 3 &&
                  index === 0 &&
                  'sm:col-start-2 sm:row-start-1',
                snapshot.users.length >= 3 &&
                  index === 1 &&
                  'sm:col-start-1 sm:row-start-1',
                snapshot.users.length >= 3 &&
                  index === 2 &&
                  'sm:col-start-3 sm:row-start-1'
              )}
            >
              <Card
                className={cn(
                  'h-full',
                  index === 0 && 'bg-primary/5 ring-primary/30'
                )}
              >
                <CardContent
                  className={cn(
                    'flex min-w-0 flex-col gap-5 py-2 sm:min-h-48',
                    index === 0 && 'sm:min-h-56'
                  )}
                >
                  <div className='flex items-center justify-between gap-3'>
                    <span
                      className={cn(
                        'text-sm font-semibold',
                        index === 0 ? 'text-primary' : 'text-muted-foreground'
                      )}
                    >
                      {t('Rank {{rank}}', { rank: index + 1 })}
                    </span>
                    {index === 0 ? (
                      <Trophy aria-hidden className='text-primary size-6' />
                    ) : (
                      <Medal
                        aria-hidden
                        className='text-muted-foreground size-5'
                      />
                    )}
                  </div>
                  <div className='flex min-w-0 items-center gap-3 sm:mt-auto'>
                    <Avatar size='lg' aria-hidden>
                      <AvatarFallback>
                        {[...user.name.trim()][0] || '#'}
                      </AvatarFallback>
                    </Avatar>
                    <span
                      className='min-w-0 truncate text-lg font-semibold'
                      title={user.name}
                    >
                      {user.name}
                    </span>
                  </div>
                  <p className='min-w-0 font-mono text-2xl font-semibold break-all tabular-nums sm:text-3xl'>
                    {formatTokens(user.total_tokens, locale)}
                    <span className='text-muted-foreground ml-2 font-sans text-sm font-normal'>
                      {t('tokens')}
                    </span>
                  </p>
                </CardContent>
              </Card>
            </li>
          ))}
        </ol>
        {snapshot.users.length > 3 && (
          <ol
            start={4}
            className='bg-card divide-y rounded-xl border px-4 sm:px-5'
          >
            {snapshot.users.slice(3).map((user, index) => (
              <li
                key={user.user_id}
                aria-label={t('Rank {{rank}}', { rank: index + 4 })}
                className='flex min-w-0 items-center gap-3 py-4 sm:gap-4'
              >
                <span className='text-muted-foreground w-7 shrink-0 text-right font-mono text-sm'>
                  {index + 4}
                </span>
                <span
                  className='min-w-0 flex-1 truncate font-medium'
                  title={user.name}
                >
                  {user.name}
                </span>
                <span className='shrink-0 font-mono text-sm font-medium tabular-nums'>
                  {formatTokens(user.total_tokens, locale)}
                </span>
              </li>
            ))}
          </ol>
        )}
      </div>
    )
  }

  return (
    <section
      className='flex flex-col gap-5'
      aria-label={t('User token leaderboard')}
    >
      <h2 className='text-xl font-semibold'>{t('User token leaderboard')}</h2>
      {snapshot && (
        <div className='flex flex-wrap items-center justify-between gap-3 border-y py-4'>
          <span className='text-muted-foreground text-sm font-medium'>
            {t('Site-wide token usage')}
          </span>
          <strong className='font-mono text-2xl font-semibold break-all tabular-nums sm:text-3xl'>
            {formatTokens(snapshot.total_tokens, locale)}
          </strong>
        </div>
      )}
      {content}
    </section>
  )
}
