import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'

import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'

type RewardPeriod = 'daily' | 'weekly' | 'monthly' | 'yearly'
type RewardSettings = {
  enabled: boolean
  enabled_at: number
  daily: [string, string, string]
  weekly: [string, string, string]
  monthly: [string, string, string]
  yearly: [string, string, string]
}

const periods: Array<{ key: RewardPeriod; label: string }> = [
  { key: 'daily', label: 'Daily' },
  { key: 'weekly', label: 'Weekly' },
  { key: 'monthly', label: 'Monthly' },
  { key: 'yearly', label: 'Yearly' },
]
const ranks = [1, 2, 3] as const

export function UsageRankingRewardsSection() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<RewardSettings | null>(null)
  const [saving, setSaving] = useState(false)
  const query = useQuery({
    queryKey: ['usage-ranking-rewards'],
    queryFn: async () => {
      const response = await api.get<{
        success: boolean
        data: RewardSettings
      }>('/api/option/usage-ranking-rewards')
      if (!response.data.success) throw new Error('Failed to load settings')
      return response.data.data
    },
  })

  useEffect(() => {
    if (query.data) setSettings(query.data)
  }, [query.data])

  const updateAmount = (period: RewardPeriod, rank: number, value: string) => {
    setSettings((current) => {
      if (!current) return current
      const amounts = [...current[period]] as [string, string, string]
      amounts[rank] = value
      return { ...current, [period]: amounts }
    })
  }

  const save = async () => {
    if (!settings) return
    for (const period of periods) {
      for (const amount of settings[period.key]) {
        if (!/^\d+(\.\d{1,2})?$/.test(amount || '0')) {
          toast.error(t('Enter a valid USD amount'))
          return
        }
      }
    }
    setSaving(true)
    try {
      const response = await api.put<{
        success: boolean
        data: RewardSettings
        message?: string
      }>('/api/option/usage-ranking-rewards', settings)
      if (!response.data.success) {
        toast.error(response.data.message || t('Save failed'))
        return
      }
      setSettings(response.data.data)
      toast.success(t('Saved successfully'))
    } catch (error) {
      handleServerError(error)
    } finally {
      setSaving(false)
    }
  }

  let content: ReactNode
  if (query.isPending) {
    content = <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
  } else if (query.isError || !settings) {
    content = (
      <Button
        type='button'
        variant='outline'
        onClick={() => void query.refetch()}
      >
        {t('Retry')}
      </Button>
    )
  } else {
    content = (
      <div className='space-y-5'>
        <div className='flex items-center justify-between gap-4 border-b pb-4'>
          <div>
            <Label htmlFor='usage-ranking-rewards-enabled'>
              {t('Enable automatic ranking rewards')}
            </Label>
            <p className='text-muted-foreground text-xs'>
              {t('Rewards are sent after each completed period (UTC+8).')}
            </p>
          </div>
          <Switch
            id='usage-ranking-rewards-enabled'
            checked={settings.enabled}
            onCheckedChange={(enabled) =>
              setSettings((current) => current && { ...current, enabled })
            }
          />
        </div>
        <div className='overflow-x-auto'>
          <div className='grid min-w-[560px] grid-cols-[minmax(100px,1fr)_repeat(3,minmax(130px,1fr))] gap-3'>
            <span />
            {ranks.map((rank) => (
              <span key={rank} className='text-muted-foreground text-sm'>
                {t('Rank {{rank}}', { rank })} (USD)
              </span>
            ))}
            {periods.map((period) => (
              <div key={period.key} className='contents'>
                <span className='self-center text-sm font-medium'>
                  {t(period.label)}
                </span>
                {ranks.map((rank) => (
                  <Input
                    key={`${period.key}-${rank}`}
                    type='number'
                    min={0}
                    step='0.01'
                    value={settings[period.key][rank - 1]}
                    aria-label={`${t(period.label)} ${t('Rank {{rank}}', { rank })}`}
                    onChange={(event) =>
                      updateAmount(period.key, rank - 1, event.target.value)
                    }
                  />
                ))}
              </div>
            ))}
          </div>
        </div>
        <SettingsPageFormActions
          onSave={() => void save()}
          isSaving={saving}
          isSaveDisabled={saving}
        />
      </div>
    )
  }

  return (
    <SettingsSection title={t('Token leaderboard rewards')}>
      {content}
    </SettingsSection>
  )
}
