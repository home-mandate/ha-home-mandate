// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { Device, DeviceCatalog } from '../api/types.ts';
import { MATCH_LIMIT, criticalGroups } from './critical.ts';

const device = (entity_id: string, name: string, extra: Partial<Device> = {}): Device => ({
  entity_id,
  name,
  category: 'switch',
  area: null,
  actions: ['read', 'turn_off', 'turn_on'],
  critical: false,
  suggest_critical: false,
  ...extra,
});

const catalog: DeviceCatalog = {
  areas: [{ id: 'hall', name: 'Flur' }],
  devices: [
    device('switch.zeta', 'Zeta'),
    device('switch.cellar', 'Kellertür', { critical: true }),
    device('switch.gate', 'Hoftor', { suggest_critical: true }),
    device('switch.alpha', 'Alpha', { area: 'hall', critical: true }),
    device('sensor.temp', 'Temperatur', { category: 'sensor', actions: ['read'] }),
    device('switch.coffee', 'Kaffee', { area: 'hall' }),
  ],
};

const ids = (list: Device[]) => list.map((d) => d.entity_id);

describe('criticalGroups', () => {
  it('lists the marked devices and the proposals by name, nothing else without a search', () => {
    const g = criticalGroups(catalog, '');
    expect(ids(g.marked)).toEqual(['switch.alpha', 'switch.cellar']);
    expect(ids(g.suggested)).toEqual(['switch.gate']);
    expect(g.matches).toEqual([]);
    expect(g.more).toBe(0);
  });

  it('finds unmarked devices by name, entity ID or area, ignoring case and blanks', () => {
    expect(ids(criticalGroups(catalog, ' KAFF ').matches)).toEqual(['switch.coffee']);
    expect(ids(criticalGroups(catalog, 'switch.ze').matches)).toEqual(['switch.zeta']);
    expect(ids(criticalGroups(catalog, 'flur').matches)).toEqual(['switch.coffee']);
  });

  it('never offers devices that can only be read: there is nothing to protect', () => {
    expect(criticalGroups(catalog, 'temp').matches).toEqual([]);
  });

  it('keeps a marked device that can only be read in the list, so its mark can be removed', () => {
    const odd: DeviceCatalog = { areas: [], devices: [device('sensor.door', 'Tür', { category: 'sensor', actions: ['read'], critical: true })] };
    expect(ids(criticalGroups(odd, '').marked)).toEqual(['sensor.door']);
  });

  it('does not repeat marked devices or proposals among the matches', () => {
    expect(criticalGroups(catalog, 'switch').matches.map((d) => d.entity_id)).toEqual(['switch.coffee', 'switch.zeta']);
  });

  it('bounds the matches and says how many more there are', () => {
    const many: DeviceCatalog = { areas: [], devices: Array.from({ length: MATCH_LIMIT + 3 }, (_, i) => device(`switch.s${i}`, `Steckdose ${i}`)) };
    const g = criticalGroups(many, 'steckdose');
    expect(g.matches).toHaveLength(MATCH_LIMIT);
    expect(g.more).toBe(3);
  });

  it('searches the cleaned name, not hidden characters in it', () => {
    const hostile: DeviceCatalog = { areas: [], devices: [device('switch.x', 'Ga\u200Brage')] };
    expect(ids(criticalGroups(hostile, 'garage').matches)).toEqual(['switch.x']);
  });
});
