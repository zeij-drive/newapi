import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { UsageRankingRewardsSection } from '../usage-ranking-rewards-section'

const initialSettings = {
  enabled: false,
  enabled_at: 0,
  daily: ['', '', ''],
  weekly: ['', '', ''],
  monthly: ['', '', ''],
  yearly: ['', '', ''],
}

function Fixture() {
  const [actionsContainer, setActionsContainer] =
    useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setActionsContainer} />
      <SettingsPageProvider actionsContainer={actionsContainer}>
        <UsageRankingRewardsSection />
      </SettingsPageProvider>
    </>
  )
}

async function renderRewards() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <Fixture />
    </QueryClientProvider>
  )
  return screen.findByRole('spinbutton', { name: 'Daily Rank 1' })
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: initialSettings },
  })
  vi.spyOn(api, 'put').mockImplementation(async (_url, settings) => ({
    data: { success: true, data: settings },
  }))
})

test('editing independent period awards saves all twelve amounts', async () => {
  const user = userEvent.setup()
  const dailyFirst = await renderRewards()
  await user.type(dailyFirst, '1.25')
  await user.type(
    screen.getByRole('spinbutton', { name: 'Weekly Rank 2' }),
    '0.50'
  )
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  await waitFor(() => {
    expect(api.put).toHaveBeenCalledWith(
      '/api/option/usage-ranking-rewards',
      expect.objectContaining({
        daily: ['1.25', '', ''],
        weekly: ['', '0.5', ''],
      })
    )
  })
})

test('invalid amount prevents the reward settings request', async () => {
  const user = userEvent.setup()
  const dailyFirst = await renderRewards()
  await user.type(dailyFirst, '1.234')
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))

  expect(api.put).not.toHaveBeenCalled()
})
