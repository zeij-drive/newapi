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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { getImageGenerationEndpoint } from './image-generation'

export function createImageGenerationSchema(t: TFunction) {
  return z.object({
    baseUrl: z
      .string()
      .trim()
      .refine((value) => {
        try {
          getImageGenerationEndpoint(value)
          return true
        } catch {
          return false
        }
      }, t('Enter an HTTPS API base URL without credentials, query or fragment. HTTP is available for localhost.')),
    apiKey: z
      .string()
      .trim()
      .min(1, t('API key is required.'))
      .max(8192, t('API key is too long.'))
      .refine(
        (value) => !/[\r\n]/.test(value),
        t('API key cannot contain line breaks.')
      ),
    model: z
      .string()
      .trim()
      .min(1, t('Model is required.'))
      .max(200, t('Model name is too long.')),
    prompt: z
      .string()
      .trim()
      .min(1, t('Prompt is required.'))
      .max(10000, t('Prompt is too long.')),
  })
}

export type ImageGenerationValues = z.infer<
  ReturnType<typeof createImageGenerationSchema>
>
