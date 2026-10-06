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
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type EdgeChange,
  type NodeChange,
  type Viewport,
  type XYPosition,
} from '@xyflow/react'
import { nanoid } from 'nanoid'
import { createStore } from 'zustand/vanilla'

import {
  clearCanvasImages,
  deleteCanvasImage,
  saveCanvasImage,
} from '@/features/infinite-canvas/lib/assets'
import {
  loadInfiniteCanvasState,
  saveInfiniteCanvasState,
} from '@/features/infinite-canvas/lib/storage'
import type {
  CanvasImageNode,
  CanvasNode,
  CanvasNoteNode,
  InfiniteCanvasState,
} from '@/features/infinite-canvas/types'

export type CanvasStore = InfiniteCanvasState & {
  saved: boolean
  saving: boolean
  addNote: (position: XYPosition) => void
  addImage: (
    file: File,
    position: XYPosition,
    signal?: AbortSignal
  ) => Promise<boolean>
  updateNodeData: (
    nodeId: string,
    data: Partial<CanvasNoteNode['data']>
  ) => void
  onNodesChange: (changes: NodeChange<CanvasNode>[]) => void
  removeNode: (nodeId: string) => void
  onEdgesChange: (changes: EdgeChange[]) => void
  connect: (connection: Connection) => void
  setViewport: (viewport: Viewport) => void
  clear: () => Promise<void>
  persist: () => void
}

