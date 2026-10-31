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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CheckCircle2,
  Circle,
  Clock3,
  Play,
  RefreshCcw,
  Square,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { handleServerError } from '@/lib/handle-server-error'
import { formatDateTimeObject } from '@/lib/time'

import {
  checkSub2APIRatioSyncPair,
  clearSub2APIRatioSyncTokens,
  createSub2APIRatioSyncPair,
  downloadSub2APIRatioSyncBackup,
  getSub2APIRatioSyncLoginHelper,
  getSub2APIRatioSyncState,
  importSub2APIRatioSyncTokens,
  previewSub2APIRatioSync,
  refreshSub2APIRatioSyncGroups,
  runSub2APIRatioSync,
  saveSub2APIRatioSyncMappings,
  saveSub2APIRatioSyncSettings,
  startSub2APIRatioSync,
  stopSub2APIRatioSync,
  type Sub2APIRatioSyncMapping,
  type Sub2APIRatioSyncResult,
  type Sub2APIRatioSyncState,
} from '../sub2api-ratio-sync-api'
import { ConnectionCard, type ConnectionValues } from './connection-card'
import { MappingEditor } from './mapping-editor'
import { RuntimePanels } from './runtime-panels'

const stateQueryKey = ['sub2api-ratio-sync']

type SyncAction =
  | { kind: 'save-settings'; values: ConnectionValues }
  | { kind: 'import-tokens'; accessToken: string; refreshToken: string }
  | { kind: 'clear-tokens' }
  | { kind: 'refresh-groups' }
  | { kind: 'save-mappings'; mappings: Sub2APIRatioSyncMapping[] }
  | { kind: 'preview' }
  | { kind: 'sync-now' }
  | { kind: 'monitor-start'; interval: number }
  | { kind: 'monitor-stop' }

type SyncActionResult =
  | {
      kind: 'preview' | 'sync-now' | 'monitor-start'
      data: Sub2APIRatioSyncResult
    }
  | {
      kind:
        | 'save-settings'
        | 'import-tokens'
        | 'clear-tokens'
        | 'refresh-groups'
        | 'save-mappings'
        | 'monitor-stop'
      data: Sub2APIRatioSyncState
    }

function formatTimestamp(value: number) {
  return value > 0 ? formatDateTimeObject(new Date(value * 1000)) : '—'
}

