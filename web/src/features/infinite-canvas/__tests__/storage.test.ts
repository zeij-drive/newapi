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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { initializeFrontendCache } from '@/lib/frontend-cache'
import { createInfiniteCanvasStore } from '@/stores/infinite-canvas-store'

import { deleteCanvasImage, saveCanvasImage } from '../lib/assets'
import {
  loadInfiniteCanvasState,
  saveInfiniteCanvasState,
} from '../lib/storage'
import type {
  CanvasImageNode,
  CanvasNoteNode,
  InfiniteCanvasState,
} from '../types'

const imageAssets = vi.hoisted(() => ({
  blobs: new Map<string, Blob>(),
  save: vi.fn(),
  remove: vi.fn(),
  clear: vi.fn(),
}))

vi.mock('../lib/assets', () => ({
  saveCanvasImage: imageAssets.save,
  loadCanvasImage: vi.fn(),
  deleteCanvasImage: imageAssets.remove,
  clearCanvasImages: imageAssets.clear,
}))

function note(id: string, x = 10): CanvasNoteNode {
  return {
    id,
    type: 'note',
    position: { x, y: 20 },
    data: { title: `Title ${id}`, content: `Content ${id}` },
  }
}

const state: InfiniteCanvasState = {
  nodes: [note('a'), note('b', 100)],
  edges: [{ id: 'edge-a-b', source: 'a', target: 'b' }],
  viewport: { x: 4, y: 8, zoom: 1.25 },
}

function deferred<T>() {
  let resolvePromise: (value: T) => void = () => undefined
  const promise = new Promise<T>((resolve) => {
    resolvePromise = resolve
  })
  return { promise, resolve: resolvePromise }
}

