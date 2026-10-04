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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { ChannelStatusProbeData, ChannelStatusSummary } from '../api'
import { ChannelStatus } from '../index'

const summary: ChannelStatusSummary = {
  groups: [
    {
      group: 'default',
      status: 'operational',
      available_channels: 2,
      total_channels: 2,
      last_checked_at: 1000,
      response_time_ms: 125,
    },
    {
      group: 'priority',
      status: 'degraded',
      available_channels: 1,
      total_channels: 2,
      last_checked_at: 1000,
    },
  ],
}
const probes: ChannelStatusProbeData = {
  config: {
    enabled: true,
    probes: [
      {
        id: 'probe-one',
        name: 'Primary probe',
        channel_id: 7,
        model: 'gpt-test',
        interval_seconds: 300,
        timeout_seconds: 30,
        prompt: '',
        enabled: true,
      },
    ],
  },
  results: [
    {
      probe_id: 'probe-one',
      status: 'outage',
      checked_at: 1000,
      response_time_ms: 50,
      message: 'Upstream unavailable',
    },
  ],
  channels: [
    { id: 7, name: 'Private channel', models: ['gpt-test'], group: 'default' },
  ],
}
let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore.getState().auth.reset()
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/channel/status/probes/' ? probes : summary,
    },
  }))
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
})

function renderPage(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'status-user', role })
  return render(
    <QueryClientProvider client={client}>
      <ChannelStatus />
    </QueryClientProvider>
  )
}

it.each([1, 10])(
  'role %i sees only group summaries and does not fetch probe details',
  async (role) => {
    renderPage(role)
    expect(await screen.findByText('priority')).toBeVisible()
    expect(screen.getByText('Operational')).toBeVisible()
    expect(screen.getByText('Degraded')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Add probe' })
    ).not.toBeInTheDocument()
    expect(screen.queryByText('Private channel')).not.toBeInTheDocument()
    expect(
      vi
        .mocked(api.get)
        .mock.calls.some(([url]) => url === '/api/channel/status/probes/')
    ).toBe(false)
  }
)

it('root sees probe controls and results without exposing them after a role change', async () => {
  renderPage(100)
  expect(await screen.findByRole('button', { name: 'Add probe' })).toBeVisible()
  expect(screen.getByText('Private channel')).toBeVisible()
  expect(screen.getByText('Upstream unavailable')).toBeVisible()
  await act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'status-user', role: 10 })
  )
  expect(
    screen.queryByRole('button', { name: 'Add probe' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Private channel')).not.toBeInTheDocument()
})

it('an empty summary shows the configured empty state', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: { groups: [] } },
  })
  renderPage(1)
  expect(await screen.findByText('No channel groups')).toBeVisible()
})

it('a failed summary provides a retry that loads the group status', async () => {
  vi.mocked(api.get).mockRejectedValueOnce(new Error('Status unavailable'))
  renderPage(1)
  expect(await screen.findByText('Status unavailable')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('priority')).toBeVisible()
})

it('a rejected save keeps the edited probe open and preserves the input', async () => {
  vi.spyOn(toast, 'error')
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Save rejected' },
  })
  renderPage(100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Edit probe' })
  )
  const dialog = await screen.findByRole('dialog')
  const name = within(dialog).getByRole('textbox', { name: 'Name' })
  await userEvent.clear(name)
  await userEvent.type(name, 'Updated probe')
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Save probe' })
  )
  await waitFor(() => expect(toast.error).toHaveBeenCalledWith('Save rejected'))
  expect(await screen.findByRole('dialog')).toBeVisible()
  expect(name).toHaveValue('Updated probe')
})

it('saving a new probe submits its settings and displays the saved probe', async () => {
  vi.spyOn(api, 'put').mockImplementation(async (_url, payload) => {
    const config = payload as ChannelStatusProbeData['config']
    return {
      data: {
        success: true,
        data: {
          ...probes,
          config: {
            ...config,
            probes: config.probes.map((probe) => ({
              ...probe,
              id: probe.id || 'probe-new',
            })),
          },
        },
      },
    }
  })
  renderPage(100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Add probe' })
  )
  const dialog = await screen.findByRole('dialog')
  await userEvent.type(
    within(dialog).getByRole('textbox', { name: 'Name' }),
    'Backup probe'
  )
  await userEvent.selectOptions(
    within(dialog).getByRole('combobox', { name: 'Channel' }),
    '7'
  )
  await userEvent.selectOptions(
    within(dialog).getByRole('combobox', { name: 'Model' }),
    'gpt-test'
  )
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Save probe' })
  )
  expect(await screen.findByText('Backup probe')).toBeVisible()
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(api.put).toHaveBeenCalledWith('/api/channel/status/probes/', {
    enabled: true,
    probes: [
      probes.config.probes[0],
      expect.objectContaining({
        name: 'Backup probe',
        channel_id: 7,
        model: 'gpt-test',
        interval_seconds: 300,
        timeout_seconds: 30,
        enabled: true,
      }),
    ],
  })
})

it('a manual probe run queues the selected probe through its dedicated endpoint', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { task_id: 'task-one', status: 'pending' } },
  })
  vi.spyOn(toast, 'success')
  renderPage(100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Run probe' })
  )
  await waitFor(() =>
    expect(toast.success).toHaveBeenCalledWith('Probe run queued')
  )
  expect(api.post).toHaveBeenCalledWith(
    '/api/channel/status/probes/probe-one/run/'
  )
})

it('an invalid interval shows its field error and prevents a save request', async () => {
  vi.spyOn(api, 'put')
  renderPage(100)
  await userEvent.click(
    await screen.findByRole('button', { name: 'Edit probe' })
  )
  const dialog = await screen.findByRole('dialog')
  const interval = within(dialog).getByRole('spinbutton', {
    name: 'Interval (seconds)',
  })
  await userEvent.clear(interval)
  await userEvent.type(interval, '30')
  // Submit directly so the test also exercises validation without browser range constraints.
  const form = within(dialog)
    .getByRole('button', { name: 'Save probe' })
    .getAttribute('form')
  const event = new Event('submit', { bubbles: true, cancelable: true })
  await act(() =>
    document.querySelector(`form[id="${form ?? ''}"]`)?.dispatchEvent(event)
  )
  expect(
    await screen.findByText('Interval must be between 60 and 86400 seconds')
  ).toBeVisible()
  expect(interval).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
})
