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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type ChannelStatus = 'operational' | 'degraded' | 'outage' | 'unknown'

export type ChannelStatusGroup = {
  group: string
  status: ChannelStatus
  available_channels: number
  total_channels: number
  last_checked_at?: number | null
  response_time_ms?: number | null
}

export type ChannelStatusSummary = {
  groups: ChannelStatusGroup[]
  updated_at?: number | null
}

export type ChannelStatusProbe = {
  id?: string
  name: string
  channel_id: number
  model: string
  endpoint_type?: string
  is_stream?: boolean
  interval_seconds: number
  timeout_seconds: number
  prompt: string
  enabled: boolean
}

export type ProbeResult = {
  probe_id: string
  status: ChannelStatus
  checked_at?: number | null
  response_time_ms?: number | null
  message?: string
  running?: boolean
}

export type ProbeChannel = {
  id: number
  name: string
  models: string[]
  group: string
}

export type ChannelStatusProbeData = {
  config: { enabled: boolean; probes: ChannelStatusProbe[] }
  results: ProbeResult[]
  channels: ProbeChannel[]
}

type ApiEnvelope<T> = { success?: boolean; data?: T; message?: string }

function unwrap<T>(value: ApiEnvelope<T> | T): T {
  const response = requireServerSuccess(value)
  if (
    typeof response === 'object' &&
    response !== null &&
    'data' in response &&
    response.data !== undefined
  ) {
    return response.data as T
  }
  return response as T
}

export async function getChannelStatus(): Promise<ChannelStatusSummary> {
  const response = await api.get<
    ApiEnvelope<ChannelStatusSummary> | ChannelStatusSummary
  >('/api/channel/status/')
  return unwrap(response.data)
}

export async function getChannelStatusProbes(): Promise<ChannelStatusProbeData> {
  const response = await api.get<
    ApiEnvelope<ChannelStatusProbeData> | ChannelStatusProbeData
  >('/api/channel/status/probes/')
  return unwrap(response.data)
}

export async function updateChannelStatusProbes(input: {
  enabled: boolean
  probes: ChannelStatusProbe[]
}): Promise<ChannelStatusProbeData> {
  const response = await api.put<
    ApiEnvelope<ChannelStatusProbeData> | ChannelStatusProbeData
  >('/api/channel/status/probes/', input)
  return unwrap(response.data)
}

export type ProbeTask = { task_id?: string; status: string }

export async function runChannelStatusProbe(
  probeId: string
): Promise<ProbeTask> {
  const response = await api.post<ApiEnvelope<ProbeTask> | ProbeTask>(
    `/api/channel/status/probes/${encodeURIComponent(probeId)}/run/`
  )
  return unwrap(response.data)
}

export async function runAllChannelStatusProbes(): Promise<ProbeTask> {
  const response = await api.post<ApiEnvelope<ProbeTask> | ProbeTask>(
    '/api/channel/status/probes/run/'
  )
  return unwrap(response.data)
}
