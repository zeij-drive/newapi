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
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
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
import { Textarea } from '@/components/ui/textarea'

import {
  generateImageFile,
  ImageGenerationError,
  loadImageGenerationConfig,
  saveImageGenerationConfig,
} from '../lib/image-generation'
import {
  createImageGenerationSchema,
  type ImageGenerationValues,
} from '../lib/image-generation-schema'

export function ImageGenerationDialog(props: {
  userId: number
  open: boolean
  onOpenChange: (open: boolean) => void
  onGenerated: (file: File, signal: AbortSignal) => Promise<boolean>
}) {
  const { t } = useTranslation()
  const [initialConfig] = useState(() =>
    loadImageGenerationConfig(props.userId)
  )
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [configSaved, setConfigSaved] = useState(true)
  const [generatedFile, setGeneratedFile] = useState<File | null>(null)
  const activeRequest = useRef<AbortController | null>(null)
  const form = useForm<ImageGenerationValues>({
    resolver: zodResolver(createImageGenerationSchema(t)),
    defaultValues: { ...initialConfig, apiKey: '', prompt: '' },
  })

  useEffect(
    () => () => {
      activeRequest.current?.abort()
      activeRequest.current = null
    },
    []
  )

  useEffect(() => {
    if (props.open) return
    activeRequest.current?.abort()
    activeRequest.current = null
    setPending(false)
    setError(null)
    setGeneratedFile(null)
  }, [props.open])

  const changeOpen = (open: boolean) => {
    if (!open) {
      activeRequest.current?.abort()
      activeRequest.current = null
      setPending(false)
      setError(null)
      setGeneratedFile(null)
    }
    props.onOpenChange(open)
  }

  const submit = async (values: ImageGenerationValues) => {
    if (activeRequest.current) return
    const controller = new AbortController()
    activeRequest.current = controller
    setPending(true)
    setError(null)
    setConfigSaved(
      saveImageGenerationConfig(props.userId, {
        baseUrl: values.baseUrl,
        model: values.model,
      })
    )
    let timedOut = false
    const timeout = window.setTimeout(() => {
      timedOut = true
      controller.abort()
    }, 180000)
    try {
      const file = await generateImageFile(values, controller.signal)
      controller.signal.throwIfAborted()
      setGeneratedFile(file)
      const saved = await props.onGenerated(file, controller.signal)
      controller.signal.throwIfAborted()
      if (!saved) {
        setError(t('Image could not be saved in this browser.'))
        return
      }
      setGeneratedFile(null)
      props.onOpenChange(false)
    } catch (caught) {
      if (controller.signal.aborted) {
        if (timedOut && activeRequest.current === controller) {
          setError(t('Image generation timed out. Please try again.'))
        }
        return
      }
      if (!(caught instanceof ImageGenerationError)) {
        setError(t('Image generation failed. Please try again.'))
        return
      }
      switch (caught.code) {
        case 'network':
          setError(
            t(
              'Cannot reach the image API. Check the URL, network and API CORS settings.'
            )
          )
          break
        case 'api-rejected':
          setError(
            t(
              'Image API rejected the request (HTTP {{status}}). Check your API key and model.',
              { status: caught.status }
            )
          )
          break
        case 'no-image':
          setError(
            t(
              'The API returned no image. Use a compatible image generation model.'
            )
          )
          break
        case 'download':
          setError(
            t(
              'Cannot download the generated image. The image URL must allow browser access.'
            )
          )
          break
        case 'too-large':
          setError(t('Generated image exceeds the 20 MB local limit.'))
          break
        default:
          setError(t('Image API returned an invalid image response.'))
      }
    } finally {
      window.clearTimeout(timeout)
      if (activeRequest.current === controller) {
        activeRequest.current = null
        setPending(false)
      }
    }
  }

  const retrySave = async () => {
    if (!generatedFile || pending) return
    const controller = new AbortController()
    activeRequest.current = controller
    setPending(true)
    setError(null)
    try {
      const saved = await props.onGenerated(generatedFile, controller.signal)
      controller.signal.throwIfAborted()
      if (!saved) {
        setError(t('Image could not be saved in this browser.'))
        return
      }
      setGeneratedFile(null)
      props.onOpenChange(false)
    } catch {
      if (!controller.signal.aborted) {
        setError(t('Image could not be saved in this browser.'))
      }
    } finally {
      if (activeRequest.current === controller) {
        activeRequest.current = null
        setPending(false)
      }
    }
  }

  const downloadGeneratedFile = () => {
    if (!generatedFile) return
    const url = URL.createObjectURL(generatedFile)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = generatedFile.name
    anchor.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={changeOpen}
      title={t('Generate image')}
      description={t('Use your own OpenAI-compatible image API.')}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button variant='outline' onClick={() => changeOpen(false)}>
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='canvas-image-generation-form'
            disabled={pending || generatedFile !== null}
          >
            {t('Generate image')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='canvas-image-generation-form'
          className='grid gap-4'
          onSubmit={(event) => void form.handleSubmit(submit)(event)}
        >
          <FormField
            control={form.control}
            name='baseUrl'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API base URL')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='url'
                    maxLength={2048}
                    disabled={pending}
                    placeholder='https://api.example.com/v1'
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'The browser connects directly to this API. The provider must allow CORS.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='apiKey'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API key')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='password'
                    maxLength={8192}
                    autoComplete='off'
                    disabled={pending}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'The API key stays in memory and is cleared when you leave this page.'
                  )}
                </FormDescription>
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
                  <Input
                    {...field}
                    maxLength={200}
                    disabled={pending}
                    placeholder='gpt-image-1'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='prompt'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Prompt')}</FormLabel>
                <FormControl>
                  <Textarea
                    {...field}
                    maxLength={10000}
                    disabled={pending}
                    className='min-h-28'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          {!configSaved && (
            <p role='status' className='text-muted-foreground text-xs'>
              {t(
                'API settings could not be saved locally. They remain available for this session.'
              )}
            </p>
          )}
          {pending && (
            <div role='status' aria-live='polite'>
              <LoadingState
                message={
                  generatedFile
                    ? t('Saving locally...')
                    : t('Generating image...')
                }
                size='sm'
                className='min-h-0 py-3'
              />
            </div>
          )}
          {error && (
            <div role='alert'>
              <ErrorState
                title={t('Image generation failed')}
                description={error}
                className='min-h-0 py-3'
                action={
                  generatedFile ? (
                    <div className='flex flex-wrap justify-center gap-2'>
                      <Button
                        type='button'
                        size='sm'
                        onClick={() => void retrySave()}
                        disabled={pending}
                      >
                        {t('Retry saving to canvas')}
                      </Button>
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        onClick={downloadGeneratedFile}
                      >
                        {t('Download image')}
                      </Button>
                    </div>
                  ) : undefined
                }
              />
            </div>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
