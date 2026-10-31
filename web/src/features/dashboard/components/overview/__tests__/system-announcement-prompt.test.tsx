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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AnnouncementItem } from '@/features/dashboard/types'
import { useNotificationStore } from '@/stores/notification-store'

import { SystemAnnouncementPrompt } from '../system-announcement-prompt'

const { announcementsHook } = vi.hoisted(() => ({
  announcementsHook: vi.fn(),
}))

vi.mock('@/features/dashboard/hooks/use-status-data', () => ({
  useAnnouncements: announcementsHook,
}))
vi.mock('@/components/ui/scroll-area', () => ({
  ScrollArea: (props: { children: ReactNode }) => <div>{props.children}</div>,
}))

const useAnnouncementsMock = vi.mocked(announcementsHook)
const announcement: AnnouncementItem = {
  id: 17,
  content: 'Scheduled maintenance starts tonight.',
  publishDate: '2026-10-31T10:00:00Z',
  type: 'warning',
}

beforeEach(() => {
  window.localStorage.clear()
  useNotificationStore.setState(useNotificationStore.getInitialState(), true)
  useAnnouncementsMock.mockReturnValue({
    items: [announcement],
    loading: false,
  })
})

afterEach(() => {
  cleanup()
  window.localStorage.clear()
  useNotificationStore.setState(useNotificationStore.getInitialState(), true)
})

describe('system announcement prompt', () => {
  it('opens on each visit after a normal close', async () => {
    const user = userEvent.setup()
    const firstVisit = render(<SystemAnnouncementPrompt />)
    await screen.findByRole('dialog')

    await user.click(screen.getByRole('button', { name: 'Close' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    firstVisit.unmount()

    render(<SystemAnnouncementPrompt />)
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('suppresses the same announcement for today and prompts again when its content changes', async () => {
    const user = userEvent.setup()
    const view = render(<SystemAnnouncementPrompt />)
    await screen.findByRole('dialog')

    await user.click(
      screen.getByRole('button', { name: 'Do not show again today' })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    view.unmount()

    const sameAnnouncement = render(<SystemAnnouncementPrompt />)
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    sameAnnouncement.rerender(<SystemAnnouncementPrompt />)

    useAnnouncementsMock.mockReturnValue({
      items: [{ ...announcement, content: 'Maintenance is complete.' }],
      loading: false,
    })
    sameAnnouncement.rerender(<SystemAnnouncementPrompt />)
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})