export function Sub2APIRatioSyncPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<Sub2APIRatioSyncResult | null>(null)
  const [activePair, setActivePair] = useState('')
  const [loginUrl, setLoginUrl] = useState('')
  const [pairMessage, setPairMessage] = useState('')
  const stateQuery = useQuery({
    queryKey: stateQueryKey,
    queryFn: getSub2APIRatioSyncState,
    retry: false,
    refetchInterval: 30000,
  })

  const actionMutation = useMutation<SyncActionResult, Error, SyncAction>({
    mutationFn: async (action) => {
      switch (action.kind) {
        case 'save-settings':
          return {
            kind: action.kind,
            data: await saveSub2APIRatioSyncSettings(action.values),
          }
        case 'import-tokens':
          return {
            kind: action.kind,
            data: await importSub2APIRatioSyncTokens({
              access_token: action.accessToken,
              refresh_token: action.refreshToken,
            }),
          }
        case 'clear-tokens':
          return {
            kind: action.kind,
            data: await clearSub2APIRatioSyncTokens(),
          }
        case 'refresh-groups':
          return {
            kind: action.kind,
            data: await refreshSub2APIRatioSyncGroups(),
          }
        case 'save-mappings':
          return {
            kind: action.kind,
            data: await saveSub2APIRatioSyncMappings(action.mappings),
          }
        case 'preview':
          return { kind: action.kind, data: await previewSub2APIRatioSync() }
        case 'sync-now':
          return { kind: action.kind, data: await runSub2APIRatioSync() }
        case 'monitor-start':
          return {
            kind: action.kind,
            data: await startSub2APIRatioSync(action.interval),
          }
        case 'monitor-stop':
          return { kind: action.kind, data: await stopSub2APIRatioSync() }
      }
    },
    onSuccess: async (result) => {
      if (
        result.kind === 'preview' ||
        result.kind === 'sync-now' ||
        result.kind === 'monitor-start'
      ) {
        setPreview(result.data)
      } else {
        queryClient.setQueryData(stateQueryKey, result.data)
      }
      switch (result.kind) {
        case 'save-settings':
          toast.success(t('Connection saved.'))
          break
        case 'import-tokens':
          toast.success(t('Upstream session saved securely.'))
          break
        case 'clear-tokens':
          toast.success(t('Upstream session cleared.'))
          break
        case 'refresh-groups':
          toast.success(t('Upstream groups refreshed.'))
          break
        case 'save-mappings':
          toast.success(t('Group mappings saved.'))
          break
        case 'preview':
          toast.success(t('Preview is ready.'))
          break
        case 'sync-now':
          toast.success(t('Group ratios synced.'))
          break
        case 'monitor-start':
          toast.success(t('Automatic sync started.'))
          break
        case 'monitor-stop':
          toast.success(t('Automatic sync stopped.'))
          break
      }
      await queryClient.invalidateQueries({ queryKey: stateQueryKey })
    },
    onError: (error, action) => {
      switch (action.kind) {
        case 'save-settings':
          handleServerError(error, t('Failed to save the connection.'))
          break
        case 'import-tokens':
          handleServerError(error, t('Failed to save the upstream session.'))
          break
        case 'clear-tokens':
          handleServerError(error, t('Failed to clear the upstream session.'))
          break
        case 'refresh-groups':
          handleServerError(error, t('Failed to read upstream groups.'))
          break
        case 'save-mappings':
          handleServerError(error, t('Failed to save group mappings.'))
          break
        case 'preview':
          handleServerError(error, t('Failed to preview ratio changes.'))
          break
        case 'sync-now':
          handleServerError(error, t('Failed to sync group ratios.'))
          break
        case 'monitor-start':
          handleServerError(error, t('Failed to start automatic sync.'))
          break
        case 'monitor-stop':
          handleServerError(error, t('Failed to stop automatic sync.'))
          break
      }
    },
  })

  const helperMutation = useMutation({
    mutationFn: getSub2APIRatioSyncLoginHelper,
    onError: (error: Error) =>
      handleServerError(error, t('Failed to load the login helper.')),
  })
  const pairMutation = useMutation({
    mutationFn: createSub2APIRatioSyncPair,
  })

  useEffect(() => {
    if (!activePair) return
    let timer: number | undefined
    let cancelled = false
    const checkPair = async () => {
      try {
        const status = await checkSub2APIRatioSyncPair(activePair)
        if (cancelled) return
        if (status.done) {
          setActivePair('')
          setLoginUrl('')
          setPairMessage(t('Upstream session connected.'))
          toast.success(t('Upstream session connected.'))
          await queryClient.invalidateQueries({ queryKey: stateQueryKey })
          return
        }
        if (status.expired) {
          setActivePair('')
          setLoginUrl('')
          setPairMessage(t('Login pairing expired. Start again to connect.'))
          return
        }
        timer = window.setTimeout(() => void checkPair(), 1500)
      } catch {
        if (!cancelled) timer = window.setTimeout(() => void checkPair(), 5000)
      }
    }
    void checkPair()
    return () => {
      cancelled = true
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [activePair, queryClient, t])

  if (stateQuery.isLoading) return <LoadingState />
  if (stateQuery.isError || !stateQuery.data) {
    return (
      <ErrorState
        description={
          stateQuery.error instanceof Error
            ? stateQuery.error.message
            : undefined
        }
        onRetry={() => void stateQuery.refetch()}
      />
    )
  }

  const state = stateQuery.data
  const busy = actionMutation.isPending || pairMutation.isPending

  const openLogin = () => {
    setPairMessage('')
    const popup = window.open('about:blank', '_blank')
    if (popup) popup.opener = null
    pairMutation.mutate(undefined, {
      onSuccess: (pair) => {
        setActivePair(pair.pair)
        setLoginUrl(popup ? '' : pair.url)
        setPairMessage(t('Waiting for upstream login...'))
        if (popup) popup.location.replace(pair.url)
      },
      onError: (error) => {
        popup?.close()
        handleServerError(error, t('Failed to create a login pairing.'))
      },
    })
  }

  const downloadBackup = async (id: number) => {
    try {
      const blob = await downloadSub2APIRatioSyncBackup(id)
      const objectURL = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = objectURL
      link.download = `group-ratio-backup-${id}.json`
      link.click()
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 0)
    } catch (error) {
      handleServerError(error, t('Failed to download the backup.'))
    }
  }

  return (
    <div className='space-y-4'>
      <Card>
        <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-3'>
          <div className='space-y-1'>
            <CardTitle>{t('Sub2API Ratio Sync')}</CardTitle>
            <p className='text-muted-foreground text-sm'>
              {t('Synchronize Sub2API group ratios to this NewAPI instance.')}
            </p>
          </div>
          <Badge variant={state.settings.enabled ? 'default' : 'secondary'}>
            {state.settings.enabled ? t('Monitoring') : t('Stopped')}
          </Badge>
        </CardHeader>
        <CardContent className='flex flex-wrap items-center gap-x-6 gap-y-2 border-t pt-4 text-sm'>
          <span className='inline-flex items-center gap-2'>
            {state.settings.enabled ? (
              <CheckCircle2
                className='text-success size-4'
                aria-hidden='true'
              />
            ) : (
              <Circle
                className='text-muted-foreground size-4'
                aria-hidden='true'
              />
            )}
            {state.settings.enabled
              ? t('Automatic monitoring is on')
              : t('Automatic monitoring is off')}
          </span>
          <span className='text-muted-foreground inline-flex items-center gap-2'>
            <Clock3 className='size-4' aria-hidden='true' />
            {t('Last check')}: {formatTimestamp(state.runtime.last_run)}
          </span>
          {state.settings.enabled && state.runtime.next_run > 0 ? (
            <span className='text-muted-foreground'>
              {t('Next check')}: {formatTimestamp(state.runtime.next_run)}
            </span>
          ) : null}
        </CardContent>
      </Card>

      <ConnectionCard
        state={state}
        busy={busy}
        loginUrl={loginUrl}
        helperBusy={helperMutation.isPending}
        onSave={(values) =>
          actionMutation.mutateAsync({ kind: 'save-settings', values })
        }
        onImportTokens={(accessToken, refreshToken) =>
          actionMutation.mutateAsync({
            kind: 'import-tokens',
            accessToken,
            refreshToken,
          })
        }
        onClearTokens={() => actionMutation.mutate({ kind: 'clear-tokens' })}
        onOpenLogin={openLogin}
        onGetHelper={() => helperMutation.mutateAsync()}
      />

      {pairMessage ? (
        <p className='text-muted-foreground text-sm' role='status'>
          {pairMessage}
        </p>
      ) : null}

      <MappingEditor
        groups={state.runtime.groups}
        targets={state.runtime.ratios}
        savedMappings={state.settings.mappings}
        busy={busy}
        onSave={(mappings) =>
          actionMutation.mutateAsync({ kind: 'save-mappings', mappings })
        }
        onRefreshGroups={() =>
          actionMutation.mutate({ kind: 'refresh-groups' })
        }
      />

      <Card>
        <CardContent className='flex flex-wrap items-center justify-between gap-3 p-4'>
          <p className='text-muted-foreground text-sm'>
            {t('Automatic sync runs inside the NewAPI system task runner.')}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => actionMutation.mutate({ kind: 'preview' })}
              disabled={
                busy || !state.auth.upstream || !state.settings.mappings.length
              }
            >
              <RefreshCcw aria-hidden='true' />
              {t('Preview changes')}
            </Button>
            <Button
              type='button'
              variant='secondary'
              onClick={() => actionMutation.mutate({ kind: 'sync-now' })}
              disabled={
                busy || !state.auth.upstream || !state.settings.mappings.length
              }
            >
              {t('Sync now')}
            </Button>
            {state.settings.enabled ? (
              <Button
                type='button'
                variant='outline'
                onClick={() => actionMutation.mutate({ kind: 'monitor-stop' })}
                disabled={busy}
              >
                <Square aria-hidden='true' />
                {t('Stop automatic sync')}
              </Button>
            ) : (
              <Button
                type='button'
                onClick={() =>
                  actionMutation.mutate({
                    kind: 'monitor-start',
                    interval: state.settings.interval_seconds,
                  })
                }
                disabled={
                  busy ||
                  !state.auth.upstream ||
                  !state.settings.mappings.length
                }
              >
                <Play aria-hidden='true' />
                {t('Start automatic sync')}
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      <RuntimePanels
        runtime={state.runtime}
        result={preview}
        busy={busy}
        onDownload={(id) => void downloadBackup(id)}
      />
    </div>
  )
}
