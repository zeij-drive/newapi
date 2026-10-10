import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import type { UsageRankingSnapshot } from '../../api'
import type { RankingPeriod } from '../../types'
import { UserUsageSection } from '../user-usage-section'

const rankedUsers: UsageRankingSnapshot['users'] = [
  { user_id: 1, name: 'Champion', total_tokens: 1_200_000 },
  { user_id: 2, name: 'Runner up', total_tokens: 900_000 },
  { user_id: 3, name: 'Third place', total_tokens: 600_000 },
  { user_id: 4, name: 'Fourth place', total_tokens: 300_000 },
]

function rankingResponse(users = rankedUsers, total = 4_000_000) {
  return {
    data: {
      success: true,
      data: { period: 'week', start: 0, end: 1, total_tokens: total, users },
    },
  }
}

function renderLeaderboard(period: RankingPeriod = 'week') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<UserUsageSection period={period} />, {
    wrapper: (props) => (
      <QueryClientProvider client={queryClient}>
        {props.children}
      </QueryClientProvider>
    ),
  })
}

test('switching the interface language updates token number formatting', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(
    rankingResponse([{ ...rankedUsers[0], total_tokens: 1234567 }])
  )
  renderLeaderboard()
  expect(await screen.findByText('123.46万')).toBeVisible()
  try {
    await act(() => i18next.changeLanguage('fr'))
    expect(screen.getByText('123,46万')).toBeVisible()
  } finally {
    await act(() => i18next.changeLanguage('en'))
  }
})

test('the page period controls user rankings and site totals without a separate filter', async () => {
  const snapshots: Record<RankingPeriod, { total: number; name: string }> = {
    today: { total: 10, name: 'Daily leader' },
    week: { total: 20, name: 'Weekly leader' },
    month: { total: 30, name: 'Monthly leader' },
    year: { total: 40, name: 'Yearly leader' },
  }
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const period = config?.params?.period as RankingPeriod
    const snapshot = snapshots[period]
    return {
      data: {
        success: true,
        data: {
          period,
          start: 0,
          end: 1,
          total_tokens: snapshot.total,
          users: [{ user_id: 3, name: snapshot.name, total_tokens: 1 }],
        },
      },
    }
  })
  const view = renderLeaderboard('month')
  expect(await screen.findByText('Monthly leader')).toBeVisible()
  expect(screen.getByText('30')).toBeVisible()
  expect(
    screen.queryByRole('group', { name: 'Period' })
  ).not.toBeInTheDocument()
  expect(api.get).toHaveBeenCalledWith('/api/usage-ranking', {
    params: { period: 'month' },
  })

  for (const period of ['today', 'year', 'week', 'month'] as const) {
    view.rerender(<UserUsageSection period={period} />)
    expect(await screen.findByText(snapshots[period].name)).toBeVisible()
    expect(screen.getByText(String(snapshots[period].total))).toBeVisible()
    expect(api.get).toHaveBeenCalledWith('/api/usage-ranking', {
      params: { period },
    })
  }
})

test('the top three occupy the podium and the remaining list starts at fourth place', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(rankingResponse())
  renderLeaderboard()

  const podium = await screen.findByRole('list', {
    name: 'User token leaderboard',
  })
  expect(
    within(podium)
      .getAllByRole('listitem')
      .map((item) => item.getAttribute('aria-label'))
  ).toEqual(['Rank 1', 'Rank 2', 'Rank 3'])
  expect(
    within(screen.getByRole('listitem', { name: 'Rank 1' })).getByText(
      'Champion'
    )
  ).toBeVisible()
  expect(within(podium).getByText('120万')).toBeVisible()
  const fourth = screen.getByRole('listitem', { name: 'Rank 4' })
  expect(within(fourth).getByText('Fourth place')).toBeVisible()
  expect(fourth.parentElement).toHaveAttribute('start', '4')
  expect(screen.getByText('400万')).toBeVisible()
})

test('the podium stacks in ranking order on mobile and places the champion in the center on desktop', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(rankingResponse())
  renderLeaderboard()

  const podium = await screen.findByRole('list', {
    name: 'User token leaderboard',
  })
  expect(podium).toHaveClass('grid-cols-1', 'sm:grid-cols-3')
  expect(screen.getByRole('listitem', { name: 'Rank 1' })).toHaveClass(
    'sm:col-start-2'
  )
  expect(screen.getByRole('listitem', { name: 'Rank 2' })).toHaveClass(
    'sm:col-start-1'
  )
  expect(screen.getByRole('listitem', { name: 'Rank 3' })).toHaveClass(
    'sm:col-start-3'
  )
})

test.each([1, 2])(
  'a leaderboard with %i users shows only occupied podium places',
  async (count) => {
    vi.spyOn(api, 'get').mockResolvedValue(
      rankingResponse(rankedUsers.slice(0, count))
    )
    renderLeaderboard()

    const podium = await screen.findByRole('list', {
      name: 'User token leaderboard',
    })
    expect(within(podium).getAllByRole('listitem')).toHaveLength(count)
    expect(
      screen.queryByRole('listitem', { name: 'Rank 3' })
    ).not.toBeInTheDocument()
    expect(screen.getAllByRole('list')).toHaveLength(1)
  }
)

test('a long username is available in full while its visible label truncates', async () => {
  const name = 'A very long display name '.repeat(10).trim()
  vi.spyOn(api, 'get').mockResolvedValue(
    rankingResponse([{ ...rankedUsers[0], name }])
  )
  renderLeaderboard()

  const label = await screen.findByText(name)
  expect(label).toHaveAttribute('title', name)
  expect(label).toHaveClass('truncate', 'min-w-0')
})

test('an empty period displays its zero site total and an empty state without podium placeholders', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(rankingResponse([], 0))
  renderLeaderboard()

  expect(await screen.findByText('No usage data yet')).toBeVisible()
  expect(screen.getByText('0')).toBeVisible()
  expect(screen.queryByRole('list')).not.toBeInTheDocument()
})

test('a failed request offers a retry that restores the leaderboard', async () => {
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue(rankingResponse())
  const user = userEvent.setup()
  renderLeaderboard()

  expect(await screen.findByText('Unable to load rankings')).toBeVisible()
  expect(screen.queryByText('Site-wide token usage')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('Champion')).toBeVisible()
})
