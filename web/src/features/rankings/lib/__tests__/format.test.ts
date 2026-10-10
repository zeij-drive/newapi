import { expect, test } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'

import { formatTokens } from '../format'

test.each([
  [0, '0'],
  [9999, '9,999'],
  [10000, '1万'],
  [125000, '12.5万'],
  [99999999, '1亿'],
  [100000000, '1亿'],
  [123456789, '1.23亿'],
  [1000000000000, '10,000亿'],
  [Number.NaN, '0'],
])('formats %s tokens with a maximum unit of 亿', (value, expected) => {
  expect(formatTokens(value, 'en-US')).toBe(expected)
})

test.each(['zhCN', 'zhTW', 'en', 'fr', 'ru', 'ja', 'vi', 'invalid'])(
  'supports the %s interface locale',
  (language) => {
    const locale = toIntlLocale(language)
    expect(formatTokens(1234567, locale)).toBe(
      `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(123.4567)}万`
    )
  }
)
