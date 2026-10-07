import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { EpayGatewayEditor } from '../epay-gateway-editor'

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          id: 'primary',
          name: 'Renamed',
          address: 'https://pay.example.com',
          merchant_id: 'merchant',
          enabled: true,
          key_set: true,
        },
      ],
    },
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

test('failed gateway load requires retry before saving', async () => {
  const get = vi.spyOn(api, 'get')
  get.mockResolvedValueOnce({
    data: { success: false, message: 'Load failed' },
  })
  get.mockResolvedValueOnce({
    data: {
      success: true,
      data: [
        {
          id: 'primary',
          name: 'Primary',
          address: 'https://pay.example.com',
          merchant_id: 'merchant',
          enabled: true,
          key_set: true,
        },
      ],
    },
  })

  render(<EpayGatewayEditor />)
  const user = userEvent.setup()

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to load settings'
  )
  expect(
    screen.queryByRole('button', { name: 'Save gateways' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))

  expect(
    await screen.findByRole('textbox', { name: 'Gateway name' })
  ).toHaveValue('Primary')
  expect(
    screen.queryByRole('textbox', { name: 'Gateway ID' })
  ).not.toBeInTheDocument()
  expect(screen.getByLabelText('Epay secret key')).toHaveValue('')

  await user.clear(screen.getByRole('textbox', { name: 'Gateway name' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Gateway name' }),
    'Renamed'
  )
  await user.click(screen.getByRole('button', { name: 'Save gateways' }))

  expect(api.put).toHaveBeenCalledWith('/api/option/epay_gateways', {
    gateways: [
      {
        id: 'primary',
        name: 'Renamed',
        address: 'https://pay.example.com',
        merchant_id: 'merchant',
        enabled: true,
        key_set: true,
        key: '',
      },
    ],
  })
})

test('successful response without gateway data cannot erase saved gateways', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true } })

  render(<EpayGatewayEditor />)

  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Failed to load settings'
  )
  expect(
    screen.queryByRole('button', { name: 'Save gateways' })
  ).not.toBeInTheDocument()
  expect(api.put).not.toHaveBeenCalled()
})

test('adding two gateways saves distinct IDs and secret keys', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })

  render(<EpayGatewayEditor />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Add gateway' }))
  await user.click(screen.getByRole('button', { name: 'Add gateway' }))

  const names = screen.getAllByRole('textbox', { name: 'Gateway name' })
  const endpoints = screen.getAllByRole('textbox', { name: 'Epay endpoint' })
  const merchantIDs = screen.getAllByRole('textbox', {
    name: 'Epay merchant ID',
  })
  const keys = screen.getAllByLabelText('Epay secret key')
  await user.type(names[0], 'Primary')
  await user.type(endpoints[0], 'https://primary.example.com')
  await user.type(merchantIDs[0], 'merchant-primary')
  await user.type(keys[0], 'primary-secret')
  await user.type(names[1], 'Backup')
  await user.type(endpoints[1], 'https://backup.example.com')
  await user.type(merchantIDs[1], 'merchant-backup')
  await user.type(keys[1], 'backup-secret')
  await user.click(screen.getByRole('button', { name: 'Save gateways' }))

  expect(api.put).toHaveBeenCalledWith('/api/option/epay_gateways', {
    gateways: [
      expect.objectContaining({ name: 'Primary', key: 'primary-secret' }),
      expect.objectContaining({ name: 'Backup', key: 'backup-secret' }),
    ],
  })
  const sent = vi.mocked(api.put).mock.calls[0][1] as {
    gateways: Array<{ id: string }>
  }
  expect(sent.gateways[0].id).not.toBe(sent.gateways[1].id)
})
