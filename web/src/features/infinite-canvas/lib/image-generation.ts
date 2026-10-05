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

export type ImageGenerationConfig = {
  baseUrl: string
  model: string
}

const CONFIG_VERSION = 'v1'
const MAX_RESPONSE_BYTES = 20 * 1024 * 1024

export type ImageGenerationErrorCode =
  | 'invalid-url'
  | 'invalid-input'
  | 'network'
  | 'api-rejected'
  | 'invalid-response'
  | 'no-image'
  | 'download'
  | 'too-large'

export class ImageGenerationError extends Error {
  constructor(
    readonly code: ImageGenerationErrorCode,
    readonly status?: number
  ) {
    super(code)
    this.name = 'ImageGenerationError'
  }
}

function configKey(userId: number): string {
  return `new-api-infinite-canvas:image-generation:${CONFIG_VERSION}:${userId}`
}

export function loadImageGenerationConfig(
  userId: number
): ImageGenerationConfig {
  const fallback: ImageGenerationConfig = {
    baseUrl: 'https://api.openai.com/v1',
    model: 'gpt-image-1',
  }
  if (!Number.isFinite(userId) || typeof window === 'undefined') return fallback
  try {
    const raw = window.localStorage.getItem(configKey(userId))
    if (!raw) return fallback
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object') return fallback
    const baseUrl = 'baseUrl' in parsed ? parsed.baseUrl : undefined
    const model = 'model' in parsed ? parsed.model : undefined
    if (
      typeof baseUrl !== 'string' ||
      baseUrl.length > 2048 ||
      typeof model !== 'string' ||
      model.length > 200
    ) {
      return fallback
    }
    return { baseUrl, model }
  } catch {
    return fallback
  }
}

export function saveImageGenerationConfig(
  userId: number,
  config: ImageGenerationConfig
): boolean {
  if (!Number.isFinite(userId) || typeof window === 'undefined') return false
  try {
    window.localStorage.setItem(
      configKey(userId),
      JSON.stringify({ baseUrl: config.baseUrl, model: config.model })
    )
    return true
  } catch {
    return false
  }
}

export function getImageGenerationEndpoint(baseUrl: string): string {
  let url: URL
  try {
    url = new URL(baseUrl.trim())
  } catch {
    throw new ImageGenerationError('invalid-url')
  }
  if (
    (url.protocol !== 'https:' &&
      !(
        url.protocol === 'http:' &&
        ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
      )) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    baseUrl.length > 2048
  ) {
    throw new ImageGenerationError('invalid-url')
  }
  const path = url.pathname.replace(/\/+$/, '')
  url.pathname = path
  if (!path.endsWith('/images/generations')) {
    url.pathname = `${path || '/v1'}/images/generations`
  }
  return url.toString()
}

function decodeBase64(value: string): File {
  if (value.length > Math.ceil(MAX_RESPONSE_BYTES / 3) * 4 + 100) {
    throw new ImageGenerationError('too-large')
  }
  const comma = value.indexOf(',')
  const encoded =
    value.startsWith('data:') && comma >= 0 ? value.slice(comma + 1) : value
  let binary: string
  try {
    binary = atob(encoded)
  } catch {
    throw new ImageGenerationError('invalid-response')
  }
  if (binary.length > MAX_RESPONSE_BYTES) {
    throw new ImageGenerationError('too-large')
  }
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  let mimeType = 'image/png'
  let extension = 'png'
  if (bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) {
    mimeType = 'image/jpeg'
    extension = 'jpg'
  } else if (binary.startsWith('RIFF') && binary.slice(8, 12) === 'WEBP') {
    mimeType = 'image/webp'
    extension = 'webp'
  } else if (binary.startsWith('GIF87a') || binary.startsWith('GIF89a')) {
    mimeType = 'image/gif'
    extension = 'gif'
  } else if (!(bytes[0] === 0x89 && binary.slice(1, 4) === 'PNG')) {
    throw new ImageGenerationError('invalid-response')
  }
  return new File([bytes], `generated-image.${extension}`, { type: mimeType })
}

function extensionForMime(mimeType: string): string {
  const subtype = mimeType.split('/')[1]?.split(';')[0]
  return subtype && /^[a-z0-9.+-]+$/i.test(subtype) ? subtype : 'png'
}

