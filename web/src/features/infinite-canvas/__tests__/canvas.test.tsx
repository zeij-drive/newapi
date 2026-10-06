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
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  createElement,
  useEffect,
  type ComponentType,
  type ReactNode,
} from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { InfiniteCanvas } from '../index'
import {
  clearCanvasImages,
  loadCanvasImage,
  saveCanvasImage,
} from '../lib/assets'

const generatedPng =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a8WQAAAAASUVORK5CYII='

type MockNode = {
  id: string
  type: string
  data: Record<string, unknown>
}

type MockCanvasProps = {
  nodes: MockNode[]
  nodeTypes: Record<string, ComponentType<Record<string, unknown>>>
  onInit?: (flow: {
    screenToFlowPosition: () => { x: number; y: number }
  }) => void
  children?: ReactNode
}

function deferred<T>() {
  let resolvePromise: (value: T) => void = () => undefined
  const promise = new Promise<T>((resolve) => {
    resolvePromise = resolve
  })
  return { promise, resolve: resolvePromise }
}

const imageAssets = vi.hoisted(() => ({
  blobs: new Map<string, Blob>(),
  save: vi.fn(),
  load: vi.fn(),
  remove: vi.fn(),
  clear: vi.fn(),
}))

// IndexedDB transaction semantics are checked in browser QA. These tests keep
// the asset boundary deterministic while exercising metadata and UI behavior.
vi.mock('../lib/assets', () => ({
  saveCanvasImage: imageAssets.save,
  loadCanvasImage: imageAssets.load,
  deleteCanvasImage: imageAssets.remove,
  clearCanvasImages: imageAssets.clear,
}))

// jsdom cannot lay out React Flow. Keep its initialization and note rendering
// boundary small; the actual page, editor inputs, store and dialog remain real.
vi.mock('@/components/ai-elements/canvas', () => ({
  Canvas: (props: MockCanvasProps) => {
    const onInit = props.onInit
    useEffect(() => {
      onInit?.({ screenToFlowPosition: () => ({ x: 400, y: 300 }) })
    }, [onInit])
    return createElement(
      'div',
      { 'data-testid': 'canvas' },
      props.nodes.map((node) => {
        const NodeComponent = props.nodeTypes[node.type]
        return createElement(NodeComponent, {
          key: node.id,
          id: node.id,
          data: node.data,
          type: node.type,
          selected: false,
        })
      }),
      props.children
    )
  },
}))

vi.mock('@/components/ai-elements/node', () => ({
  Node: (props: { children?: ReactNode }) =>
    createElement('div', { 'data-testid': 'note-node' }, props.children),
  NodeContent: (props: { children?: ReactNode }) =>
    createElement('div', undefined, props.children),
  NodeHeader: (props: { children?: ReactNode }) =>
    createElement('div', undefined, props.children),
  NodeTitle: (props: { children?: ReactNode }) =>
    createElement('div', undefined, props.children),
}))

vi.mock('@/components/ai-elements/panel', () => ({
  Panel: (props: { children?: ReactNode }) =>
    createElement('div', undefined, props.children),
}))

vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>()
  return {
    ...actual,
    MiniMap: () => null,
  }
})

beforeEach(() => {
  window.localStorage.clear()
  imageAssets.blobs.clear()
  imageAssets.save
    .mockReset()
    .mockImplementation(async (userId, assetId, blob) => {
      imageAssets.blobs.set(`${userId}:${assetId}`, blob)
      return true
    })
  imageAssets.load.mockReset().mockImplementation(async (userId, assetId) => {
    return imageAssets.blobs.get(`${userId}:${assetId}`) ?? null
  })
  imageAssets.remove.mockReset().mockImplementation(async (userId, assetId) => {
    imageAssets.blobs.delete(`${userId}:${assetId}`)
    return true
  })
  imageAssets.clear
    .mockReset()
    .mockImplementation(async (userId: number, assetIds?: Iterable<string>) => {
      const ids = assetIds ? new Set(assetIds) : null
      for (const key of imageAssets.blobs.keys()) {
        if (!key.startsWith(`${userId}:`)) continue
        if (ids === null || ids.has(key.slice(`${userId}:`.length))) {
          imageAssets.blobs.delete(key)
        }
      }
      return true
    })
  vi.stubGlobal(
    'URL',
    Object.assign(class extends URL {}, {
      createObjectURL: vi.fn(() => 'blob:canvas-test-image'),
      revokeObjectURL: vi.fn(),
    })
  )
  useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role: 1 })
})

