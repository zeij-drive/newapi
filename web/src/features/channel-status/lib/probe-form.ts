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

export function probeFormSchema(t: TFunction) {
  return z
    .object({
      id: z.string().optional(),
      name: z
        .string()
        .trim()
        .min(1, t('Probe name is required'))
        .max(80, t('Probe name must be at most 80 characters')),
      channel_id: z.number().int().positive(t('Select a channel')),
      model: z
        .string()
        .trim()
        .min(1, t('Model is required'))
        .max(200, t('Model must be at most 200 characters')),
      interval_seconds: z
        .number()
        .int()
        .min(60, t('Interval must be between 60 and 86400 seconds'))
        .max(86400, t('Interval must be between 60 and 86400 seconds')),
      timeout_seconds: z
        .number()
        .int()
        .min(5, t('Timeout must be between 5 and 300 seconds'))
        .max(300, t('Timeout must be between 5 and 300 seconds')),
      prompt: z.string().max(2000, t('Prompt must be at most 2000 characters')),
      enabled: z.boolean(),
      endpoint_type: z.string().optional(),
      is_stream: z.boolean().optional(),
    })
    .refine((probe) => probe.timeout_seconds <= probe.interval_seconds, {
      path: ['timeout_seconds'],
      message: t('Timeout cannot exceed the probe interval'),
    })
}

export type ProbeFormValues = z.infer<ReturnType<typeof probeFormSchema>>

export const DEFAULT_PROBE: ProbeFormValues = {
  name: '',
  channel_id: 0,
  model: '',
  endpoint_type: '',
  is_stream: false,
  interval_seconds: 300,
  timeout_seconds: 30,
  prompt: '',
  enabled: true,
}
