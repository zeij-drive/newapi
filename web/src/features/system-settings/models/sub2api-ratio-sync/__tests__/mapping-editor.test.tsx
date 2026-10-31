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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type {
  Sub2APIRatioSyncGroup,
  Sub2APIRatioSyncMapping,
} from '@/features/system-settings/models/sub2api-ratio-sync-api'

import { MappingEditor } from '../mapping-editor'

const groups: Sub2APIRatioSyncGroup[] = [
  {
    id: 1,
    name: 'Standard',
    canonical_name: 'standard',
    default: 1,
    custom: null,
    effective: 1,
    peak: false,
  },
]
const mapping: Sub2APIRatioSyncMapping = {
  source_id: 1,
  target: 'default',
  mode: 'effective',
  value: 1,
  enabled: true,
  allow_peak: false,
}

describe('Sub2API group mapping editor', () => {
  it('blocks saving when a new mapping duplicates a target group', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(
      <MappingEditor
        groups={groups}
        targets={{ default: 1, premium: 2 }}
        savedMappings={[mapping]}
        busy={false}
        onSave={onSave}
        onRefreshGroups={vi.fn()}
      />
    )

    await user.click(screen.getByRole('button', { name: 'Add mapping' }))
    await user.click(screen.getByRole('button', { name: 'Save mappings' }))

    expect(
      screen.getByText(
        'Check that each mapping is valid and target groups are unique.'
      )
    ).toBeVisible()
    expect(onSave).not.toHaveBeenCalled()
  })
})
