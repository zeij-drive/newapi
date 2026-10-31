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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { Sub2APIRatioSyncState } from '@/features/system-settings/models/sub2api-ratio-sync-api'

import { ConnectionCard, type ConnectionValues } from '../connection-card'

const state: Sub2APIRatioSyncState = {
  settings: {
    upstream_url: 'https://sub2api.example',
    interval_seconds: 60,
    enabled: false,
    mappings: [],
    mapping_scope: 'default',
  },
  runtime: {
    groups: [],
    ratios: {},
    warnings: [],
    failures: 0,
    last_run: 0,
    next_run: 0,
    last_result: null,
    logs: [],
    backups: [],
  },
  auth: { upstream: false, refresh: false, pending_2fa: false },
  server_time: 0,
}

function renderConnectionCard(
  onSave: (values: ConnectionValues) => Promise<unknown>
) {
  const onLogin = vi.fn().mockResolvedValue({
    requires_2fa: false,
    refresh: true,
  })
  const onLogin2FA = vi.fn().mockResolvedValue({
    requires_2fa: false,
    refresh: true,
  })
  render(
    <ConnectionCard
      state={state}
      busy={false}
      loginUrl=''
      helperBusy={false}
      onSave={onSave}
      onImportTokens={vi.fn().mockResolvedValue(undefined)}
      onClearTokens={vi.fn()}
      onLogin={onLogin}
      onLogin2FA={onLogin2FA}
      onLogout={vi.fn().mockResolvedValue(undefined)}
      onOpenLogin={vi.fn()}
      onGetHelper={vi.fn().mockResolvedValue('')}
    />
  )
  return {
    urlInput: screen.getByLabelText('Sub2API base URL'),
    onLogin,
    onLogin2FA,
  }
}

describe('Sub2API connection URL validation', () => {
  it('rejects remote HTTP URLs before saving', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { urlInput } = renderConnectionCard(onSave)

    await user.clear(urlInput)
    await user.type(urlInput, 'http://sub2api.example')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))

    expect(
      await screen.findByText('Enter a valid HTTP or HTTPS URL.')
    ).toBeVisible()
    expect(
      screen.getByText(
        'Remote URLs require HTTPS; HTTP is allowed only for localhost or loopback addresses.'
      )
    ).toBeVisible()
    expect(urlInput).toHaveAttribute(
      'aria-describedby',
      'sub2api-upstream-url-hint sub2api-upstream-url-error'
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter a valid HTTP or HTTPS URL.'
    )
    expect(onSave).not.toHaveBeenCalled()
  })

  it('allows localhost HTTP for local development', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { urlInput } = renderConnectionCard(onSave)

    await user.clear(urlInput)
    await user.type(urlInput, 'http://localhost:3000')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith({
        upstream_url: 'http://localhost:3000',
        interval_seconds: 60,
      })
    )
  })

  it('allows HTTPS for remote upstreams', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { urlInput } = renderConnectionCard(onSave)

    await user.clear(urlInput)
    await user.type(urlInput, 'https://new-sub2api.example')
    await user.click(screen.getByRole('button', { name: 'Save connection' }))

    await waitFor(() =>
      expect(onSave).toHaveBeenCalledWith({
        upstream_url: 'https://new-sub2api.example',
        interval_seconds: 60,
      })
    )
  })

  it('sends account credentials and captcha JSON to the upstream login action', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { onLogin } = renderConnectionCard(onSave)

    await user.click(screen.getByRole('button', { name: 'Use account login' }))
    await user.type(screen.getByLabelText('Email'), 'admin@example.com')
    await user.type(screen.getByLabelText('Password'), 'secret-password')
    await user.click(screen.getByLabelText('Captcha fields (optional JSON)'))
    await user.paste('{"turnstile_token":"captcha"}')
    await user.click(screen.getByRole('button', { name: 'Login' }))

    await waitFor(() =>
      expect(onLogin).toHaveBeenCalledWith({
        email: 'admin@example.com',
        password: 'secret-password',
        captcha_json: '{"turnstile_token":"captcha"}',
      })
    )
  })

  it('switches to two-factor verification when upstream requests it', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { onLogin, onLogin2FA } = renderConnectionCard(onSave)
    onLogin.mockResolvedValue({
      requires_2fa: true,
      email: 'a***@example.com',
      refresh: false,
    })

    await user.click(screen.getByRole('button', { name: 'Use account login' }))
    await user.type(screen.getByLabelText('Email'), 'admin@example.com')
    await user.type(screen.getByLabelText('Password'), 'secret-password')
    await user.click(screen.getByRole('button', { name: 'Login' }))

    expect(await screen.findByLabelText('Verification code')).toBeVisible()
    await user.type(screen.getByLabelText('Verification code'), '123456')
    await user.click(screen.getByRole('button', { name: 'Complete login' }))

    await waitFor(() => expect(onLogin2FA).toHaveBeenCalledWith('123456'))
  })
})
