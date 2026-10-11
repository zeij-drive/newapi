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
import { Clock3, Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { useSystemConfigStore } from '@/stores/system-config-store'

import type { UsageRankingRewardPreview } from '../api'

type UsageRankingRewardsProps = {
  reward: UsageRankingRewardPreview
}

function formatRankingDate(timestamp: number, locale: Intl.LocalesArgument) {
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    timeZone: 'Asia/Shanghai',
  }).format(timestamp * 1000)
}

export function UsageRankingRewards(props: UsageRankingRewardsProps) {
  const { t, i18n } = useTranslation()
  useSystemConfigStore((state) => state.config.currency)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const start = formatRankingDate(props.reward.period_start, locale)
  const end = formatRankingDate(props.reward.period_end - 1, locale)
  const settlementTime = new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZone: 'Asia/Shanghai',
  }).format(props.reward.settlement_at * 1000)

  return (
    <section aria-label={t('Upcoming balance rewards')}>
      <Card className='border-primary/20 bg-primary/5'>
        <CardHeader className='gap-2'>
          <CardTitle className='text-primary flex items-center gap-2'>
            <Gift aria-hidden className='size-5' />
            {t('Upcoming balance rewards')}
          </CardTitle>
          <p className='text-muted-foreground flex flex-wrap items-center gap-2 text-sm'>
            <Clock3 aria-hidden className='size-4 shrink-0' />
            <span>{t('Estimated settlement')}:</span>
            <time
              className='text-foreground font-medium'
              dateTime={new Date(
                props.reward.settlement_at * 1000
              ).toISOString()}
            >
              {settlementTime} (UTC+8)
            </time>
          </p>
          <p className='text-muted-foreground text-sm'>
            {t('Reward period: {{start}}–{{end}}', { start, end })}
          </p>
        </CardHeader>
        <CardContent>
          {props.reward.recipients.length > 0 ? (
            <ol
              aria-label={t('Expected reward recipients')}
              className='bg-background divide-y rounded-lg border px-4'
            >
              {props.reward.recipients.map((recipient) => (
                <li
                  key={`${recipient.user_id}-${recipient.rank}`}
                  aria-label={t('Rank {{rank}}', { rank: recipient.rank })}
                  className='flex min-w-0 items-center gap-3 py-3 sm:gap-4'
                >
                  <span className='text-muted-foreground w-7 shrink-0 text-right font-mono text-sm'>
                    {recipient.rank}
                  </span>
                  <span
                    className='min-w-0 flex-1 truncate font-medium'
                    title={recipient.name}
                  >
                    {recipient.name}
                  </span>
                  <strong className='shrink-0 font-mono text-sm font-semibold tabular-nums sm:text-base'>
                    +
                    {formatQuotaWithCurrency(recipient.quota, {
                      locale,
                      abbreviate: false,
                    })}
                  </strong>
                </li>
              ))}
            </ol>
          ) : (
            <p className='text-muted-foreground text-sm'>
              {props.reward.eligible
                ? t('No eligible recipients yet')
                : t('Rewards begin next period')}
            </p>
          )}
        </CardContent>
      </Card>
    </section>
  )
}
