import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { UserUsageSection } from '../user-usage-section'

test('selecting a period shows its site total and user order', async () => {
  const totals: Record<string, number> = { today: 10, week: 20, all: 30 }
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const period = String(config?.params?.period)
    return {
      data: {
        success: true,
        data: {
          period,
          start: 0,
          end: 1,
          total_tokens: totals[period] ?? 0,
          users: [{ user_id: 3, name: 'User #3', total_tokens: 1 }],
        },
      },
    }
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={queryClient}>
      <UserUsageSection />
    </QueryClientProvider>
  )

  expect(await screen.findByText('20')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Today' }))
  expect(await screen.findByText('10')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'All time' }))
  expect(await screen.findByText('30')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'All time' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(api.get).toHaveBeenCalledWith('/api/usage-ranking', {
    params: { period: 'all' },
  })
})
