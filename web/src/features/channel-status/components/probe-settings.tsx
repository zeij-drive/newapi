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
import { Pencil, Play, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getChannelStatusProbes,
  runAllChannelStatusProbes,
  runChannelStatusProbe,
  updateChannelStatusProbes,
  type ChannelStatusProbe,
  type ProbeResult,
} from '../api'
import { ProbeEditor } from './probe-editor'
import { ChannelHealthBadge } from './status-summary'

export function ProbeSettings() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['channel-status', 'probes'],
    queryFn: getChannelStatusProbes,
    refetchInterval: 10_000,
  })
  const [editorOpen, setEditorOpen] = useState(false)
  const [editing, setEditing] = useState<ChannelStatusProbe>()
  const [deleting, setDeleting] = useState<ChannelStatusProbe>()
  const saveMutation = useMutation({
    mutationFn: updateChannelStatusProbes,
    onSuccess: (data) => {
      queryClient.setQueryData(['channel-status', 'probes'], data)
      void queryClient.invalidateQueries({
        queryKey: ['channel-status', 'summary'],
      })
      setEditorOpen(false)
      toast.success(t('Probe settings saved'))
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save probe settings')),
  })
  const runMutation = useMutation({
    mutationFn: runChannelStatusProbe,
    onSuccess: () => {
      toast.success(t('Probe run queued'))
      void queryClient.invalidateQueries({ queryKey: ['channel-status'] })
    },
    onError: (error) => handleServerError(error, t('Probe run failed')),
  })
  const runAllMutation = useMutation({
    mutationFn: runAllChannelStatusProbes,
    onSuccess: () => {
      toast.success(t('Probe run queued'))
      void queryClient.invalidateQueries({ queryKey: ['channel-status'] })
    },
    onError: (error) => handleServerError(error, t('Probe run failed')),
  })
  if (query.isLoading) {
    return <LoadingState message={t('Loading probe settings...')} />
  }
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load probe settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.data) return null

  const data = query.data
  const results = new Map(
    data.results.map((result) => [result.probe_id, result])
  )
  const save = (probe: ChannelStatusProbe) => {
    const probes = probe.id
      ? data.config.probes.map((item) => (item.id === probe.id ? probe : item))
      : [...data.config.probes, probe]
    saveMutation.mutate({ enabled: data.config.enabled, probes })
  }
  const remove = () => {
    if (!deleting) return
    saveMutation.mutate(
      {
        enabled: data.config.enabled,
        probes: data.config.probes.filter((probe) => probe.id !== deleting.id),
      },
      { onSuccess: () => setDeleting(undefined) }
    )
  }
  const running =
    runMutation.isPending ||
    runAllMutation.isPending ||
    data.results.some((result) => result.running)

  return (
    <>
      <section className='bg-card overflow-hidden rounded-lg border shadow-xs'>
        <div className='flex flex-col gap-3 border-b px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5'>
          <div className='min-w-0'>
            <h3 className='text-sm font-semibold'>{t('Probe settings')}</h3>
            <p className='text-muted-foreground mt-0.5 text-xs'>
              {t('Root administrators can configure and run channel probes.')}
            </p>
          </div>
          <div className='flex items-center gap-2'>
            <Switch
              id='probes-enabled'
              checked={data.config.enabled}
              disabled={saveMutation.isPending}
              onCheckedChange={(enabled) =>
                saveMutation.mutate({ enabled, probes: data.config.probes })
              }
            />
            <Label htmlFor='probes-enabled'>{t('Enable probes')}</Label>
          </div>
        </div>
        <div className='space-y-4 p-4 sm:p-5'>
          <div className='flex flex-wrap justify-end gap-2'>
            <Button
              variant='outline'
              size='sm'
              disabled={
                running || !data.config.probes.some((probe) => probe.enabled)
              }
              onClick={() => runAllMutation.mutate()}
            >
              {runAllMutation.isPending ? (
                <RefreshCw className='animate-spin' aria-hidden='true' />
              ) : (
                <Play aria-hidden='true' />
              )}
              {t('Run all probes')}
            </Button>
            <Button
              size='sm'
              disabled={
                saveMutation.isPending || data.config.probes.length >= 64
              }
              onClick={() => {
                setEditing(undefined)
                setEditorOpen(true)
              }}
            >
              <Plus aria-hidden='true' />
              {t('Add probe')}
            </Button>
          </div>
          {data.config.probes.length === 0 ? (
            <EmptyState
              title={t('No probes configured')}
              description={t('Add a probe to monitor a channel automatically.')}
            />
          ) : (
            <StaticDataTable>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Name')}</TableHead>
                  <TableHead>{t('Channel')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead>{t('Response time')}</TableHead>
                  <TableHead>{t('Last checked')}</TableHead>
                  <TableHead>{t('User requests (10 min)')}</TableHead>
                  <TableHead className='text-right'>{t('Actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.config.probes.map((probe) => (
                  <ProbeRow
                    key={probe.id}
                    probe={probe}
                    channelName={
                      data.channels.find(
                        (channel) => channel.id === probe.channel_id
                      )?.name
                    }
                    result={results.get(probe.id ?? '')}
                    busy={running}
                    saving={saveMutation.isPending}
                    onRun={() => {
                      if (probe.id) runMutation.mutate(probe.id)
                    }}
                    onEdit={() => {
                      setEditing(probe)
                      setEditorOpen(true)
                    }}
                    onDelete={() => setDeleting(probe)}
                  />
                ))}
              </TableBody>
            </StaticDataTable>
          )}
        </div>
      </section>
      <ProbeEditor
        open={editorOpen}
        probe={editing}
        channels={data.channels}
        pending={saveMutation.isPending}
        onOpenChange={setEditorOpen}
        onSave={save}
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(undefined)
        }}
        title={t('Delete probe?')}
        desc={t('This removes the probe and its saved results.')}
        destructive
        handleConfirm={remove}
        isLoading={saveMutation.isPending}
        confirmText={t('Delete')}
      />
    </>
  )
}

