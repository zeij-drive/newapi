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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { AffiliateRewardsSection } from '../affiliate-rewards-section'

type FixtureProps = {
  defaultValues?: {
    AffiliateRewardsEnabled: boolean
    AffiliateRewardPercent: number
  }
  complianceConfirmed?: boolean
}

function Fixture(props: FixtureProps) {
  const [actionsContainer, setActionsContainer] =
    useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setActionsContainer} />
      <SettingsPageProvider actionsContainer={actionsContainer}>
        <AffiliateRewardsSection
          defaultValues={
            props.defaultValues ?? {
              AffiliateRewardsEnabled: false,
              AffiliateRewardPercent: 0,
            }
          }
          complianceConfirmed={props.complianceConfirmed ?? true}
        />
      </SettingsPageProvider>
    </>
  )
}

function renderSettings(props: FixtureProps = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: () => <Fixture {...props} /> }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('enabling referral rewards saves the toggle and recharge percentage', async () => {
  const user = userEvent.setup()
  renderSettings()

  await user.click(
    await screen.findByRole('switch', { name: 'Enable invitation rewards' })
  )
  await user.clear(
    screen.getByRole('spinbutton', { name: 'Recharge reward percentage' })
  )
  await user.type(
    screen.getByRole('spinbutton', { name: 'Recharge reward percentage' }),
    '12.5'
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => {
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'AffiliateRewardsEnabled',
      value: true,
    })
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'AffiliateRewardPercent',
      value: 12.5,
    })
  })
})

test('shows the compliance notice for non-zero rewards when rewards are disabled', async () => {
  renderSettings({
    defaultValues: {
      AffiliateRewardsEnabled: false,
      AffiliateRewardPercent: 10,
    },
    complianceConfirmed: false,
  })

  expect(
    await screen.findByText(
      'Non-zero invitation rewards require compliance confirmation in Payment Gateway settings.'
    )
  ).toBeVisible()
})