async function readImageResponse(
  response: Response,
  maxBytes: number,
  signal?: AbortSignal
): Promise<Uint8Array> {
  const declaredSize = Number(response.headers.get('content-length') ?? 0)
  if (declaredSize > maxBytes) {
    await response.body?.cancel()
    throw new ImageGenerationError('too-large')
  }
  if (!response.body) throw new ImageGenerationError('invalid-response')
  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let received = 0
  try {
    while (true) {
      signal?.throwIfAborted()
      const chunk = await reader.read()
      if (chunk.done) break
      received += chunk.value.byteLength
      if (received > maxBytes) {
        throw new ImageGenerationError('too-large')
      }
      chunks.push(chunk.value)
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined)
    throw error
  } finally {
    reader.releaseLock()
  }
  const bytes = new Uint8Array(received)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return bytes
}

export async function generateImageFile(
  config: ImageGenerationConfig & { apiKey: string; prompt: string },
  signal?: AbortSignal
): Promise<File> {
  const apiKey = config.apiKey.trim()
  const prompt = config.prompt.trim()
  if (
    !apiKey ||
    apiKey.length > 8192 ||
    /[\r\n]/.test(apiKey) ||
    !prompt ||
    prompt.length > 10000 ||
    !config.model.trim() ||
    config.model.trim().length > 200
  ) {
    throw new ImageGenerationError('invalid-input')
  }
  const endpoint = getImageGenerationEndpoint(config.baseUrl)
  signal?.throwIfAborted()

  let response: Response
  try {
    response = await fetch(endpoint, {
      method: 'POST',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      cache: 'no-store',
      signal,
      headers: {
        Authorization: `Bearer ${apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ model: config.model.trim(), prompt }),
    })
  } catch (error) {
    signal?.throwIfAborted()
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error
    }
    throw new ImageGenerationError('network')
  }
  signal?.throwIfAborted()

  if (!response.ok) {
    await response.body?.cancel()
    throw new ImageGenerationError('api-rejected', response.status)
  }

  let payload: unknown
  try {
    const bytes = await readImageResponse(
      response,
      Math.ceil(MAX_RESPONSE_BYTES / 3) * 4 + 1024,
      signal
    )
    const body = new TextDecoder().decode(bytes)
    payload = JSON.parse(body)
    signal?.throwIfAborted()
  } catch (error) {
    signal?.throwIfAborted()
    if (error instanceof ImageGenerationError) throw error
    throw new ImageGenerationError('invalid-response')
  }

  const data =
    payload && typeof payload === 'object' && 'data' in payload
      ? payload.data
      : null
  if (!Array.isArray(data)) throw new ImageGenerationError('no-image')
  const image: unknown = data[0]
  if (
    image &&
    typeof image === 'object' &&
    'b64_json' in image &&
    typeof image.b64_json === 'string' &&
    image.b64_json
  ) {
    return decodeBase64(image.b64_json)
  }
  const remoteUrl =
    data[0] &&
    typeof data[0] === 'object' &&
    'url' in data[0] &&
    typeof data[0].url === 'string'
      ? data[0].url
      : null
  if (!remoteUrl) throw new ImageGenerationError('no-image')
  let imageResponse: Response
  try {
    const imageUrl = new URL(remoteUrl)
    if (
      (imageUrl.protocol !== 'https:' &&
        !(
          imageUrl.protocol === 'http:' &&
          ['localhost', '127.0.0.1', '[::1]'].includes(imageUrl.hostname)
        )) ||
      imageUrl.username ||
      imageUrl.password
    ) {
      throw new ImageGenerationError('download')
    }
    imageResponse = await fetch(imageUrl.toString(), {
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      signal,
    })
  } catch (error) {
    signal?.throwIfAborted()
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw error
    }
    throw new ImageGenerationError('download')
  }
  if (!imageResponse.ok) {
    await imageResponse.body?.cancel()
    throw new ImageGenerationError('download')
  }
  const mimeType =
    imageResponse.headers
      .get('content-type')
      ?.split(';')[0]
      ?.trim()
      .toLowerCase() ?? ''
  if (
    !['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(mimeType)
  ) {
    await imageResponse.body?.cancel()
    throw new ImageGenerationError('invalid-response')
  }
  const bytes = await readImageResponse(
    imageResponse,
    MAX_RESPONSE_BYTES,
    signal
  )
  const blob = new Blob([bytes as Uint8Array<ArrayBuffer>], { type: mimeType })
  signal?.throwIfAborted()
  if (blob.size > MAX_RESPONSE_BYTES) {
    throw new ImageGenerationError('too-large')
  }
  if (
    !['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(blob.type)
  ) {
    throw new ImageGenerationError('invalid-response')
  }
  return new File([blob], `generated-image.${extensionForMime(blob.type)}`, {
    type: blob.type,
  })
}
