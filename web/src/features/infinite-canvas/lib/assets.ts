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
const DATABASE_NAME = 'new-api-infinite-canvas-assets'
const STORE_NAME = 'images'

function withCanvasAssets<T>(
  mode: IDBTransactionMode,
  initialResult: T,
  operation: (store: IDBObjectStore, setResult: (result: T) => void) => void
): Promise<T> {
  return new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DATABASE_NAME, 1)
    let settled = false

    request.addEventListener('upgradeneeded', () => {
      const store = request.result.createObjectStore(STORE_NAME, {
        keyPath: ['userId', 'assetId'],
      })
      store.createIndex('userId', 'userId')
    })
    request.addEventListener('error', () => reject(request.error))
    request.addEventListener('blocked', () => {
      settled = true
      reject(new Error('Local image storage is blocked'))
    })
    request.addEventListener('success', () => {
      const database = request.result
      if (settled) {
        database.close()
        return
      }
      database.addEventListener('versionchange', () => database.close())
      let result = initialResult
      try {
        const transaction = database.transaction(STORE_NAME, mode)
        transaction.addEventListener('complete', () => {
          database.close()
          resolve(result)
        })
        transaction.addEventListener('error', () => {
          database.close()
          reject(transaction.error)
        })
        transaction.addEventListener('abort', () => {
          database.close()
          reject(transaction.error)
        })
        operation(transaction.objectStore(STORE_NAME), (value) => {
          result = value
        })
      } catch (error) {
        database.close()
        reject(error)
      }
    })
  })
}

export async function saveCanvasImage(
  userId: number,
  assetId: string,
  blob: Blob
): Promise<boolean> {
  if (!Number.isFinite(userId) || !assetId || !blob.type.startsWith('image/')) {
    return false
  }
  try {
    return await withCanvasAssets('readwrite', true, (store) => {
      store.put({ userId, assetId, blob })
    })
  } catch {
    return false
  }
}

export async function loadCanvasImage(
  userId: number,
  assetId: string
): Promise<Blob | null> {
  if (!Number.isFinite(userId) || !assetId) return null
  try {
    return await withCanvasAssets<Blob | null>(
      'readonly',
      null,
      (store, setResult) => {
        const request = store.get([userId, assetId])
        request.addEventListener('success', () => {
          const record: unknown = request.result
          if (
            record &&
            typeof record === 'object' &&
            'blob' in record &&
            record.blob instanceof Blob &&
            record.blob.type.startsWith('image/')
          ) {
            setResult(record.blob)
          }
        })
      }
    )
  } catch {
    return null
  }
}

export async function deleteCanvasImage(
  userId: number,
  assetId: string
): Promise<boolean> {
  if (!Number.isFinite(userId) || !assetId) return false
  try {
    return await withCanvasAssets('readwrite', true, (store) => {
      store.delete([userId, assetId])
    })
  } catch {
    return false
  }
}

export async function clearCanvasImages(
  userId: number,
  assetIds?: Iterable<string>
): Promise<boolean> {
  if (!Number.isFinite(userId)) return false
  const assetsToDelete = assetIds ? new Set(assetIds) : null
  try {
    return await withCanvasAssets('readwrite', true, (store) => {
      const request = store.index('userId').openCursor(IDBKeyRange.only(userId))
      request.addEventListener('success', () => {
        const cursor = request.result
        if (!cursor) return
        const key = cursor.primaryKey
        const assetId = Array.isArray(key) ? key[1] : null
        if (
          assetsToDelete === null ||
          (typeof assetId === 'string' && assetsToDelete.has(assetId))
        ) {
          cursor.delete()
        }
        cursor.continue()
      })
    })
  } catch {
    return false
  }
}
