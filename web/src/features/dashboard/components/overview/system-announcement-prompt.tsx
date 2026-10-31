/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { RichContent } from '@/components/rich-content'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useAnnouncements } from '@/features/dashboard/hooks/use-status-data'
import { formatDateTimeObject } from '@/lib/time'
import { useNotificationStore } from '@/stores/notification-store'

function localDateKey(date = new Date()): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function SystemAnnouncementPrompt() {
  const { t } = useTranslation()
  const { items, loading } = useAnnouncements()
  const dismissedAnnouncement = useNotificationStore(
    (state) => state.dismissedAnnouncement
  )
  const dismissAnnouncement = useNotificationStore(
    (state) => state.dismissAnnouncement
  )
  const [open, setOpen] = useState(false)
  const [seenFingerprint, setSeenFingerprint] = useState('')

  const fingerprint = useMemo(
    () =>
      JSON.stringify(
        items.map((item) => ({
          id: item.id ?? null,
          content: item.content.trim(),
          extra: item.extra?.trim() ?? '',
          publishDate: item.publishDate ?? '',
          type: item.type ?? 'default',
        }))
      ),
    [items]
  )

  useEffect(() => {
    if (loading) return
    if (items.length === 0) {
      setOpen(false)
      setSeenFingerprint('')
      return
    }
    if (seenFingerprint === fingerprint) return

    setSeenFingerprint(fingerprint)
    const alreadyDismissed =
      dismissedAnnouncement?.fingerprint === fingerprint &&
      dismissedAnnouncement.date === localDateKey()
    setOpen(!alreadyDismissed)
  }, [
    dismissedAnnouncement,
    fingerprint,
    items.length,
    loading,
    seenFingerprint,
  ])

  const handleDismissForToday = () => {
    dismissAnnouncement(fingerprint, localDateKey())
    setOpen(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) setOpen(true)
      }}
      title={t('System Announcements')}
      description={t('Latest platform updates and notices')}
      contentClassName='sm:max-w-2xl'
      contentHeight='auto'
      bodyClassName='space-y-4'
      showCloseButton={false}
      footer={
        <>
          <Button variant='outline' onClick={() => setOpen(false)}>
            {t('Close')}
          </Button>
          <Button onClick={handleDismissForToday}>
            {t('Do not show again today')}
          </Button>
        </>
      }
    >
      <ScrollArea className='max-h-[min(62vh,36rem)] pr-3'>
        <div className='space-y-5'>
          {items.map((item, index) => (
            <article
              key={item.id ?? `${item.publishDate ?? ''}-${index}`}
              className='space-y-2'
            >
              {item.publishDate ? (
                <time className='text-muted-foreground text-sm'>
                  {formatDateTimeObject(new Date(item.publishDate))}
                </time>
              ) : null}
              <div className='text-sm leading-relaxed'>
                <RichContent breaks content={item.content} />
              </div>
              {item.extra ? (
                <div className='text-muted-foreground text-sm leading-relaxed'>
                  <RichContent breaks content={item.extra} />
                </div>
              ) : null}
            </article>
          ))}
        </div>
      </ScrollArea>
    </Dialog>
  )
}
