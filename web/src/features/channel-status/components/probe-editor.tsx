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
import { RefreshCw } from 'lucide-react'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import type { ChannelStatusProbe, ChannelStatusProbeData } from '../api'
import {
  DEFAULT_PROBE,
  probeFormSchema,
  type ProbeFormValues,
} from '../lib/probe-form'

export function ProbeEditor(props: {
  open: boolean
  probe?: ChannelStatusProbe
  channels: ChannelStatusProbeData['channels']
  pending: boolean
  onOpenChange: (open: boolean) => void
  onSave: (probe: ChannelStatusProbe) => void
}) {
  const { t } = useTranslation()
  const form = useForm<ProbeFormValues>({
    resolver: zodResolver(probeFormSchema(t)),
    defaultValues: DEFAULT_PROBE,
  })
  const selectedChannelId = form.watch('channel_id')
  const selectedChannel = props.channels.find(
    (channel) => channel.id === selectedChannelId
  )
  const models = selectedChannel?.models ?? []

  useEffect(() => {
    if (props.open) {
      form.reset(
        props.probe ? { ...DEFAULT_PROBE, ...props.probe } : DEFAULT_PROBE
      )
    }
  }, [form, props.open, props.probe])

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.probe ? t('Edit probe') : t('Add probe')}
      description={t(
        'A probe periodically sends a small request to verify a channel.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <Button
          type='submit'
          form='channel-probe-form'
          disabled={props.pending}
        >
          {props.pending ? <RefreshCw className='animate-spin' /> : null}
          {t('Save probe')}
        </Button>
      }
    >
      <Form {...form}>
        <form
          id='channel-probe-form'
          className='grid gap-4 sm:grid-cols-2'
          onSubmit={(event) => void form.handleSubmit(props.onSave)(event)}
        >
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem className='sm:col-span-2'>
                <FormLabel>{t('Name')}</FormLabel>
                <FormControl>
                  <Input maxLength={80} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='channel_id'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Channel')}</FormLabel>
                <FormControl>
                  <NativeSelect
                    className='w-full'
                    value={field.value || ''}
                    onChange={(event) => {
                      field.onChange(Number(event.target.value))
                      form.setValue('model', '')
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('Select a channel')}
                    </NativeSelectOption>
                    {props.channels.map((channel) => (
                      <NativeSelectOption key={channel.id} value={channel.id}>
                        {channel.name} ({channel.group || t('Default')})
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='model'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Model')}</FormLabel>
                <FormControl>
                  {models.length > 0 ? (
                    <NativeSelect className='w-full' {...field}>
                      <NativeSelectOption value=''>
                        {t('Select a model')}
                      </NativeSelectOption>
                      {models.map((model) => (
                        <NativeSelectOption key={model} value={model}>
                          {model}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  ) : (
                    <Input maxLength={200} {...field} />
                  )}
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='interval_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Interval (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={60}
                    max={86400}
                    {...field}
                    onChange={(event) =>
                      field.onChange(Number(event.target.value))
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='timeout_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Timeout (seconds)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={5}
                    max={300}
                    {...field}
                    onChange={(event) =>
                      field.onChange(Number(event.target.value))
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='endpoint_type'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Endpoint Type')}</FormLabel>
                <FormControl>
                  <NativeSelect className='w-full' {...field}>
                    <NativeSelectOption value=''>
                      {t('Auto detect (default)')}
                    </NativeSelectOption>
                    {[
                      'openai',
                      'openai-response',
                      'openai-response-compact',
                      'anthropic',
                      'gemini',
                      'embeddings',
                      'jina-rerank',
                      'image-generation',
                    ].map((endpoint) => (
                      <NativeSelectOption key={endpoint} value={endpoint}>
                        {endpoint}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='is_stream'
            render={({ field }) => (
              <FormItem className='flex items-center gap-2'>
                <FormControl>
                  <Switch
                    checked={field.value ?? false}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormLabel className='cursor-pointer'>
                  {t('Stream Mode')}
                </FormLabel>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex items-center gap-2 sm:col-span-2'>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormLabel className='cursor-pointer'>
                  {t('Probe enabled')}
                </FormLabel>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='prompt'
            render={({ field }) => (
              <FormItem className='sm:col-span-2'>
                <FormLabel>{t('Prompt')}</FormLabel>
                <FormControl>
                  <Textarea maxLength={2000} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </form>
      </Form>
    </Dialog>
  )
}
