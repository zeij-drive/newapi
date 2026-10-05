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
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useTopNavLinks } from '@/hooks/use-top-nav-links'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

let queryClient: QueryClient

beforeEach(() => {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  useAuthStore.getState().auth.reset()
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { HeaderNavModules: {} },
    },
  })
})

afterEach(() => {
  queryClient.clear()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      {props.children}
    </QueryClientProvider>
  )
}

describe.each([
  { title: 'Channel status', href: '/channel-status' },
  { title: 'Infinite Canvas', href: '/infinite-canvas' },
])('$title navigation', ({ title, href }) => {
  it.each([
    { name: 'signed-out visitor', user: null, requiresAuth: true },
    {
      name: 'authenticated user',
      user: { id: 1, username: 'status-user', role: 1 },
      requiresAuth: false,
    },
  ])('adds the link for a $name', async ({ user, requiresAuth }) => {
    if (user) useAuthStore.getState().auth.setUser(user)

    const { result } = renderHook(() => useTopNavLinks(), { wrapper })

    await waitFor(() => {
      expect(result.current).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ title, href, requiresAuth }),
        ])
      )
    })
  })
})
