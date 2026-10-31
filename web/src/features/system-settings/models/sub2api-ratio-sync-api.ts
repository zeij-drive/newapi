/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

export type Sub2APIRatioSyncMapping = {
  source_id: number
  target: string
  mode: 'effective' | 'default' | 'fixed'
  value: number
  enabled: boolean
  allow_peak: boolean
}

export type Sub2APIRatioSyncGroup = {
  id: number
  name: string
  canonical_name: string
  default: number
  custom: number | null
  effective: number
  peak: boolean
}

export type Sub2APIRatioSyncRuntime = {
  groups: Sub2APIRatioSyncGroup[]
  ratios: Record<string, number>
  warnings: Array<{ message: string; source_id?: number; target?: string }>
  failures: number
  last_run: number
  next_run: number
  last_result: { ok?: boolean; changed?: number; message?: string } | null
  logs: Array<{
    id: number
    created_at: number
    level: string
    message: string
    details?: Record<string, unknown>
  }>
  backups: Array<{ id: number; created_at: number }>
}

export type Sub2APIRatioSyncState = {
  settings: {
    upstream_url: string
    interval_seconds: number
    enabled: boolean
    mappings: Sub2APIRatioSyncMapping[]
    mapping_scope: string
  }
  runtime: Sub2APIRatioSyncRuntime
  auth: { upstream: boolean; refresh: boolean }
  server_time: number
}

export type Sub2APIRatioSyncResult = {
  at: number
  mode: string
  changed: number
  rows: Array<{
    source: string
    source_id: number
    source_rate: number
    target: string
    before: number
    after: number
    changed: boolean
  }>
  warnings: Sub2APIRatioSyncRuntime['warnings']
  upstream_changes: Array<{
    source: string
    target: string
    before: number
    after: number
  }>
  backup_id?: number
}

type ApiResponse<T> = { success: boolean; message: string; data: T }

function requireSuccess<T>(response: ApiResponse<T>): T {
  if (!response.success) throw createServerError(response)
  return response.data
}

const basePath = '/api/option/sub2api-ratio-sync'

export async function getSub2APIRatioSyncState() {
  const response = await api.get<ApiResponse<Sub2APIRatioSyncState>>(basePath)
  return requireSuccess(response.data)
}

export async function saveSub2APIRatioSyncSettings(request: {
  upstream_url: string
  interval_seconds: number
}) {
  const response = await api.put<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/settings`,
    request
  )
  return requireSuccess(response.data)
}

export async function saveSub2APIRatioSyncMappings(
  mappings: Sub2APIRatioSyncMapping[]
) {
  const response = await api.put<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/mappings`,
    { mappings }
  )
  return requireSuccess(response.data)
}

export async function importSub2APIRatioSyncTokens(request: {
  access_token: string
  refresh_token: string
}) {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/tokens`,
    request
  )
  return requireSuccess(response.data)
}

export async function clearSub2APIRatioSyncTokens() {
  const response = await api.delete<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/tokens`
  )
  return requireSuccess(response.data)
}

export async function refreshSub2APIRatioSyncGroups() {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/groups/refresh`
  )
  return requireSuccess(response.data)
}

export async function previewSub2APIRatioSync() {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncResult>>(
    `${basePath}/preview`
  )
  return requireSuccess(response.data)
}

export async function runSub2APIRatioSync() {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncResult>>(
    `${basePath}/sync`
  )
  return requireSuccess(response.data)
}

export async function startSub2APIRatioSync(interval_seconds: number) {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncResult>>(
    `${basePath}/monitor/start`,
    { interval_seconds }
  )
  return requireSuccess(response.data)
}

export async function stopSub2APIRatioSync() {
  const response = await api.post<ApiResponse<Sub2APIRatioSyncState>>(
    `${basePath}/monitor/stop`
  )
  return requireSuccess(response.data)
}

export async function createSub2APIRatioSyncPair() {
  const response = await api.post<
    ApiResponse<{ pair: string; expires_at: number; url: string }>
  >(`${basePath}/pair`)
  return requireSuccess(response.data)
}

export async function checkSub2APIRatioSyncPair(pair: string) {
  const response = await api.post<
    ApiResponse<{ done: boolean; expired: boolean }>
  >(`${basePath}/pair/status`, { pair })
  return requireSuccess(response.data)
}

export async function getSub2APIRatioSyncLoginHelper() {
  const response = await api.get<string>(`${basePath}/pair/login-helper`, {
    responseType: 'text',
  })
  return response.data
}

export async function downloadSub2APIRatioSyncBackup(id: number) {
  const response = await api.get<Blob>(`${basePath}/backups/${id}`, {
    responseType: 'blob',
  })
  return response.data
}
