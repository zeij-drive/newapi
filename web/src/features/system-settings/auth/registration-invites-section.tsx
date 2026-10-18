import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  createRegistrationInvites,
  listRegistrationInvites,
  updateRegistrationInviteStatus,
  type RegistrationInviteRecord,
} from '../api'
import { SettingsSection } from '../components/settings-section'

function statusLabel(status: number) {
  switch (status) {
    case 1:
      return 'Available'
    case 2:
      return 'Claimed'
    case 3:
      return 'Used'
    default:
      return 'Disabled'
  }
}

export function RegistrationInvitesSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [count, setCount] = useState('1')
  const [page, setPage] = useState(1)
  const [createdCodes, setCreatedCodes] = useState<string[]>([])
  const inviteQuery = useQuery({
    queryKey: ['registration-invites', page],
    queryFn: async () =>
      requireServerSuccess(await listRegistrationInvites(page)),
  })

  const createMutation = useMutation({
    mutationFn: async () => {
      const amount = Number(count)
      if (!Number.isInteger(amount) || amount < 1 || amount > 1000) {
        throw new Error(t('Enter a number between 1 and 1000'))
      }
      return requireServerSuccess(await createRegistrationInvites(amount))
    },
    onSuccess: (response) => {
      setCreatedCodes(response.data ?? [])
      void queryClient.invalidateQueries({ queryKey: ['registration-invites'] })
      toast.success(t('Invitation codes created'))
    },
    onError: (error: Error) => handleServerError(error),
  })

  const statusMutation = useMutation({
    mutationFn: async (invite: RegistrationInviteRecord) =>
      requireServerSuccess(
        await updateRegistrationInviteStatus(
          invite.id,
          invite.status === 4 ? 1 : 4
        )
      ),
    onSuccess: () =>
      void queryClient.invalidateQueries({ queryKey: ['registration-invites'] }),
    onError: (error: Error) => handleServerError(error),
  })

  const records = inviteQuery.data?.data?.items ?? []
  const total = inviteQuery.data?.data?.total ?? 0
  const columns = [
    {
      id: 'code',
      header: t('Code prefix'),
      cell: (invite: RegistrationInviteRecord) => invite.code_prefix,
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (invite: RegistrationInviteRecord) => (
        <Badge variant={invite.status === 1 ? 'secondary' : 'outline'}>
          {t(statusLabel(invite.status))}
        </Badge>
      ),
    },
    {
      id: 'created',
      header: t('Created at'),
      cell: (invite: RegistrationInviteRecord) =>
        formatTimestampToDate(invite.created_at),
    },
    {
      id: 'expires',
      header: t('Expires at'),
      cell: (invite: RegistrationInviteRecord) =>
        formatTimestampToDate(invite.expires_at),
    },
    {
      id: 'action',
      header: t('Action'),
      cell: (invite: RegistrationInviteRecord) =>
        invite.status === 3 ? null : (
          <Button
            type='button'
            size='sm'
            variant='outline'
            disabled={statusMutation.isPending}
            onClick={() => statusMutation.mutate(invite)}
          >
            {invite.status === 4 ? t('Enable') : t('Disable')}
          </Button>
        ),
    },
  ]

  return (
    <SettingsSection title={t('Registration invitations')}>
      <div className='space-y-4'>
        <p className='text-muted-foreground text-sm'>
          {t('Create one-time invitation codes to share with new users.')}
        </p>
        <div className='flex flex-wrap items-end gap-3'>
          <div className='grid w-36 gap-2'>
            <Label htmlFor='registration-invite-count'>{t('Number of codes')}</Label>
            <Input
              id='registration-invite-count'
              type='number'
              min={1}
              max={1000}
              step={1}
              value={count}
              onChange={(event) => setCount(event.target.value)}
            />
          </div>
          <Button
            type='button'
            disabled={createMutation.isPending}
            onClick={() => createMutation.mutate()}
          >
            {createMutation.isPending ? t('Creating...') : t('Create codes')}
          </Button>
        </div>

        {createdCodes.length > 0 && (
          <div className='bg-muted/40 space-y-2 rounded-lg border p-3'>
            <p className='text-sm font-medium'>{t('Copy these codes now')}</p>
            <p className='text-muted-foreground text-xs'>
              {t('Full codes are shown only once.')}
            </p>
            {createdCodes.map((code) => (
              <div
                key={code}
                className='flex items-center justify-between gap-2 rounded-md border bg-background px-3 py-2 font-mono text-sm'
              >
                <code className='break-all'>{code}</code>
                <CopyButton value={code} aria-label={t('Copy invitation code')} />
              </div>
            ))}
          </div>
        )}

        {inviteQuery.isError ? (
          <Button
            type='button'
            variant='outline'
            onClick={() => void inviteQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        ) : (
          <>
            <StaticDataTable
              columns={columns}
              data={records}
              getRowKey={(invite) => invite.id}
              emptyContent={
                inviteQuery.isPending ? t('Loading...') : t('No invitation codes')
              }
            />
            <div className='flex items-center justify-between gap-3'>
              <span className='text-muted-foreground text-sm'>
                {t('{{count}} invitation codes', { count: total })}
              </span>
              <div className='flex gap-2'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={page <= 1 || inviteQuery.isFetching}
                  onClick={() => setPage((current) => current - 1)}
                >
                  {t('Previous')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={page * 25 >= total || inviteQuery.isFetching}
                  onClick={() => setPage((current) => current + 1)}
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          </>
        )}
      </div>
    </SettingsSection>
  )
}