afterEach(() => {
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderCanvas() {
  return render(<InfiniteCanvas />)
}

async function addNote(title: string, content: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Add note' }))
  await user.type(screen.getByRole('textbox', { name: 'Note title' }), title)
  await user.type(
    screen.getByRole('textbox', { name: 'Note content' }),
    content
  )
}

it('restores an edited note for the same account after remounting', async () => {
  const first = renderCanvas()
  await addNote('Planning', 'Ship the canvas')
  first.unmount()

  renderCanvas()

  expect(await screen.findByDisplayValue('Planning')).toBeVisible()
  expect(screen.getByDisplayValue('Ship the canvas')).toBeVisible()
})

it('requires confirmation before clearing and keeps notes when cancelled', async () => {
  renderCanvas()
  await addNote('Keep this', 'Still here')
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Clear canvas' }))
  const dialog = await screen.findByRole('alertdialog')
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(screen.getByDisplayValue('Keep this')).toBeVisible()

  await user.click(screen.getByRole('button', { name: 'Clear canvas' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Clear canvas',
    })
  )
  await waitFor(() =>
    expect(screen.queryByDisplayValue('Keep this')).not.toBeInTheDocument()
  )
  expect(screen.getByText('Start with a note')).toBeVisible()
})

it('scopes canvas state to the active account', async () => {
  renderCanvas()
  await addNote('Alice private note', 'Account one')

  act(() => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'bob', role: 1 })
  })

  await waitFor(() => {
    expect(
      screen.queryByDisplayValue('Alice private note')
    ).not.toBeInTheDocument()
  })
  expect(screen.getByText('Start with a note')).toBeVisible()
})

it('shows a save failure when browser storage rejects writes', async () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
    throw new Error('storage quota exceeded')
  })

  renderCanvas()

  expect(
    await screen.findByText('Canvas could not be saved in this browser.')
  ).toBeVisible()
})

async function selectImage() {
  const user = userEvent.setup()
  const file = new File(['canvas image fixture'], 'diagram.png', {
    type: 'image/png',
  })
  await user.upload(screen.getByLabelText('Add image'), file)
  return file
}

it('shows a selected image and restores its asset when reopening the canvas', async () => {
  const first = renderCanvas()
  const file = await selectImage()
  expect(
    await screen.findByRole('img', { name: 'diagram.png' })
  ).toHaveAttribute('src', 'blob:canvas-test-image')
  expect(saveCanvasImage).toHaveBeenCalledWith(1, expect.any(String), file)
  first.unmount()
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:canvas-test-image')

  renderCanvas()

  expect(await screen.findByRole('img', { name: 'diagram.png' })).toBeVisible()
  expect(loadCanvasImage).toHaveBeenCalledWith(1, expect.any(String))
})

it('deletes only the right-clicked image and its local asset while keeping other canvas items', async () => {
  const first = renderCanvas()
  await addNote('Keep note', 'Keep content')
  await selectImage()
  const image = await screen.findByRole('img', { name: 'diagram.png' })
  const user = userEvent.setup()
  await user.upload(
    screen.getByLabelText('Add image'),
    new File(['second'], 'other.png', { type: 'image/png' })
  )
  await screen.findByRole('img', { name: 'other.png' })

  fireEvent.contextMenu(image, { clientX: 200, clientY: 200, button: 2 })
  await user.click(
    await screen.findByRole('menuitem', { name: 'Delete image' })
  )

  await waitFor(() =>
    expect(
      screen.queryByRole('img', { name: 'diagram.png' })
    ).not.toBeInTheDocument()
  )
  await waitFor(() => expect(imageAssets.remove).toHaveBeenCalledTimes(1))
  expect(screen.getByRole('img', { name: 'other.png' })).toBeVisible()
  expect(screen.getByDisplayValue('Keep note')).toBeVisible()
  first.unmount()
  renderCanvas()
  expect(await screen.findByRole('img', { name: 'other.png' })).toBeVisible()
  expect(
    screen.queryByRole('img', { name: 'diagram.png' })
  ).not.toBeInTheDocument()
})

