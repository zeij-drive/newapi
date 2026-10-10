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
// ----------------------------------------------------------------------------
// Rankings formatting helpers
// ----------------------------------------------------------------------------

import i18next from 'i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

/** Token notation uses 万 (10,000) and 亿 (100,000,000), capped at 亿. */
export function formatTokens(
  value: number,
  locale = toIntlLocale(i18next.resolvedLanguage || i18next.language)
): string {
  if (!Number.isFinite(value) || value <= 0) return '0'
  if (value >= 99_999_950)
    return `${formatNumber(value / 100_000_000, locale)}亿`
  if (value >= 10_000) return `${formatNumber(value / 10_000, locale)}万`
  return formatNumber(value, locale)
}

/** Format a 0..1 share as a percentage with two decimals. */
export function formatShare(share: number): string {
  if (!Number.isFinite(share) || share <= 0) return '0%'
  if (share < 0.001) return '<0.1%'
  return `${(share * 100).toFixed(share < 0.01 ? 2 : 1)}%`
}

/** Format a release date like `Oct 12, 2025`. */
export function formatReleaseDate(iso: string): string {
  const ts = Date.parse(iso)
  if (!Number.isFinite(ts)) return iso
  return new Date(ts).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}