function ProbeRow(props: {
  probe: ChannelStatusProbe
  channelName?: string
  result?: ProbeResult
  busy: boolean
  saving: boolean
  onRun: () => void
  onEdit: () => void
  onDelete: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <TableRow>
      <TableCell className='max-w-64 whitespace-normal'>
        <div className='font-medium break-words'>{props.probe.name}</div>
        <div className='text-muted-foreground text-xs break-words'>
          {props.probe.model}
        </div>
        {!props.probe.enabled && (
          <div className='text-muted-foreground text-xs'>{t('Disabled')}</div>
        )}
      </TableCell>
      <TableCell>{props.channelName ?? `#${props.probe.channel_id}`}</TableCell>
      <TableCell className='max-w-80 whitespace-normal'>
        <ChannelHealthBadge status={props.result?.status ?? 'unknown'} />
        {props.result?.message && (
          <p className='text-muted-foreground mt-1 text-xs break-words'>
            {props.result.message}
          </p>
        )}
      </TableCell>
      <TableCell>
        {props.result?.response_time_ms == null
          ? '-'
          : `${formatNumber(props.result.response_time_ms, locale)} ms`}
      </TableCell>
      <TableCell>
        {props.result?.running
          ? t('Running...')
          : formatTimestampToDate(props.result?.checked_at ?? undefined)}
      </TableCell>
      <TableCell>
        <span className='whitespace-nowrap'>
          {t('{{successes}} / {{requests}} successful', {
            successes: formatNumber(props.result?.user_successes ?? 0, locale),
            requests: formatNumber(props.result?.user_requests ?? 0, locale),
          })}
        </span>
      </TableCell>
      <TableCell>
        <div className='flex justify-end gap-1'>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('Run probe')}
            disabled={props.busy || !props.probe.id || !props.probe.enabled}
            onClick={props.onRun}
          >
            <Play aria-hidden='true' />
          </Button>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('Edit probe')}
            disabled={props.saving}
            onClick={props.onEdit}
          >
            <Pencil aria-hidden='true' />
          </Button>
          <Button
            variant='ghost'
            size='icon-sm'
            aria-label={t('Delete probe')}
            disabled={props.saving}
            onClick={props.onDelete}
          >
            <Trash2 className='text-destructive' aria-hidden='true' />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}