it('shows an unavailable image placeholder when its local asset is missing', async () => {
  const first = renderCanvas()
  await selectImage()
  await screen.findByRole('img', { name: 'diagram.png' })
  first.unmount()
  imageAssets.blobs.clear()

  renderCanvas()

  expect(await screen.findByText('Image unavailable locally')).toBeVisible()
  expect(
    screen.queryByRole('img', { name: 'diagram.png' })
  ).not.toBeInTheDocument()
})

it('reports a rejected image save without adding an image node', async () => {
  imageAssets.save.mockResolvedValueOnce(false)
  renderCanvas()

  await selectImage()

  expect(
    await screen.findByText('Image could not be saved in this browser.')
  ).toBeVisible()
  expect(
    screen.queryByRole('img', { name: 'diagram.png' })
  ).not.toBeInTheDocument()
})

it('recovers from an image save failure when the user retries', async () => {
  imageAssets.save.mockResolvedValueOnce(false)
  renderCanvas()

  await selectImage()
  expect(
    await screen.findByText('Image could not be saved in this browser.')
  ).toBeVisible()

  await selectImage()

  expect(await screen.findByRole('img', { name: 'diagram.png' })).toBeVisible()
  expect(screen.getByText('Canvas saved locally')).toBeVisible()
})

it('keeps image assets isolated by account and clears only the current account', async () => {
  renderCanvas()
  await selectImage()
  await screen.findByRole('img', { name: 'diagram.png' })
  const ownAssetIds = [...imageAssets.blobs.keys()].map((key) => key.slice(2))
  imageAssets.blobs.set('2:other-account-image', new Blob(['other account']))

  act(() => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'bob', role: 1 })
  })
  expect(
    screen.queryByRole('img', { name: 'diagram.png' })
  ).not.toBeInTheDocument()

  act(() => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role: 1 })
  })
  await screen.findByRole('img', { name: 'diagram.png' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Clear canvas' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Clear canvas',
    })
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('img', { name: 'diagram.png' })
    ).not.toBeInTheDocument()
  )
  const [clearedUser, clearedAssets] =
    vi.mocked(clearCanvasImages).mock.calls[0] ?? []
  expect(clearedUser).toBe(1)
  expect(clearedAssets ? [...clearedAssets] : []).toEqual(ownAssetIds)
  expect([...imageAssets.blobs.keys()]).toEqual(['2:other-account-image'])
})

it('aborts an in-progress image when switching accounts', async () => {
  const saving = deferred<boolean>()
  const started = deferred<void>()
  imageAssets.save.mockImplementation(() => {
    started.resolve()
    return saving.promise
  })
  renderCanvas()
  const adding = selectImage()
  await started.promise

  act(() => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'bob', role: 1 })
  })
  saving.resolve(true)
  await adding

  expect(
    screen.queryByRole('img', { name: 'diagram.png' })
  ).not.toBeInTheDocument()
  await waitFor(() =>
    expect(imageAssets.remove).toHaveBeenCalledWith(1, expect.any(String))
  )
})

async function openGenerationForm() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Generate image' }))
  const dialog = await screen.findByRole('dialog', { name: 'Generate image' })
  const baseUrl = within(dialog).getByLabelText('API base URL')
  await user.clear(baseUrl)
  await user.type(baseUrl, 'https://images.example.com/v1')
  await user.type(within(dialog).getByLabelText('API key'), 'provider-only-key')
  await user.type(within(dialog).getByLabelText('Prompt'), 'A calm blue lake')
  return { user, dialog }
}

