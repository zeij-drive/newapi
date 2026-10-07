import { Plus, Trash2 } from 'lucide-react'
import { nanoid } from 'nanoid'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getEpayGateways,
  updateEpayGateways,
  type EpayGatewayView,
} from '../api'

type EditableGateway = EpayGatewayView & { key: string }

export function EpayGatewayEditor() {
  const { t } = useTranslation()
  const [gateways, setGateways] = useState<EditableGateway[]>([])
  const [loading, setLoading] = useState(true)
  const [loadFailed, setLoadFailed] = useState(false)
  const [reloadVersion, setReloadVersion] = useState(0)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    void getEpayGateways()
      .then((response) => {
        if (cancelled) return
        if (!response.success || !Array.isArray(response.data)) {
          setLoadFailed(true)
          handleServerError(response)
          return
        }
        setGateways(response.data.map((gateway) => ({ ...gateway, key: '' })))
      })
      .catch((error) => {
        if (cancelled) return
        setLoadFailed(true)
        handleServerError(error)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [reloadVersion])

  const update = (index: number, patch: Partial<EditableGateway>) => {
    setGateways((current) =>
      current.map((gateway, itemIndex) =>
        itemIndex === index ? { ...gateway, ...patch } : gateway
      )
    )
  }

  const save = async () => {
    setSaving(true)
    try {
      const response = await updateEpayGateways(gateways)
      if (!response.success || !Array.isArray(response.data)) {
        toast.error(response.message || t('Save failed'))
        return
      }
      setGateways(response.data.map((gateway) => ({ ...gateway, key: '' })))
      toast.success(t('Saved successfully'))
    } catch (error) {
      handleServerError(error)
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <LoadingState inline message={t('Loading...')} />
  }
  if (loadFailed) {
    return (
      <div role='alert'>
        <ErrorState
          title={t('Failed to load settings')}
          className='min-h-32'
          onRetry={() => {
            setLoading(true)
            setLoadFailed(false)
            setReloadVersion((version) => version + 1)
          }}
        />
      </div>
    )
  }

  return (
    <div className='space-y-4 rounded-lg border p-4'>
      <div className='flex items-center justify-between gap-3'>
        <div>
          <h4 className='font-medium'>{t('Multiple Epay gateways')}</h4>
          <p className='text-muted-foreground text-sm'>
            {t('Secret keys are encrypted and never shown again')}
          </p>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() =>
            setGateways((current) => [
              ...current,
              {
                id: nanoid(),
                name: '',
                address: '',
                merchant_id: '',
                enabled: true,
                key_set: false,
                key: '',
              },
            ])
          }
        >
          <Plus className='mr-1 h-4 w-4' />
          {t('Add gateway')}
        </Button>
      </div>
      {gateways.map((gateway, index) => (
        <div
          key={gateway.id}
          className='grid gap-3 rounded-md border p-3 md:grid-cols-2'
        >
          <Input
            aria-label={t('Gateway name')}
            placeholder={t('Gateway name')}
            value={gateway.name}
            onChange={(event) => update(index, { name: event.target.value })}
          />
          <Input
            aria-label={t('Epay endpoint')}
            placeholder={t('https://pay.example.com')}
            value={gateway.address}
            onChange={(event) => update(index, { address: event.target.value })}
          />
          <Input
            aria-label={t('Epay merchant ID')}
            placeholder={t('Epay merchant ID')}
            value={gateway.merchant_id}
            onChange={(event) =>
              update(index, { merchant_id: event.target.value })
            }
          />
          <Input
            aria-label={t('Epay secret key')}
            type='password'
            autoComplete='new-password'
            placeholder={
              gateway.key_set
                ? t('Leave blank to keep current key')
                : t('Epay secret key')
            }
            value={gateway.key}
            onChange={(event) => update(index, { key: event.target.value })}
          />
          <div className='flex items-center justify-between gap-3'>
            <label className='flex items-center gap-2 text-sm'>
              <Switch
                checked={gateway.enabled}
                onCheckedChange={(enabled) => update(index, { enabled })}
              />
              {t('Enabled')}
            </label>
            <Button
              type='button'
              variant='ghost'
              size='icon'
              aria-label={t('Delete gateway')}
              onClick={() =>
                setGateways((current) =>
                  current.filter((_, itemIndex) => itemIndex !== index)
                )
              }
            >
              <Trash2 className='h-4 w-4' />
            </Button>
          </div>
        </div>
      ))}
      <Button type='button' onClick={() => void save()} disabled={saving}>
        {saving ? t('Saving...') : t('Save gateways')}
      </Button>
    </div>
  )
}
