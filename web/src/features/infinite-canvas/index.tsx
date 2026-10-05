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
  MiniMap,
  type Edge,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from '@xyflow/react'
import { ImagePlus, Plus, Sparkles, Trash2, Workflow } from 'lucide-react'
import {
  createContext,
  memo,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
} from 'react'
import { useTranslation } from 'react-i18next'
import { useStore } from 'zustand'
import type { StoreApi } from 'zustand/vanilla'

import { Canvas } from '@/components/ai-elements/canvas'
import {
  Node as CanvasNode,
  NodeContent,
  NodeHeader,
  NodeTitle,
} from '@/components/ai-elements/node'
import { Panel } from '@/components/ai-elements/panel'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'
import {
  createInfiniteCanvasStore,
  type CanvasStore,
} from '@/stores/infinite-canvas-store'

import { ImageGenerationDialog } from './components/image-generation-dialog'
import { loadCanvasImage } from './lib/assets'
import type {
  CanvasImageNode,
  CanvasNode as InfiniteCanvasNode,
  CanvasNoteData,
  CanvasNoteNode,
} from './types'

const CanvasStoreContext = createContext<StoreApi<CanvasStore> | null>(null)
const CanvasUserContext = createContext<number | null>(null)

const NoteNode = memo(function NoteNode(props: NodeProps<CanvasNoteNode>) {
  const { t } = useTranslation()
  const store = useContext(CanvasStoreContext)

  const updateData = useCallback(
    (key: keyof CanvasNoteData, value: string) => {
      store?.getState().updateNodeData(props.id, { [key]: value })
    },
    [props.id, store]
  )

  return (
    <CanvasNode
      handles={{ target: true, source: true }}
      className={
        props.selected ? 'ring-primary ring-2 ring-offset-2' : undefined
      }
    >
      <NodeHeader>
        <NodeTitle className='flex items-center gap-2 text-xs tracking-wide uppercase'>
          <Workflow className='size-3.5' aria-hidden='true' />
          {t('Note')}
        </NodeTitle>
        <Input
          aria-label={t('Note title')}
          className='nodrag nopan nowheel bg-background/70 mt-2 h-8 font-medium'
          value={props.data.title}
          onChange={(event) => updateData('title', event.target.value)}
          placeholder={t('Untitled note')}
        />
      </NodeHeader>
      <NodeContent>
        <Textarea
          aria-label={t('Note content')}
          className='nodrag nopan nowheel bg-background/50 min-h-28 resize-y'
          value={props.data.content}
          onChange={(event) => updateData('content', event.target.value)}
          placeholder={t('Write something here...')}
        />
      </NodeContent>
    </CanvasNode>
  )
})

const ImageNode = memo(function ImageNode(props: NodeProps<CanvasImageNode>) {
  const { t } = useTranslation()
  const userId = useContext(CanvasUserContext)
  const [source, setSource] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let active = true
    let objectUrl: string | null = null
    setSource(null)
    setFailed(false)
    if (userId === null) return

    void loadCanvasImage(userId, props.data.assetId)
      .then((blob) => {
        if (!active) return
        if (!blob || typeof URL.createObjectURL !== 'function') {
          setFailed(true)
          return
        }
        objectUrl = URL.createObjectURL(blob)
        setSource(objectUrl)
      })
      .catch(() => {
        if (active) setFailed(true)
      })

    return () => {
      active = false
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [props.data.assetId, userId])

  return (
    <CanvasNode
      handles={{ target: true, source: true }}
      className={
        props.selected ? 'ring-primary ring-2 ring-offset-2' : undefined
      }
    >
      <NodeHeader>
        <NodeTitle className='flex items-center gap-2 text-xs tracking-wide uppercase'>
          <ImagePlus className='size-3.5' aria-hidden='true' />
          {t('Image')}
        </NodeTitle>
      </NodeHeader>
      <NodeContent>
        {source ? (
          <img
            src={source}
            alt={props.data.name}
            className='nodrag nopan nowheel max-h-72 max-w-72 rounded-md object-contain'
            onError={() => {
              setFailed(true)
              setSource(null)
            }}
          />
        ) : null}
        {!source && failed && (
          <ErrorState
            title={t('Image unavailable locally')}
            className='min-h-0 max-w-72 py-4'
          />
        )}
        {!source && !failed && (
          <LoadingState
            message={t('Loading image...')}
            size='sm'
            className='min-h-0 py-4'
          />
        )}
        <p className='text-muted-foreground mt-2 max-w-64 truncate text-xs'>
          {props.data.name}
        </p>
      </NodeContent>
    </CanvasNode>
  )
})