it('inserts a generated image into the canvas without storing its provider key', async () => {
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockResolvedValue(
      new Response(JSON.stringify({ data: [{ b64_json: generatedPng }] }))
    )
  vi.stubGlobal('fetch', fetchMock)
  const first = renderCanvas()
  const { user, dialog } = await openGenerationForm()
  expect(within(dialog).getByLabelText('API key')).toHaveAttribute(
    'type',
    'password'
  )

  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )

  expect(
    await screen.findByRole('img', { name: 'generated-image.png' })
  ).toBeVisible()
  expect(fetchMock).toHaveBeenCalledTimes(1)
  const savedValues = Array.from(
    { length: localStorage.length },
    (_value, index) => {
      const key = localStorage.key(index)
      return key ? localStorage.getItem(key) : null
    }
  )
  expect(savedValues.join('')).not.toContain('provider-only-key')
  first.unmount()
  renderCanvas()
  expect(
    await screen.findByRole('img', { name: 'generated-image.png' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Generate image' }))
  const reopened = await screen.findByRole('dialog', { name: 'Generate image' })
  expect(within(reopened).getByLabelText('API key')).toHaveValue('')
})

it('cancels a pending generation and prevents its late response from entering the canvas', async () => {
  const responding = deferred<Response>()
  const started = deferred<void>()
  let requestSignal: AbortSignal | null | undefined
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockImplementation((_url, request) => {
      requestSignal = request?.signal
      started.resolve()
      return responding.promise
    })
  vi.stubGlobal('fetch', fetchMock)
  renderCanvas()
  const { user, dialog } = await openGenerationForm()
  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )
  await started.promise

  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(requestSignal?.aborted).toBe(true)
  responding.resolve(
    new Response(JSON.stringify({ data: [{ b64_json: generatedPng }] }))
  )
  await act(async () => {
    await responding.promise
  })

  expect(
    screen.queryByRole('img', { name: 'generated-image.png' })
  ).not.toBeInTheDocument()
  expect(saveCanvasImage).not.toHaveBeenCalled()
})

it('retries saving an already generated image without charging another generation request', async () => {
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockResolvedValue(
      new Response(JSON.stringify({ data: [{ b64_json: generatedPng }] }))
    )
  vi.stubGlobal('fetch', fetchMock)
  imageAssets.save.mockResolvedValueOnce(false)
  renderCanvas()
  const { user, dialog } = await openGenerationForm()
  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )
  const retry = await within(dialog).findByRole('button', {
    name: 'Retry saving to canvas',
  })
  expect(
    within(dialog).getByRole('button', { name: 'Generate image' })
  ).toBeDisabled()

  await user.click(retry)

  expect(
    await screen.findByRole('img', { name: 'generated-image.png' })
  ).toBeVisible()
  expect(fetchMock).toHaveBeenCalledTimes(1)
})

it('shows a rejected API status and lets the user retry generation', async () => {
  const fetchMock = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      new Response('Unauthorized', {
        status: 401,
        headers: { 'Content-Type': 'text/plain' },
      })
    )
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ data: [{ b64_json: generatedPng }] }))
    )
  vi.stubGlobal('fetch', fetchMock)
  renderCanvas()
  const { user, dialog } = await openGenerationForm()
  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )

  expect(
    await within(dialog).findByText(
      'Image API rejected the request (HTTP 401). Check your API key and model.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('img', { name: 'generated-image.png' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).getByRole('button', { name: 'Generate image' })
  ).toBeEnabled()
  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )

  expect(
    await screen.findByRole('img', { name: 'generated-image.png' })
  ).toBeVisible()
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

it('shows required field errors for an empty key and prompt without fetching', async () => {
  const fetchMock = vi.fn<typeof fetch>()
  vi.stubGlobal('fetch', fetchMock)
  renderCanvas()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Generate image' }))
  const dialog = await screen.findByRole('dialog', { name: 'Generate image' })
  await user.click(
    within(dialog).getByRole('button', { name: 'Generate image' })
  )

  expect(await within(dialog).findByText('API key is required.')).toBeVisible()
  expect(await within(dialog).findByText('Prompt is required.')).toBeVisible()
  expect(fetchMock).not.toHaveBeenCalled()
})
