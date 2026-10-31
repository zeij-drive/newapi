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
import { Plus, RefreshCcw, Save, Trash2 } from 'lucide-react'
import { nanoid } from 'nanoid'
import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import type {
  Sub2APIRatioSyncGroup,
  Sub2APIRatioSyncMapping,
} from '@/features/system-settings/models/sub2api-ratio-sync-api'

const mappingListSchema = z
  .array(
    z.object({
      source_id: z.number().int().positive(),
      target: z.string().min(1).max(128),
      mode: z.enum(['effective', 'default', 'fixed']),
      value: z.number().finite().min(0),
      enabled: z.boolean(),
      allow_peak: z.boolean(),
    })
  )
  .max(500)
  .refine(
    (mappings) =>
      new Set(mappings.map((mapping) => mapping.target)).size ===
      mappings.length
  )

type MappingEditorProps = {
  groups: Sub2APIRatioSyncGroup[]
  targets: Record<string, number>
  savedMappings: Sub2APIRatioSyncMapping[]
  busy: boolean
  onSave: (mappings: Sub2APIRatioSyncMapping[]) => Promise<unknown>
  onRefreshGroups: () => void
}

function formatRatio(value: number): string {
  return value.toFixed(8).replace(/\.?0+$/, '')
}

export function MappingEditor(props: MappingEditorProps) {
  const { t } = useTranslation()
  const [mappings, setMappings] = useState(props.savedMappings)
  const [rowKeys, setRowKeys] = useState(() =>
    props.savedMappings.map(() => nanoid())
  )
  const [invalid, setInvalid] = useState(false)
  const savedMappingsKey = JSON.stringify(props.savedMappings)
  const targetNames = Object.keys(props.targets).sort((left, right) =>
    left.localeCompare(right)
  )

  useEffect(() => {
    const savedMappings = JSON.parse(
      savedMappingsKey
    ) as Sub2APIRatioSyncMapping[]
    setMappings(savedMappings)
    setRowKeys(savedMappings.map(() => nanoid()))
    setInvalid(false)
  }, [savedMappingsKey])

  const updateMapping = (
    index: number,
    patch: Partial<Sub2APIRatioSyncMapping>
  ) => {
    setMappings((current) =>
      current.map((mapping, rowIndex) =>
        rowIndex === index ? { ...mapping, ...patch } : mapping
      )
    )
    setInvalid(false)
  }

  const handleSave = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const parsed = mappingListSchema.safeParse(mappings)
    if (!parsed.success) {
      setInvalid(true)
      return
    }
    await props.onSave(parsed.data)
  }

  const addMapping = () => {
    const firstGroup = props.groups[0]
    const firstTarget = targetNames[0]
    if (!firstGroup || !firstTarget) return
    setMappings((current) => [
      ...current,
      {
        source_id: firstGroup.id,
        target: firstTarget,
        mode: 'effective',
        value: 1,
        enabled: true,
        allow_peak: false,
      },
    ])
    setRowKeys((current) => [...current, nanoid()])
    setInvalid(false)
  }

  return (
    <Card>
      <CardHeader className='flex flex-row items-center justify-between gap-3'>
        <CardTitle>{t('Group mapping')}</CardTitle>
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={props.onRefreshGroups}
            disabled={props.busy}
          >
            <RefreshCcw aria-hidden='true' />
            {t('Read upstream groups')}
          </Button>
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={addMapping}
            disabled={props.busy || !props.groups.length || !targetNames.length}
          >
            <Plus aria-hidden='true' />
            {t('Add mapping')}
          </Button>
        </div>
      </CardHeader>
      <CardContent className='space-y-4'>
        {props.groups.length === 0 || targetNames.length === 0 ? (
          <p className='text-muted-foreground rounded-lg border border-dashed p-4 text-sm'>
            {t(
              'Read upstream groups and configure NewAPI group ratios before creating mappings.'
            )}
          </p>
        ) : null}
        <form className='space-y-3' onSubmit={handleSave}>
          {mappings.map((mapping, index) => {
            const group = props.groups.find(
              (item) => item.id === mapping.source_id
            )
            const sourceValue = String(mapping.source_id)
            const targetValue = mapping.target

            return (
              <div
                key={rowKeys[index] ?? `${mapping.source_id}-${mapping.target}`}
                className='grid gap-3 rounded-xl border p-3 lg:grid-cols-[minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(14rem,1.1fr)_auto] lg:items-end'
              >
                <div className='space-y-2'>
                  <Label htmlFor={`sync-source-${index}`}>
                    {t('Source group')}
                  </Label>
                  <Select
                    value={sourceValue}
                    onValueChange={(value) =>
                      value &&
                      updateMapping(index, { source_id: Number(value) })
                    }
                  >
                    <SelectTrigger
                      id={`sync-source-${index}`}
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {!group ? (
                        <SelectItem value={sourceValue}>
                          {t('Missing group')} #{mapping.source_id}
                        </SelectItem>
                      ) : null}
                      {props.groups.map((item) => (
                        <SelectItem key={item.id} value={String(item.id)}>
                          {item.name} · {formatRatio(item.effective)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className='space-y-2'>
                  <Label htmlFor={`sync-target-${index}`}>
                    {t('Target group')}
                  </Label>
                  <Select
                    value={targetValue}
                    onValueChange={(value) =>
                      value && updateMapping(index, { target: value })
                    }
                  >
                    <SelectTrigger
                      id={`sync-target-${index}`}
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {!Object.hasOwn(props.targets, targetValue) ? (
                        <SelectItem value={targetValue}>
                          {t('Missing group')}: {targetValue}
                        </SelectItem>
                      ) : null}
                      {targetNames.map((target) => (
                        <SelectItem key={target} value={target}>
                          {target} · {formatRatio(props.targets[target])}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className='grid gap-2 sm:grid-cols-2'>
                  <div className='space-y-2'>
                    <Label htmlFor={`sync-mode-${index}`}>
                      {t('Calculation')}
                    </Label>
                    <Select
                      value={mapping.mode}
                      onValueChange={(value) =>
                        value &&
                        updateMapping(index, {
                          mode: value as Sub2APIRatioSyncMapping['mode'],
                        })
                      }
                    >
                      <SelectTrigger
                        id={`sync-mode-${index}`}
                        className='w-full'
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value='effective'>
                          {t('Effective ratio')}
                        </SelectItem>
                        <SelectItem value='default'>
                          {t('Default ratio')}
                        </SelectItem>
                        <SelectItem value='fixed'>
                          {t('Fixed ratio')}
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  <div className='space-y-2'>
                    <Label htmlFor={`sync-value-${index}`}>
                      {mapping.mode === 'fixed'
                        ? t('Fixed value')
                        : t('Multiplier')}
                    </Label>
                    <Input
                      id={`sync-value-${index}`}
                      type='number'
                      min={0}
                      step='any'
                      value={mapping.value}
                      onChange={(event) =>
                        updateMapping(index, {
                          value: Number(event.target.value),
                        })
                      }
                      aria-invalid={
                        mapping.value < 0 || !Number.isFinite(mapping.value)
                      }
                    />
                  </div>
                </div>

                <div className='flex items-center justify-between gap-3 lg:justify-end'>
                  <div className='flex items-center gap-2'>
                    <Switch
                      id={`sync-enabled-${index}`}
                      checked={mapping.enabled}
                      onCheckedChange={(enabled) =>
                        updateMapping(index, { enabled })
                      }
                      aria-label={t('Enable mapping')}
                    />
                    <Label htmlFor={`sync-enabled-${index}`}>
                      {t('Enabled')}
                    </Label>
                  </div>
                  <Button
                    type='button'
                    size='icon'
                    variant='ghost'
                    aria-label={t('Remove mapping')}
                    onClick={() => {
                      setMappings((current) =>
                        current.filter((_, rowIndex) => rowIndex !== index)
                      )
                      setRowKeys((current) =>
                        current.filter((_, rowIndex) => rowIndex !== index)
                      )
                    }}
                  >
                    <Trash2 aria-hidden='true' />
                  </Button>
                </div>

                {group?.peak && mapping.mode !== 'fixed' ? (
                  <div className='flex items-center gap-2 lg:col-span-4'>
                    <Switch
                      id={`sync-peak-${index}`}
                      checked={mapping.allow_peak}
                      onCheckedChange={(allow_peak) =>
                        updateMapping(index, { allow_peak })
                      }
                      aria-label={t('Allow base ratio during peak pricing')}
                    />
                    <Label htmlFor={`sync-peak-${index}`}>
                      {t(
                        'Allow syncing the base ratio when peak pricing is enabled'
                      )}
                    </Label>
                  </div>
                ) : null}
              </div>
            )
          })}
          {invalid ? (
            <p className='text-destructive text-sm' role='alert'>
              {t(
                'Check that each mapping is valid and target groups are unique.'
              )}
            </p>
          ) : null}
          <div className='flex justify-end border-t pt-3'>
            <Button type='submit' disabled={props.busy}>
              <Save aria-hidden='true' />
              {t('Save mappings')}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
