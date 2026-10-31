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
import { zodResolver } from '@hookform/resolvers/zod'
import { ExternalLink, KeyRound, LoaderCircle } from 'lucide-react'
import { useEffect, useState, type FormEvent } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import type { Sub2APIRatioSyncState } from '@/features/system-settings/models/sub2api-ratio-sync-api'

const connectionSchema = z.object({
  upstream_url: z
    .string()
    .url()
    .refine((value) => {
      let url: URL
      try {
        url = new URL(value)
      } catch {
        return false
      }

      if (
        url.username ||
        url.password ||
        url.search ||
        url.hash ||
        /[\s\\]/.test(value)
      ) {
        return false
      }
      if (url.protocol === 'https:') return true
      if (url.protocol !== 'http:') return false

      const hostname = url.hostname
        .toLowerCase()
        .replaceAll(/^\[|\]$/g, '')
        .replace(/\.$/, '')
      return (
        hostname === 'localhost' ||
        hostname.endsWith('.localhost') ||
        hostname === '::1' ||
        /^127(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}$/.test(hostname)
      )
    }),
  interval_seconds: z.number().int().min(60).max(86400),
})

export type ConnectionValues = z.infer<typeof connectionSchema>

type ConnectionCardProps = {
  state: Sub2APIRatioSyncState
  busy: boolean
  loginUrl: string
  helperBusy: boolean
  onSave: (values: ConnectionValues) => Promise<unknown>
  onImportTokens: (
    accessToken: string,
    refreshToken: string
  ) => Promise<unknown>
  onClearTokens: () => void
  onOpenLogin: () => void
  onGetHelper: () => Promise<string>
}

