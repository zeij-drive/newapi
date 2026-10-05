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
import { z } from 'zod'

import type { CanvasNode, InfiniteCanvasState } from '../types'

const STORAGE_PREFIX = 'new-api-infinite-canvas:v1:'

const finiteNumber = z.number().refine(Number.isFinite)

const noteNodeSchema = z.object({
  id: z.string().min(1),
  type: z.literal('note'),
  position: z.object({ x: finiteNumber, y: finiteNumber }),
  data: z.object({ title: z.string(), content: z.string() }),
})

const imageNodeSchema = z.object({
  id: z.string().min(1),
  type: z.literal('image'),
  position: z.object({ x: finiteNumber, y: finiteNumber }),
  data: z.object({
    assetId: z.string().min(1),
    name: z.string(),
    mimeType: z.string().regex(/^image\/[a-zA-Z0-9.+-]+$/),
  }),
})

const edgeSchema = z.object({
  id: z.string().min(1),
  source: z.string().min(1),
  target: z.string().min(1),
  sourceHandle: z.string().nullable().optional(),
  targetHandle: z.string().nullable().optional(),
})

const viewportSchema = z.object({
  x: finiteNumber,
  y: finiteNumber,
  zoom: finiteNumber.refine((value) => value > 0),
})

const envelopeSchema = z.object({
  nodes: z.array(
    z.discriminatedUnion('type', [noteNodeSchema, imageNodeSchema])
  ),
  edges: z.array(z.unknown()),
  viewport: viewportSchema.optional(),
})

const emptyState = (): InfiniteCanvasState => ({ nodes: [], edges: [] })

function storageKey(userId: number): string | null {
  return Number.isFinite(userId) ? `${STORAGE_PREFIX}${userId}` : null
}

function isUniqueNodeList(nodes: CanvasNode[]): boolean {
  const ids = new Set<string>()
  for (const node of nodes) {
    if (ids.has(node.id)) return false
    ids.add(node.id)
  }
  return true
}

function serializeState(state: InfiniteCanvasState): {
  nodes: CanvasNode[]
  edges: z.infer<typeof edgeSchema>[]
  viewport?: z.infer<typeof viewportSchema>
} | null {
  const parsed = envelopeSchema.safeParse({
    nodes: state.nodes,
    edges: state.edges,
    viewport: state.viewport,
  })
  if (!parsed.success || !isUniqueNodeList(parsed.data.nodes)) return null

  const nodeIds = new Set(parsed.data.nodes.map((node) => node.id))
  const edgeIds = new Set<string>()
  const edges: z.infer<typeof edgeSchema>[] = []

  for (const candidate of parsed.data.edges) {
    const edge = edgeSchema.safeParse(candidate)
    if (!edge.success) return null
    if (edgeIds.has(edge.data.id)) return null
    if (!nodeIds.has(edge.data.source) || !nodeIds.has(edge.data.target)) {
      return null
    }
    edgeIds.add(edge.data.id)
    edges.push(edge.data)
  }

  return {
    nodes: parsed.data.nodes,
    edges,
    ...(parsed.data.viewport ? { viewport: parsed.data.viewport } : {}),
  }
}

export function loadInfiniteCanvasState(userId: number): InfiniteCanvasState {
  const key = storageKey(userId)
  if (typeof window === 'undefined' || !key) return emptyState()

  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return emptyState()
    const parsed = JSON.parse(raw) as unknown
    const envelope = envelopeSchema.safeParse(parsed)
    if (!envelope.success || !isUniqueNodeList(envelope.data.nodes)) {
      return emptyState()
    }

    const nodeIds = new Set(envelope.data.nodes.map((node) => node.id))
    const edgeIds = new Set<string>()
    const edges = envelope.data.edges.flatMap((candidate) => {
      const edge = edgeSchema.safeParse(candidate)
      if (
        !edge.success ||
        edgeIds.has(edge.data.id) ||
        !nodeIds.has(edge.data.source) ||
        !nodeIds.has(edge.data.target)
      ) {
        return []
      }
      edgeIds.add(edge.data.id)
      return [edge.data]
    })

    return {
      nodes: envelope.data.nodes as CanvasNode[],
      edges,
      ...(envelope.data.viewport ? { viewport: envelope.data.viewport } : {}),
    }
  } catch {
    return emptyState()
  }
}

export function saveInfiniteCanvasState(
  userId: number,
  state: InfiniteCanvasState
): boolean {
  const key = storageKey(userId)
  if (typeof window === 'undefined' || !key) return false

  const serialized = serializeState(state)
  if (!serialized) return false

  try {
    window.localStorage.setItem(key, JSON.stringify(serialized))
    return true
  } catch {
    return false
  }
}