export function createInfiniteCanvasStore(userId: number) {
  const initialState = loadInfiniteCanvasState(userId)

  return createStore<CanvasStore>()((set, get) => {
    let metadataSaved = true
    let assetsSaved = true
    const pendingCleanupAssets = new Set<string>()
    let pendingAssetOperations = 0
    let imageGeneration = 0
    let assetQueue = Promise.resolve()

    const refreshSaved = () => {
      set({
        saved: metadataSaved && assetsSaved && pendingAssetOperations === 0,
        saving: pendingAssetOperations > 0,
      })
    }

    // Serialize image writes and cleanup so clearing a canvas cannot remove a
    // newly added image or revive an image whose save was still in progress.
    const runAssetOperation = <T>(operation: () => Promise<T>): Promise<T> => {
      pendingAssetOperations += 1
      refreshSaved()
      const result = assetQueue.then(operation).finally(() => {
        pendingAssetOperations -= 1
        refreshSaved()
      })
      assetQueue = result.then(
        () => undefined,
        () => undefined
      )
      return result
    }

    // Keep persistence in the store so editing, deletion and viewport changes
    // all report whether the current workspace was actually saved.
    const update = (change: (state: CanvasStore) => InfiniteCanvasState) => {
      set((state) => {
        const nextState = change(state)
        metadataSaved = saveInfiniteCanvasState(userId, nextState)
        return {
          ...nextState,
          saved: metadataSaved && assetsSaved && pendingAssetOperations === 0,
        }
      })
      return metadataSaved
    }

    return {
      ...initialState,
      saved: true,
      saving: false,
      persist: () => {
        metadataSaved = saveInfiniteCanvasState(userId, get())
        refreshSaved()
      },
      addNote: (position) => {
        const node: CanvasNoteNode = {
          id: nanoid(),
          type: 'note',
          position,
          data: { title: '', content: '' },
        }
        update((state) => ({ ...state, nodes: [...state.nodes, node] }))
      },
      addImage: (file, position, signal) => {
        if (!file.type.startsWith('image/') || file.size === 0) {
          return Promise.resolve(false)
        }
        const generation = imageGeneration
        return runAssetOperation(async () => {
          if (signal?.aborted || generation !== imageGeneration) return false
          const assetId = nanoid()
          if (pendingCleanupAssets.size === 0) assetsSaved = true
          if (!(await saveCanvasImage(userId, assetId, file))) {
            assetsSaved = false
            return false
          }
          if (signal?.aborted || generation !== imageGeneration) {
            const deleted = await deleteCanvasImage(userId, assetId)
            if (!deleted) pendingCleanupAssets.add(assetId)
            assetsSaved = assetsSaved && deleted
            return false
          }
          const node: CanvasImageNode = {
            id: nanoid(),
            type: 'image',
            position,
            data: { assetId, name: file.name, mimeType: file.type },
          }
          const current = get()
          const nextState: InfiniteCanvasState = {
            nodes: [...current.nodes, node],
            edges: current.edges,
            viewport: current.viewport,
          }
          metadataSaved = saveInfiniteCanvasState(userId, nextState)
          if (!metadataSaved) {
            const deleted = await deleteCanvasImage(userId, assetId)
            if (!deleted) pendingCleanupAssets.add(assetId)
            assetsSaved = pendingCleanupAssets.size === 0
            return false
          }
          assetsSaved = pendingCleanupAssets.size === 0
          set({ ...nextState, saved: false })
          return true
        })
      },
      updateNodeData: (nodeId, data) =>
        update((state) => ({
          ...state,
          nodes: state.nodes.map((node) =>
            node.id === nodeId && node.type === 'note'
              ? { ...node, data: { ...node.data, ...data } }
              : node
          ),
        })),
      onNodesChange: (changes) => {
        const previousNodes = get().nodes
        const persisted = update((state) => {
          const nodes = applyNodeChanges(changes, state.nodes)
          const nodeIds = new Set(nodes.map((node) => node.id))
          return {
            nodes,
            edges: state.edges.filter(
              (edge) => nodeIds.has(edge.source) && nodeIds.has(edge.target)
            ),
            viewport: state.viewport,
          }
        })
        if (!persisted) return
        const retainedAssetIds = new Set(
          get().nodes.flatMap((node) =>
            node.type === 'image' ? [node.data.assetId] : []
          )
        )
        const removedAssetIds = new Set(
          previousNodes.flatMap((node) =>
            node.type === 'image' && !retainedAssetIds.has(node.data.assetId)
              ? [node.data.assetId]
              : []
          )
        )
        if (removedAssetIds.size === 0) return
        void runAssetOperation(async () => {
          const results = await Promise.all(
            [...removedAssetIds].map((assetId) =>
              deleteCanvasImage(userId, assetId)
            )
          )
          const assetIds = [...removedAssetIds]
          for (const [index, assetId] of assetIds.entries()) {
            if (!results[index]) pendingCleanupAssets.add(assetId)
          }
          assetsSaved = assetsSaved && pendingCleanupAssets.size === 0
        })
      },
      removeNode: (nodeId) =>
        get().onNodesChange([{ type: 'remove', id: nodeId }]),
      onEdgesChange: (changes) =>
        update((state) => ({
          ...state,
          edges: applyEdgeChanges(changes, state.edges),
        })),
      connect: (connection) =>
        update((state) => ({
          ...state,
          edges: addEdge(connection, state.edges),
        })),
      setViewport: (viewport) => update((state) => ({ ...state, viewport })),
      clear: async () => {
        imageGeneration += 1
        const assetsToClear = get().nodes.flatMap((node) =>
          node.type === 'image' ? [node.data.assetId] : []
        )
        const hadImages = assetsToClear.length > 0
        const persisted = update((state) => ({
          nodes: [],
          edges: [],
          viewport: state.viewport,
        }))
        if (
          !persisted ||
          (!hadImages && assetsSaved && pendingAssetOperations === 0)
        ) {
          return
        }
        await runAssetOperation(async () => {
          const cleanupIds = [
            ...new Set([...assetsToClear, ...pendingCleanupAssets]),
          ]
          assetsSaved = await clearCanvasImages(userId, cleanupIds)
          if (assetsSaved) {
            pendingCleanupAssets.clear()
          } else {
            for (const assetId of cleanupIds) pendingCleanupAssets.add(assetId)
          }
        })
      },
    }
  })
}
