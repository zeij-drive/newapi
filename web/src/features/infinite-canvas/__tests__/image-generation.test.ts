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
import { Buffer } from 'node:buffer'

import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import {
  generateImageFile,
  getImageGenerationEndpoint,
  loadImageGenerationConfig,
  saveImageGenerationConfig,
} from '../lib/image-generation'

const pngBase64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a8WQAAAAASUVORK5CYII='
const config = {
  baseUrl: 'https://images.example.com/v1',
  model: 'image-model',
  apiKey: 'provider-only-key',
  prompt: 'A small island',
}
let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>

beforeEach(() => {
  window.localStorage.clear()
  fetchMock = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetchMock)
  useAuthStore.setState((state) => ({
    auth: { ...state.auth, accessToken: 'dashboard-session-token' },
  }))
})

afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

it.each([
  [
    'https://images.example.com',
    'https://images.example.com/v1/images/generations',
  ],
  [
    'https://images.example.com/v1/',
    'https://images.example.com/v1/images/generations',
  ],
  [
    'https://images.example.com/proxy/v1',
    'https://images.example.com/proxy/v1/images/generations',
  ],
  [
    'https://images.example.com/v1/images/generations',
    'https://images.example.com/v1/images/generations',
  ],
  ['http://localhost:3000/v1', 'http://localhost:3000/v1/images/generations'],
])('normalizes the configured API URL %s', (url, expected) => {
  expect(getImageGenerationEndpoint(url)).toBe(expected)
})

it.each([
  'javascript:alert(1)',
  'http://images.example.com/v1',
  'https://user:key@images.example.com/v1',
  'https://images.example.com/v1?api_key=secret',
  'https://images.example.com/v1#secret',
])('rejects a base URL that could expose credentials: %s', (url) => {
  expect(() => getImageGenerationEndpoint(url)).toThrow('invalid-url')
})

it('sends only the supplied provider key without dashboard credentials and decodes base64 to a File', async () => {
  fetchMock.mockResolvedValue(
    new Response(JSON.stringify({ data: [{ b64_json: pngBase64 }] }))
  )

  const file = await generateImageFile({
    ...config,
    apiKey: ` ${config.apiKey} `,
  })

  expect(fetchMock).toHaveBeenCalledWith(
    'https://images.example.com/v1/images/generations',
    expect.objectContaining({
      method: 'POST',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      cache: 'no-store',
      headers: {
        Authorization: 'Bearer provider-only-key',
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ model: 'image-model', prompt: 'A small island' }),
    })
  )
  expect(file).toBeInstanceOf(File)
  expect(file.name).toBe('generated-image.png')
  expect(file.type).toBe('image/png')
  expect(file.size).toBe(atob(pngBase64).length)
})

it('rejects a base64 payload that exceeds the 20 MB decoded image limit', async () => {
  const bytes = new Uint8Array(20 * 1024 * 1024 + 1)
  bytes.set([0x89, 0x50, 0x4e, 0x47])
  fetchMock.mockResolvedValue(
    new Response(
      JSON.stringify({
        data: [{ b64_json: Buffer.from(bytes).toString('base64') }],
      })
    )
  )

  await expect(generateImageFile(config)).rejects.toMatchObject({
    code: 'too-large',
  })
})

it('downloads a returned image URL without forwarding its provider key', async () => {
  const jpeg = new Blob([new Uint8Array([0xff, 0xd8, 0xff, 0xd9])], {
    type: 'image/jpeg',
  })
  fetchMock
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({ data: [{ url: 'https://cdn.example.com/image.jpg' }] })
      )
    )
    .mockResolvedValueOnce(
      new Response(new Uint8Array([0xff, 0xd8, 0xff, 0xd9]), {
        headers: { 'Content-Type': 'image/jpeg' },
      })
    )

  const file = await generateImageFile(config)

  expect(fetchMock.mock.calls[1]).toEqual([
    'https://cdn.example.com/image.jpg',
    {
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      signal: undefined,
    },
  ])
  expect(file.name).toBe('generated-image.jpeg')
  expect(file.type).toBe('image/jpeg')
  expect(file.size).toBe(jpeg.size)
})

it('reports an API error without including response text or the provider key', async () => {
  fetchMock.mockResolvedValue(
    new Response(JSON.stringify({ error: { message: config.apiKey } }), {
      status: 401,
    })
  )

  await expect(generateImageFile(config)).rejects.toMatchObject({
    code: 'api-rejected',
    status: 401,
    message: 'api-rejected',
  })
})

it('cancels a downloaded image stream that exceeds 20 MB without a size header', async () => {
  const cancel = vi.fn()
  const imageStream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new Uint8Array(20 * 1024 * 1024 + 1))
    },
    cancel,
  })
  fetchMock
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({ data: [{ url: 'https://cdn.example.com/image.png' }] })
      )
    )
    .mockResolvedValueOnce(
      new Response(imageStream, { headers: { 'Content-Type': 'image/png' } })
    )

  await expect(generateImageFile(config)).rejects.toMatchObject({
    code: 'too-large',
  })

  expect(cancel).toHaveBeenCalledTimes(1)
})

it.each([
  [{ data: [] }, 'no-image'],
  [{ data: [{ b64_json: 'not a valid image' }] }, 'invalid-response'],
  [
    { data: [{ url: 'https://user:key@cdn.example.com/image.png' }] },
    'download',
  ],
])('rejects an unusable image response %j', async (payload, code) => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify(payload)))
  await expect(generateImageFile(config)).rejects.toMatchObject({ code })
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

it('propagates cancellation of a pending provider request', async () => {
  const controller = new AbortController()
  let requestStarted: () => void = () => undefined
  const started = new Promise<void>((resolve) => {
    requestStarted = resolve
  })
  fetchMock.mockImplementation(
    (_url, request) =>
      new Promise((_resolve, reject) => {
        request?.signal?.addEventListener('abort', () =>
          reject(new DOMException('Cancelled', 'AbortError'))
        )
        requestStarted()
      })
  )

  const generating = generateImageFile(config, controller.signal)
  const rejected = expect(generating).rejects.toMatchObject({
    name: 'AbortError',
  })
  await started
  controller.abort()

  await rejected
})

it('persists only endpoint and model per account while keeping API keys and prompts out of local storage', () => {
  expect(saveImageGenerationConfig(101, config)).toBe(true)
  expect(loadImageGenerationConfig(101)).toEqual({
    baseUrl: config.baseUrl,
    model: config.model,
  })
  expect(loadImageGenerationConfig(202)).toEqual({
    baseUrl: 'https://api.openai.com/v1',
    model: 'gpt-image-1',
  })
  const key = window.localStorage.key(0)
  if (!key) throw new Error('Image generation settings were not saved')
  expect(window.localStorage.getItem(key)).toBe(
    JSON.stringify({ baseUrl: config.baseUrl, model: config.model })
  )
})
