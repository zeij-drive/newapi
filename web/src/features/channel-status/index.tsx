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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getChannelStatus } from './api'
import { ProbeSettings } from './components/probe-settings'
import { StatusSummary } from './components/status-summary'

export function ChannelStatus() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['channel-status', 'summary'],
    queryFn: getChannelStatus,
    refetchInterval: 30_000,
  })
  const isRoot = (user?.role ?? 0) >= ROLE.SUPER_ADMIN

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Channel status')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          disabled={query.isFetching}
          onClick={() =>
            void queryClient.invalidateQueries({ queryKey: ['channel-status'] })
          }
        >
          <RefreshCw
            className={query.isFetching ? 'animate-spin' : ''}
            aria-hidden='true'
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <StatusSummary
            summary={query.data}
            isLoading={query.isLoading}
            error={query.error}
            onRetry={() => void query.refetch()}
          />
          {isRoot && <ProbeSettings />}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
