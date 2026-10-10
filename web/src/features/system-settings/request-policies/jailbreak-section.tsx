import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { ErrorState } from '@/components/error-state'
import { MultiSelect } from '@/components/multi-select'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { getGroups } from '@/features/users/api'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import type { JailbreakSettings } from './defaults'
import { useSavePolicy } from './use-save-policy'

export function JailbreakSection(props: { defaultValues: JailbreakSettings }) {
  const { t } = useTranslation()
  const save = useSavePolicy()
  const groups = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
  })
  const schema = z
    .object({
      JailbreakEnabled: z.boolean(),
      JailbreakAllowedGroups: z.array(z.string()),
      JailbreakChannelId: z
        .number()
        .int(t('Enter a positive integer'))
        .min(0, t('Select a detection channel')),
      JailbreakModel: z
        .string()
        .trim()
        .min(1, t('Model is required'))
        .max(200, t('Maximum length is {{count}} characters', { count: 200 })),
      JailbreakBanThreshold: z
        .number()
        .int(t('Enter a positive integer'))
        .min(
          1,
          t('Count must be between {{min}} and {{max}}', { min: 1, max: 100 })
        )
        .max(
          100,
          t('Count must be between {{min}} and {{max}}', { min: 1, max: 100 })
        ),
      JailbreakReplies: z.array(
        z
          .string()
          .trim()
          .min(1, t('Required'))
          .max(
            2000,
            t('Maximum length is {{count}} characters', { count: 2000 })
          )
      ),
    })
    .superRefine((values, context) => {
      if (values.JailbreakEnabled && values.JailbreakChannelId < 1) {
        context.addIssue({
          code: 'custom',
          path: ['JailbreakChannelId'],
          message: t('Select a detection channel'),
        })
      }
      if (values.JailbreakReplies.length !== values.JailbreakBanThreshold) {
        context.addIssue({
          code: 'custom',
          path: ['JailbreakBanThreshold'],
          message: t('Provide one reply for each interception'),
        })
      }
    })
  const form = useForm<JailbreakSettings>({
    resolver: zodResolver(schema),
    defaultValues: props.defaultValues,
  })
  useEffect(() => {
    form.reset(props.defaultValues)
  }, [props.defaultValues, form])
  const threshold = form.watch('JailbreakBanThreshold')
  const replyCount = Number.isInteger(threshold)
    ? Math.min(100, Math.max(0, threshold))
    : 0
  const enabled = form.watch('JailbreakEnabled')
  const onSubmit = async (values: JailbreakSettings) => {
    try {
      await save.mutateAsync(
        Object.fromEntries(
          Object.entries(values).map(([key, value]) => [
            key,
            Array.isArray(value) ? JSON.stringify(value) : String(value),
          ])
        )
      )
    } catch (error) {
      handleServerError(error)
    }
  }
  const groupOptions = [
    ...new Set([
      ...(groups.data?.data ?? []),
      ...form.watch('JailbreakAllowedGroups'),
    ]),
  ]
    .filter((group) => group !== 'auto')
    .map((group) => ({ label: group, value: group }))

  return (
    <SettingsSection title={t('Jailbreak protection')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={form.formState.isSubmitting}
            saveLabel='Save jailbreak settings'
          />
          <FormField
            control={form.control}
            name='JailbreakEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable jailbreak detection')}</FormLabel>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
          <FormField
            control={form.control}
            name='JailbreakAllowedGroups'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Allowed groups')}</FormLabel>
                <FormControl>
                  <MultiSelect
                    options={groupOptions}
                    selected={field.value}
                    onChange={field.onChange}
                    allowCreate={false}
                    disabled={groups.isPending || groups.isError}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Only these groups may send jailbreak prompts. All other groups are checked.'
                  )}
                </FormDescription>
                <FormMessage />
                {groups.isError && (
                  <ErrorState
                    title={t('Failed to load groups')}
                    onRetry={() => {
                      void groups.refetch()
                    }}
                    className='min-h-0 py-4'
                  />
                )}
              </FormItem>
            )}
          />
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='JailbreakChannelId'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Detection channel ID')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      step={1}
                      {...field}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Use an OpenAI channel connected to Qwen3Guard. Its key stays in channel settings.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='JailbreakModel'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Detection model')}</FormLabel>
                  <FormControl>
                    <Input {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='JailbreakBanThreshold'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Ban after interceptions')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={100}
                      step={1}
                      {...field}
                      onChange={(event) => {
                        const next = Number(event.target.value)
                        field.onChange(next)
                        if (
                          Number.isInteger(next) &&
                          next >= 1 &&
                          next <= 100
                        ) {
                          const existing = form.getValues('JailbreakReplies')
                          form.setValue(
                            'JailbreakReplies',
                            Array.from(
                              { length: next },
                              (_, index) =>
                                existing[index] ??
                                t('Jailbreak request blocked.')
                            ),
                            { shouldDirty: true }
                          )
                        }
                      }}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Counts are shared across API keys. Re-enabling a banned user resets the count. Root accounts are never automatically banned.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsFormGrid>
          <div className='grid gap-5'>
            {Array.from({ length: replyCount }, (_, index) => (
              <FormField
                key={index}
                control={form.control}
                name={`JailbreakReplies.${index}`}
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Reply for interception {{count}}', {
                        count: index + 1,
                      })}
                    </FormLabel>
                    <FormControl>
                      <Textarea rows={2} maxLength={2000} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            ))}
          </div>
          {enabled && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Detection failures stop the request without adding a strike. Text is checked before billing; images and audio are not scanned. Realtime requires an allowed group.'
              )}
            </p>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
