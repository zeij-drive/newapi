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
import { Download, History, TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type {
  Sub2APIRatioSyncResult,
  Sub2APIRatioSyncRuntime,
} from '@/features/system-settings/models/sub2api-ratio-sync-api'
import { formatDateTimeObject } from '@/lib/time'

type RuntimePanelsProps = {
  runtime: Sub2APIRatioSyncRuntime
  result: Sub2APIRatioSyncResult | null
  busy: boolean
  onDownload: (id: number) => void
}

function formatRatio(value: number): string {
  return value.toFixed(8).replace(/\.?0+$/, '')
}

function formatTimestamp(value: number) {
  return value > 0 ? formatDateTimeObject(new Date(value * 1000)) : '—'
}

export function RuntimePanels(props: RuntimePanelsProps) {
  const { t } = useTranslation()
  const result = props.result
  const rows = result?.rows ?? []
  const warnings = result?.warnings ?? props.runtime.warnings

  return (
    <div className='grid gap-4 xl:grid-cols-[minmax(0,1.3fr)_minmax(18rem,0.7fr)]'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Sync preview')}</CardTitle>
        </CardHeader>
        <CardContent className='space-y-4'>
          {result ? (
            <>
              <div className='flex flex-wrap items-center gap-x-5 gap-y-1 text-sm'>
                <span>
                  {t('Changed groups')}: <strong>{result.changed}</strong>
                </span>
                {result.backup_id ? (
                  <span>
                    {t('Backup')}:{' '}
                    <code className='font-mono'>{result.backup_id}</code>
                  </span>
                ) : null}
                <span className='text-muted-foreground'>
                  {formatTimestamp(result.at)}
                </span>
              </div>
              <div className='overflow-x-auto rounded-lg border'>
                <table className='w-full min-w-[38rem] text-sm'>
                  <thead className='bg-muted/50 text-muted-foreground'>
                    <tr>
                      <th className='px-3 py-2 text-left font-medium'>
                        {t('Source group')}
                      </th>
                      <th className='px-3 py-2 text-left font-medium'>
                        {t('Target group')}
                      </th>
                      <th className='px-3 py-2 text-right font-medium'>
                        {t('Before')}
                      </th>
                      <th className='px-3 py-2 text-right font-medium'>
                        {t('After')}
                      </th>
                      <th className='px-3 py-2 text-right font-medium'>
                        {t('Status')}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <tr
                        key={`${row.source_id}-${row.target}`}
                        className='border-t'
                      >
                        <td className='px-3 py-2'>{row.source}</td>
                        <td className='px-3 py-2'>{row.target}</td>
                        <td className='px-3 py-2 text-right tabular-nums'>
                          {formatRatio(row.before)}
                        </td>
                        <td className='px-3 py-2 text-right tabular-nums'>
                          {formatRatio(row.after)}
                        </td>
                        <td className='px-3 py-2 text-right'>
                          {row.changed ? t('Will change') : t('No change')}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {rows.length === 0 ? (
                <p className='text-muted-foreground text-sm'>
                  {t('No mapped groups to preview.')}
                </p>
              ) : null}
              {warnings.length > 0 ? (
                <div className='border-warning/40 bg-warning/10 space-y-2 rounded-lg border p-3 text-sm'>
                  <div className='flex items-center gap-2 font-medium'>
                    <TriangleAlert className='size-4' aria-hidden='true' />
                    {t('Skipped mappings')}
                  </div>
                  {warnings.map((warning) => (
                    <p
                      key={`${warning.source_id ?? ''}-${warning.target ?? ''}-${warning.message}`}
                    >
                      {warning.message}
                      {warning.target ? ` (${warning.target})` : ''}
                    </p>
                  ))}
                </div>
              ) : null}
            </>
          ) : (
            <p className='text-muted-foreground rounded-lg border border-dashed p-4 text-sm'>
              {t('Run a preview to review the proposed group ratio changes.')}
            </p>
          )}
        </CardContent>
      </Card>

      <div className='space-y-4'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Backups')}</CardTitle>
          </CardHeader>
          <CardContent>
            {props.runtime.backups.length === 0 ? (
              <p className='text-muted-foreground text-sm'>
                {t('No backups yet.')}
              </p>
            ) : (
              <ul className='space-y-2'>
                {props.runtime.backups.map((backup) => (
                  <li
                    key={backup.id}
                    className='flex items-center justify-between gap-2 text-sm'
                  >
                    <span>{formatTimestamp(backup.created_at)}</span>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => props.onDownload(backup.id)}
                      disabled={props.busy}
                    >
                      <Download aria-hidden='true' />
                      {t('Download')}
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className='flex items-center gap-2'>
              <History className='size-4' aria-hidden='true' />
              {t('Recent activity')}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {props.runtime.logs.length === 0 ? (
              <p className='text-muted-foreground text-sm'>
                {t('No sync activity yet.')}
              </p>
            ) : (
              <ol className='space-y-3'>
                {props.runtime.logs.slice(0, 12).map((log) => (
                  <li key={log.id} className='border-l-2 pl-3 text-sm'>
                    <div className='flex flex-wrap items-center justify-between gap-x-3 gap-y-1'>
                      <span
                        className={
                          log.level === 'error'
                            ? 'text-destructive font-medium'
                            : 'font-medium'
                        }
                      >
                        {log.message}
                      </span>
                      <time className='text-muted-foreground text-xs'>
                        {formatTimestamp(log.created_at)}
                      </time>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