export function ConnectionCard(props: ConnectionCardProps) {
  const { t } = useTranslation()
  const [accessToken, setAccessToken] = useState('')
  const [refreshToken, setRefreshToken] = useState('')
  const [helperCode, setHelperCode] = useState('')
  const [helperOpen, setHelperOpen] = useState(false)
  const [clearOpen, setClearOpen] = useState(false)
  const form = useForm<ConnectionValues>({
    resolver: zodResolver(connectionSchema),
    defaultValues: {
      upstream_url: props.state.settings.upstream_url,
      interval_seconds: props.state.settings.interval_seconds || 60,
    },
  })

  useEffect(() => {
    form.reset({
      upstream_url: props.state.settings.upstream_url,
      interval_seconds: props.state.settings.interval_seconds || 60,
    })
  }, [
    form,
    props.state.settings.interval_seconds,
    props.state.settings.upstream_url,
  ])

  const handleImport = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    await props.onImportTokens(accessToken, refreshToken)
    setAccessToken('')
    setRefreshToken('')
  }

  const handleGetHelper = async () => {
    setHelperCode(await props.onGetHelper())
    setHelperOpen(true)
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>{t('Connection')}</CardTitle>
        </CardHeader>
        <CardContent className='space-y-6'>
          <form
            className='grid gap-4 md:grid-cols-[minmax(0,1fr)_12rem_auto] md:items-end'
            onSubmit={form.handleSubmit((values) => props.onSave(values))}
          >
            <div className='space-y-2'>
              <Label htmlFor='sub2api-upstream-url'>
                {t('Sub2API base URL')}
              </Label>
              <Input
                id='sub2api-upstream-url'
                type='url'
                autoComplete='url'
                placeholder='https://'
                aria-invalid={Boolean(form.formState.errors.upstream_url)}
                aria-describedby={
                  form.formState.errors.upstream_url
                    ? 'sub2api-upstream-url-hint sub2api-upstream-url-error'
                    : 'sub2api-upstream-url-hint'
                }
                {...form.register('upstream_url')}
              />
              <p
                id='sub2api-upstream-url-hint'
                className='text-muted-foreground text-xs'
              >
                {t(
                  'Remote URLs require HTTPS; HTTP is allowed only for localhost or loopback addresses.'
                )}
              </p>
              {form.formState.errors.upstream_url ? (
                <p
                  id='sub2api-upstream-url-error'
                  className='text-destructive text-xs'
                  role='alert'
                >
                  {t('Enter a valid HTTP or HTTPS URL.')}
                </p>
              ) : null}
            </div>
            <div className='space-y-2'>
              <Label htmlFor='sub2api-interval'>{t('Polling interval')}</Label>
              <Input
                id='sub2api-interval'
                type='number'
                min={60}
                max={86400}
                step={1}
                aria-invalid={Boolean(form.formState.errors.interval_seconds)}
                {...form.register('interval_seconds', { valueAsNumber: true })}
              />
              <p className='text-muted-foreground text-xs'>
                {t('Use a value from 60 to 86400 seconds.')}
              </p>
              {form.formState.errors.interval_seconds ? (
                <p className='text-destructive text-xs' role='alert'>
                  {t('Enter an interval from 60 to 86400 seconds.')}
                </p>
              ) : null}
            </div>
            <Button type='submit' disabled={props.busy}>
              {t('Save connection')}
            </Button>
          </form>

          <div className='flex flex-wrap items-center justify-between gap-3 border-t pt-4'>
            <div className='flex items-center gap-2 text-sm'>
              <KeyRound
                className={
                  props.state.auth.upstream
                    ? 'text-success size-4'
                    : 'text-muted-foreground size-4'
                }
                aria-hidden='true'
              />
              <span>
                {props.state.auth.upstream
                  ? t('Upstream session connected')
                  : t('No upstream session')}
              </span>
            </div>
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={props.onOpenLogin}
                disabled={props.busy || !props.state.settings.upstream_url}
              >
                <ExternalLink aria-hidden='true' />
                {t('Open upstream login')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => void handleGetHelper()}
                disabled={
                  props.helperBusy || !props.state.settings.upstream_url
                }
              >
                {props.helperBusy ? (
                  <LoaderCircle className='animate-spin' />
                ) : null}
                {t('Get login helper')}
              </Button>
              {props.state.auth.upstream ? (
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  onClick={() => setClearOpen(true)}
                  disabled={props.busy}
                >
                  {t('Clear session')}
                </Button>
              ) : null}
            </div>
          </div>

          <form className='grid gap-3 border-t pt-4' onSubmit={handleImport}>
            <div className='grid gap-3 md:grid-cols-2'>
              <div className='space-y-2'>
                <Label htmlFor='sub2api-access-token'>
                  {t('Access token')}
                </Label>
                <Input
                  id='sub2api-access-token'
                  type='password'
                  autoComplete='new-password'
                  value={accessToken}
                  onChange={(event) => setAccessToken(event.target.value)}
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='sub2api-refresh-token'>
                  {t('Refresh token')}
                </Label>
                <Input
                  id='sub2api-refresh-token'
                  type='password'
                  autoComplete='new-password'
                  value={refreshToken}
                  onChange={(event) => setRefreshToken(event.target.value)}
                />
              </div>
            </div>
            <div className='flex justify-end'>
              <Button
                type='submit'
                variant='secondary'
                disabled={
                  props.busy || (!accessToken.trim() && !refreshToken.trim())
                }
              >
                {t('Import session tokens')}
              </Button>
            </div>
          </form>

          {props.loginUrl ? (
            <a
              className='text-primary inline-flex text-sm underline underline-offset-4'
              href={props.loginUrl}
              target='_blank'
              rel='noreferrer'
            >
              {t('Open login page')}
            </a>
          ) : null}
        </CardContent>
      </Card>

      <Dialog
        open={helperOpen}
        onOpenChange={setHelperOpen}
        title={t('Login helper')}
        description={t(
          'Install this script in Tampermonkey to transfer the signed-in session.'
        )}
        contentClassName='sm:max-w-3xl'
        footer={
          <>
            <CopyButton value={helperCode} variant='outline' size='sm'>
              {t('Copy code')}
            </CopyButton>
            <Button variant='secondary' onClick={() => setHelperOpen(false)}>
              {t('Close')}
            </Button>
          </>
        }
      >
        <Textarea
          aria-label={t('Login helper code')}
          className={
            'min-h-[min(55vh,32rem)] font-mono text-xs' /* taste-skill-ignore: code is intentionally monospaced */
          }
          readOnly
          value={helperCode}
        />
      </Dialog>

      <ConfirmDialog
        open={clearOpen}
        onOpenChange={setClearOpen}
        title={t('Clear upstream session?')}
        desc={t(
          'Automatic monitoring will stop until a new session is connected.'
        )}
        destructive
        confirmText={t('Clear session')}
        handleConfirm={() => {
          props.onClearTokens()
          setClearOpen(false)
        }}
      />
    </>
  )
}