beforeEach(() => {
  window.localStorage.clear()
  imageAssets.blobs.clear()
  imageAssets.save
    .mockReset()
    .mockImplementation(async (userId, assetId, blob) => {
      imageAssets.blobs.set(`${userId}:${assetId}`, blob)
      return true
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
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

function replaceStoredCanvas(raw: string): void {
  expect(saveInfiniteCanvasState(101, state)).toBe(true)
  const key = window.localStorage.key(0)
  if (key === null) throw new Error('Canvas was not saved')
  window.localStorage.setItem(key, raw)
}

describe('infinite canvas storage', () => {
  it('restores note data, connections, and viewport without transient selection state', () => {
    const selectedState: InfiniteCanvasState = {
      ...state,
      nodes: state.nodes.map((node) => ({ ...node, selected: true })),
      edges: state.edges.map((edge) => ({ ...edge, selected: true })),
    }

    expect(saveInfiniteCanvasState(101, selectedState)).toBe(true)
    expect(loadInfiniteCanvasState(101)).toEqual(state)
  })

  it('keeps saved canvases isolated when switching accounts', () => {
    const otherState = { nodes: [note('other')], edges: [] }
    expect(saveInfiniteCanvasState(101, state)).toBe(true)
    expect(loadInfiniteCanvasState(202)).toEqual({ nodes: [], edges: [] })
    expect(saveInfiniteCanvasState(202, otherState)).toBe(true)

    expect(loadInfiniteCanvasState(101)).toEqual(state)
    expect(loadInfiniteCanvasState(202)).toEqual(otherState)
  })

  it('keeps the canvas when frontend cache initialization clears old UI cache', () => {
    expect(saveInfiniteCanvasState(101, state)).toBe(true)

    initializeFrontendCache()

    expect(loadInfiniteCanvasState(101)).toEqual(state)
  })

  it('removes connected edges with a deleted note and restores that result on reopening', () => {
    expect(saveInfiniteCanvasState(101, state)).toBe(true)
    const canvas = createInfiniteCanvasStore(101)

    canvas.getState().onNodesChange([{ type: 'remove', id: 'a' }])

    const restored = createInfiniteCanvasStore(101).getState()
    expect(restored.nodes).toEqual([note('b', 100)])
    expect(restored.edges).toEqual([])
    expect(restored.viewport).toEqual(state.viewport)
  })

  it.each([
    ['bad JSON', '{'],
    [
      'missing node fields',
      JSON.stringify({ nodes: [{ id: 'a', type: 'note' }], edges: [] }),
    ],
    [
      'duplicate node IDs',
      JSON.stringify({ nodes: [note('a'), note('a')], edges: [] }),
    ],
    [
      'non-finite node coordinates',
      '{"nodes":[{"id":"a","type":"note","position":{"x":1e400,"y":0},"data":{"title":"","content":""}}],"edges":[]}',
    ],
    [
      'invalid viewport zoom',
      JSON.stringify({ ...state, viewport: { x: 0, y: 0, zoom: 0 } }),
    ],
  ])('returns an empty canvas for %s', (_name, raw) => {
    replaceStoredCanvas(raw)
    expect(loadInfiniteCanvasState(101)).toEqual({ nodes: [], edges: [] })
  })

  it('filters malformed, disconnected, and duplicate edges while retaining valid notes', () => {
    replaceStoredCanvas(
      JSON.stringify({
        nodes: [note('a'), note('b')],
        edges: [
          { id: 'valid', source: 'a', target: 'b' },
          { id: 'missing-target', source: 'a', target: 'unknown' },
          { id: 'malformed', source: 'a' },
          { id: 'valid', source: 'b', target: 'a' },
        ],
      })
    )

    expect(loadInfiniteCanvasState(101)).toEqual({
      nodes: [note('a'), note('b')],
      edges: [{ id: 'valid', source: 'a', target: 'b' }],
    })
  })

  it('falls back to an empty canvas when storage reads fail', () => {
    expect(saveInfiniteCanvasState(101, state)).toBe(true)
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new DOMException('Storage unavailable', 'SecurityError')
      },
    })
    expect(loadInfiniteCanvasState(101)).toEqual({ nodes: [], edges: [] })
  })

  it('reports failure and preserves the prior canvas when storage is full', () => {
    expect(saveInfiniteCanvasState(101, state)).toBe(true)
    const storage = window.localStorage
    vi.stubGlobal('localStorage', {
      getItem: storage.getItem.bind(storage),
      setItem: () => {
        throw new DOMException('Storage full', 'QuotaExceededError')
      },
    })

    expect(saveInfiniteCanvasState(101, { nodes: [], edges: [] })).toBe(false)
    expect(loadInfiniteCanvasState(101)).toEqual(state)
  })

  it('restores image metadata without persisting image bytes or object URLs', () => {
    const image: CanvasImageNode = {
      id: 'image-one',
      type: 'image',
      position: { x: 50, y: 100 },
      data: {
        assetId: 'asset-one',
        name: 'diagram.png',
        mimeType: 'image/png',
      },
    }
    const images: InfiniteCanvasState = {
      nodes: [image],
      edges: [],
    }
    const imageWithPreview = {
      ...image,
      selected: true,
      data: {
        ...image.data,
        previewUrl: 'blob:temporary-preview',
        dataUrl: 'data:image/png;base64,cGljdHVyZQ==',
      },
    }
    expect(
      saveInfiniteCanvasState(101, {
        ...images,
        nodes: [imageWithPreview],
      })
    ).toBe(true)

    expect(loadInfiniteCanvasState(101)).toEqual(images)
    const key = window.localStorage.key(0)
    if (key === null) throw new Error('Canvas metadata was not saved')
    const raw = window.localStorage.getItem(key)
    if (raw === null) throw new Error('Canvas metadata was not saved')
    expect(JSON.parse(raw)).toEqual(images)
  })

  it('rolls back the image asset when saving its metadata fails and keeps the old canvas', async () => {
    expect(saveInfiniteCanvasState(101, state)).toBe(true)
    const canvas = createInfiniteCanvasStore(101)
    const file = new File(['diagram bytes'], 'diagram.png', {
      type: 'image/png',
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Storage full', 'QuotaExceededError')
    })

    expect(await canvas.getState().addImage(file, { x: 0, y: 0 })).toBe(false)

    expect(saveCanvasImage).toHaveBeenCalledWith(101, expect.any(String), file)
    expect(deleteCanvasImage).toHaveBeenCalledWith(101, expect.any(String))
    expect(imageAssets.blobs.size).toBe(0)
    expect(canvas.getState().nodes).toEqual(state.nodes)
    expect(loadInfiniteCanvasState(101)).toEqual(state)
  })

  it('does not resurrect an image when clearing while its asset save is pending', async () => {
    const saving = deferred<boolean>()
    const started = deferred<void>()
    imageAssets.save.mockImplementation(async (userId, assetId, blob) => {
      started.resolve()
      const saved = await saving.promise
      if (saved) imageAssets.blobs.set(`${userId}:${assetId}`, blob)
      return saved
    })
    const canvas = createInfiniteCanvasStore(101)
    const file = new File(['pending image'], 'pending.png', {
      type: 'image/png',
    })

    const adding = canvas.getState().addImage(file, { x: 0, y: 0 })
    await started.promise
    const clearing = canvas.getState().clear()
    saving.resolve(true)

    expect(await adding).toBe(false)
    await clearing
    expect(canvas.getState().nodes).toEqual([])
    expect(deleteCanvasImage).toHaveBeenCalledWith(101, expect.any(String))
    expect(imageAssets.blobs.size).toBe(0)
  })

  it('aborts an image save without adding metadata and removes the committed blob', async () => {
    const saving = deferred<boolean>()
    const started = deferred<void>()
    imageAssets.save.mockImplementation(async (userId, assetId, blob) => {
      started.resolve()
      const saved = await saving.promise
      if (saved) imageAssets.blobs.set(`${userId}:${assetId}`, blob)
      return saved
    })
    const canvas = createInfiniteCanvasStore(101)
    const controller = new AbortController()
    const file = new File(['aborted image'], 'aborted.png', {
      type: 'image/png',
    })

    const adding = canvas
      .getState()
      .addImage(file, { x: 0, y: 0 }, controller.signal)
    await started.promise
    controller.abort()
    saving.resolve(true)

    expect(await adding).toBe(false)
    expect(canvas.getState().nodes).toEqual([])
    expect(deleteCanvasImage).toHaveBeenCalledWith(101, expect.any(String))
    expect(imageAssets.blobs.size).toBe(0)
  })

  it('keeps a new image when an older workspace finishes clearing the same account', async () => {
    const oldCanvas = createInfiniteCanvasStore(101)
    const oldFile = new File(['old image'], 'old.png', { type: 'image/png' })
    expect(await oldCanvas.getState().addImage(oldFile, { x: 0, y: 0 })).toBe(
      true
    )
    const clearing = deferred<void>()
    const started = deferred<void>()
    imageAssets.clear.mockImplementationOnce(
      async (userId: number, assetIds: Iterable<string>) => {
        const ids = [...assetIds]
        started.resolve()
        await clearing.promise
        for (const id of ids) imageAssets.blobs.delete(`${userId}:${id}`)
        return true
      }
    )

    const oldClear = oldCanvas.getState().clear()
    await started.promise
    const newCanvas = createInfiniteCanvasStore(101)
    const newFile = new File(['new image'], 'new.png', { type: 'image/png' })
    expect(await newCanvas.getState().addImage(newFile, { x: 50, y: 50 })).toBe(
      true
    )
    clearing.resolve()
    await oldClear

    expect([...imageAssets.blobs.values()]).toEqual([newFile])
    expect(loadInfiniteCanvasState(101).nodes).toEqual(
      newCanvas.getState().nodes
    )
  })

  it('keeps saved false after image cleanup fails and a later note edit succeeds', async () => {
    const canvas = createInfiniteCanvasStore(101)
    const file = new File(['cleanup image'], 'cleanup.png', {
      type: 'image/png',
    })
    expect(await canvas.getState().addImage(file, { x: 0, y: 0 })).toBe(true)
    imageAssets.clear.mockResolvedValue(false)
    await canvas.getState().clear()
    canvas.getState().addNote({ x: 10, y: 10 })

    expect(canvas.getState().saved).toBe(false)
  })
})