const NODE_TYPES: NodeTypes = { note: NoteNode, image: ImageNode }

export function InfiniteCanvas() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  if (userId === undefined) return null
  return <CanvasWorkspace key={userId} userId={userId} />
}

function CanvasWorkspace(props: { userId: number }) {
  const { t } = useTranslation()
  const [store] = useState(() => createInfiniteCanvasStore(props.userId))
  const nodes = useStore(store, (state) => state.nodes)
  const edges = useStore(store, (state) => state.edges)
  const saved = useStore(store, (state) => state.saved)
  const saving = useStore(store, (state) => state.saving)
  const [initialViewport] = useState(() => store.getState().viewport)
  const [fitInitialView] = useState(
    () =>
      store.getState().nodes.length > 0 &&
      store.getState().viewport === undefined
  )
  const [flow, setFlow] = useState<ReactFlowInstance<
    InfiniteCanvasNode,
    Edge
  > | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const imageInputRef = useRef<HTMLInputElement>(null)
  const imageAbortController = useRef<AbortController | null>(null)
  const [confirmClear, setConfirmClear] = useState(false)
  const [imageError, setImageError] = useState(false)
  const [generationOpen, setGenerationOpen] = useState(false)

  useEffect(() => {
    store.getState().persist()
    return () => imageAbortController.current?.abort()
  }, [store])

  const centerPosition = useCallback(() => {
    if (!flow || !containerRef.current) return null
    const bounds = containerRef.current.getBoundingClientRect()
    const center = flow.screenToFlowPosition({
      x: bounds.left + bounds.width / 2,
      y: bounds.top + bounds.height / 2,
    })
    const offset = (store.getState().nodes.length % 5) * 20
    return { x: center.x - 192 + offset, y: center.y - 120 + offset }
  }, [flow, store])

  const addNote = useCallback(() => {
    const position = centerPosition()
    if (position) store.getState().addNote(position)
  }, [centerPosition, store])

  const chooseImage = useCallback(() => imageInputRef.current?.click(), [])

  const addImage = useCallback(
    async (event: ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0]
      event.target.value = ''
      const position = centerPosition()
      if (!file || !position) return
      imageAbortController.current?.abort()
      const controller = new AbortController()
      imageAbortController.current = controller
      setImageError(false)
      const added = await store
        .getState()
        .addImage(file, position, controller.signal)
      if (!controller.signal.aborted) setImageError(!added)
    },
    [centerPosition, store]
  )

  const clearCanvas = useCallback(async () => {
    imageAbortController.current?.abort()
    setGenerationOpen(false)
    await store.getState().clear()
    setImageError(false)
    setConfirmClear(false)
  }, [store])

  const addGeneratedImage = useCallback(
    async (file: File, signal: AbortSignal) => {
      const position = centerPosition()
      if (!position || signal.aborted) return false
      return store.getState().addImage(file, position, signal)
    },
    [centerPosition, store]
  )

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Infinite Canvas')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' size='sm' onClick={addNote} disabled={!flow}>
          <Plus aria-hidden='true' />
          {t('Add note')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          onClick={chooseImage}
          disabled={!flow || saving}
        >
          <ImagePlus aria-hidden='true' />
          {t('Add image')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setGenerationOpen(true)}
          disabled={!flow || saving}
        >
          <Sparkles aria-hidden='true' />
          {t('Generate image')}
        </Button>
        <input
          ref={imageInputRef}
          type='file'
          accept='image/*'
          aria-label={t('Add image')}
          className='hidden'
          onChange={addImage}
        />
        <Button
          variant='ghost'
          size='sm'
          onClick={() => setConfirmClear(true)}
          disabled={nodes.length === 0 && edges.length === 0}
        >
          <Trash2 aria-hidden='true' />
          {t('Clear canvas')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <CanvasStoreContext.Provider value={store}>
          <CanvasUserContext.Provider value={props.userId}>
            <div
              ref={containerRef}
              className='bg-muted/20 relative h-full min-h-0 overflow-hidden rounded-lg border'
            >
              <Canvas<InfiniteCanvasNode, Edge>
                nodes={nodes}
                edges={edges}
                nodeTypes={NODE_TYPES}
                onInit={setFlow}
                onNodesChange={store.getState().onNodesChange}
                onEdgesChange={store.getState().onEdgesChange}
                onConnect={store.getState().connect}
                onMoveEnd={(_event, viewport) =>
                  store.getState().setViewport(viewport)
                }
                panOnDrag
                panOnScroll={false}
                zoomOnScroll
                selectionOnDrag
                fitView={fitInitialView}
                defaultViewport={initialViewport}
                minZoom={0.2}
                maxZoom={2}
                defaultEdgeOptions={{ animated: true }}
                deleteKeyCode={['Backspace', 'Delete']}
                ariaLabelConfig={{
                  'controls.ariaLabel': t('Canvas controls'),
                  'controls.zoomIn.ariaLabel': t('Zoom in'),
                  'controls.zoomOut.ariaLabel': t('Zoom out'),
                  'controls.fitView.ariaLabel': t('Fit canvas'),
                  'controls.interactive.ariaLabel': t(
                    'Toggle canvas interaction'
                  ),
                  'minimap.ariaLabel': t('Canvas minimap'),
                  'handle.ariaLabel': t('Connection handle'),
                  'node.a11yDescription.default': t(
                    'Press Enter or Space to select an item. Press Delete to remove it or Escape to cancel.'
                  ),
                  'node.a11yDescription.keyboardDisabled': t(
                    'Press Enter or Space to select an item. Use arrow keys to move it. Press Delete to remove it or Escape to cancel.'
                  ),
                  'edge.a11yDescription.default': t(
                    'Press Enter or Space to select a connection. Press Delete to remove it or Escape to cancel.'
                  ),
                  'node.a11yDescription.ariaLiveMessage': ({
                    direction,
                    x,
                    y,
                  }) => {
                    const directions: Record<string, string> = {
                      up: t('upward'),
                      down: t('downward'),
                      left: t('leftward'),
                      right: t('rightward'),
                    }
                    return t(
                      'Moved selected item {{direction}}. Position: x {{x}}, y {{y}}.',
                      { direction: directions[direction] ?? direction, x, y }
                    )
                  },
                }}
              >
                <Panel position='top-left' className='max-w-xs'>
                  <p role='status' className='text-sm font-medium'>
                    {saving && t('Saving locally...')}
                    {!saving && saved && t('Canvas saved locally')}
                    {!saving &&
                      !saved &&
                      t('Canvas could not be saved in this browser.')}
                  </p>
                  {imageError && (
                    <p role='alert' className='text-destructive mt-1 text-xs'>
                      {t('Image could not be saved in this browser.')}
                    </p>
                  )}
                  <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
                    {t('Drag to pan. Use the wheel to zoom.')}
                  </p>
                  <p className='text-muted-foreground mt-1 text-xs leading-relaxed'>
                    {t(
                      'Images are stored only in this browser on your device.'
                    )}
                  </p>
                </Panel>
                <MiniMap
                  pannable
                  zoomable
                  className='bg-card! border! shadow-none! max-sm:hidden'
                />
              </Canvas>
              {nodes.length === 0 && (
                <div className='pointer-events-none absolute inset-0 flex items-center justify-center p-6'>
                  <EmptyState
                    icon={Workflow}
                    title={t('Start with a note')}
                    description={t(
                      'Add a note or image to start mapping your ideas.'
                    )}
                    className='bg-background/85 min-h-0 max-w-sm rounded-lg border px-5 py-4 shadow-sm backdrop-blur'
                  />
                </div>
              )}
            </div>
          </CanvasUserContext.Provider>
        </CanvasStoreContext.Provider>
        <ConfirmDialog
          open={confirmClear}
          onOpenChange={setConfirmClear}
          title={t('Clear canvas')}
          desc={t(
            'Delete all notes, images and connections? This action cannot be undone.'
          )}
          confirmText={t('Clear canvas')}
          destructive
          handleConfirm={clearCanvas}
        />
        <ImageGenerationDialog
          userId={props.userId}
          open={generationOpen}
          onOpenChange={setGenerationOpen}
          onGenerated={addGeneratedImage}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
