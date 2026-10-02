// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, voiceAssistantDraft } from '../api/fixtures.ts';
import type { Device, Rule } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { removeRule, replaceRule, withDecision } from './edit.ts';
import { buildRows, cellAt, filterRows, gridMove, groupRows, tally } from './matrix.ts';

beforeEach(() => setLocale('en', { reload: false }));

const base = voiceAssistantDraft;
const catalog = devicesFixture;
const rows = buildRows(base, base, catalog.devices);
const row = (entityId: string, list = rows) => list.find((r) => r.device.entity_id === entityId);

describe('buildRows', () => {
  it('has one row per device with its actions in vocabulary order', () => {
    expect(rows).toHaveLength(catalog.devices.length);
    expect(row('light.kitchen')?.cells.map((c) => c.action)).toEqual(['read', 'turn_on', 'turn_off', 'set']);
    expect(row('lock.front_door')?.cells.map((c) => [c.action, c.cell.decision])).toEqual([
      ['read', 'ask'],
      ['lock', 'default'],
      ['unlock', 'ask'],
      ['open', 'default'],
    ]);
  });

  it('leaves out actions the device does not have or the vocabulary does not know', () => {
    const odd: Device = { entity_id: 'light.odd', name: 'Odd', category: 'light', area: null, actions: ['read', 'dance'] };
    expect(buildRows(base, base, [odd])[0]?.cells.map((c) => c.action)).toEqual(['read']);
  });

  it('marks what differs from the stored version', () => {
    const next = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'deny'));
    const changed = buildRows(next, base, catalog.devices);
    expect(changed.filter((r) => r.changed).map((r) => r.device.entity_id)).toEqual(['lock.front_door']);
    const unlock = cellAt(row('lock.front_door', changed)!, 'unlock');
    expect(unlock).toMatchObject({ changed: true, cell: { decision: 'deny' }, previous: { decision: 'ask' } });
    expect(cellAt(row('lock.front_door', changed)!, 'lock')?.changed).toBe(false);
    expect(cellAt(row('lock.front_door', changed)!, 'dance')).toBeUndefined();
  });

  it('counts a new "at times" outcome as a change', () => {
    // The media rule applies 07:00–22:00 only; without it media is default all day.
    expect(cellAt(row('media_player.living_room')!, 'play')?.cell).toMatchObject({ decision: 'default', timed: { decision: 'allow' } });
    const changed = buildRows(removeRule(base, 3), base, catalog.devices);
    expect(cellAt(row('media_player.living_room', changed)!, 'play')?.changed).toBe(true);
  });
});

describe('tally', () => {
  it('counts the cells per decision that always applies', () => {
    const counts = tally(rows);
    expect(counts.allow + counts.ask + counts.deny + counts.default).toBe(rows.reduce((n, r) => n + r.cells.length, 0));
    // Lights (2 × 4) and climate read + set_temperature.
    expect(counts.allow).toBe(10);
    expect(counts.deny).toBe(2);
    expect(counts.ask).toBe(2);
  });
});

describe('filterRows', () => {
  const ids = (query: string, onlyChanges = false, list = rows) => filterRows(list, { query, onlyChanges }, catalog).map((r) => r.device.entity_id);

  it('searches device name, entity_id, area name and category name', () => {
    expect(ids('haust')).toEqual(['lock.front_door']);
    expect(ids('LIVING_ROOM')).toEqual(['light.living_room', 'media_player.living_room']);
    expect(ids('küche')).toEqual(['light.kitchen']);
    expect(ids('  gate/garage ')).toEqual(['cover.garage_door']);
    expect(ids('nothing like this')).toEqual([]);
    expect(ids('')).toHaveLength(rows.length);
  });

  it('keeps only changed rows when asked', () => {
    const changed = buildRows(removeRule(base, 4), base, catalog.devices);
    expect(ids('', true)).toEqual([]);
    expect(ids('', true, changed)).toEqual(['camera.demo_camera']);
    expect(ids('light', true, changed)).toEqual([]);
  });
});

describe('groupRows', () => {
  it('groups by category in vocabulary order, one block each', () => {
    const groups = groupRows(rows, 'category', catalog);
    expect(groups.map((g) => g.key)).toEqual(['light', 'climate', 'gate', 'lock', 'alarm', 'camera', 'media', 'sensor', 'script']);
    expect(groups[0]).toMatchObject({ devices: 2, tally: { allow: 8, ask: 0, deny: 0, default: 0 } });
    expect(groups[0]?.blocks).toHaveLength(1);
    expect(groups[0]?.blocks[0]?.actions).toEqual(['read', 'turn_on', 'turn_off', 'set']);
  });

  it('groups by area in catalog order with a block per category; devices without an area come last', () => {
    const groups = groupRows(rows, 'area', catalog);
    expect(groups.map((g) => g.key)).toEqual(['kitchen', 'living_room', 'hallway', 'garage', '']);
    expect(groups[1]?.blocks.map((b) => b.category)).toEqual(['light', 'climate', 'media']);
    expect(groups[4]?.blocks.map((b) => b.rows.map((r) => r.device.entity_id))).toEqual([['alarm_control_panel.security'], ['sensor.outside_temperature'], ['script.demo']]);
  });

  it('puts devices of an area the catalog does not list with those without an area', () => {
    const stray: Device = { entity_id: 'light.attic', name: 'Attic', category: 'light', area: 'attic', actions: ['read'] };
    const groups = groupRows(buildRows(base, base, [stray]), 'area', catalog);
    expect(groups.map((g) => g.key)).toEqual(['']);
    expect(groups[0]?.blocks[0]?.actions).toEqual(['read']);
  });

  it('has no groups without rows', () => {
    expect(groupRows([], 'area', catalog)).toEqual([]);
  });
});

describe('gridMove', () => {
  const size = { rows: 12, cols: 4 };
  const ltr = { rtl: false, ctrl: false };
  const move = (key: string, row: number, col: number, options = ltr) => gridMove(key, { row, col }, size, options);

  it('moves with the arrow keys and stops at the edges', () => {
    expect(move('ArrowRight', 0, 0)).toEqual({ row: 0, col: 1 });
    expect(move('ArrowLeft', 0, 0)).toEqual({ row: 0, col: 0 });
    expect(move('ArrowRight', 0, 3)).toEqual({ row: 0, col: 3 });
    expect(move('ArrowDown', 11, 2)).toEqual({ row: 11, col: 2 });
    expect(move('ArrowUp', 5, 2)).toEqual({ row: 4, col: 2 });
  });

  it('mirrors the horizontal arrows right-to-left', () => {
    const rtl = { rtl: true, ctrl: false };
    expect(move('ArrowLeft', 0, 1, rtl)).toEqual({ row: 0, col: 2 });
    expect(move('ArrowRight', 0, 1, rtl)).toEqual({ row: 0, col: 0 });
    expect(move('ArrowDown', 0, 1, rtl)).toEqual({ row: 1, col: 1 });
  });

  it('jumps with Home, End and the page keys', () => {
    expect(move('Home', 5, 2)).toEqual({ row: 5, col: 0 });
    expect(move('End', 5, 2)).toEqual({ row: 5, col: 3 });
    expect(move('Home', 5, 2, { rtl: false, ctrl: true })).toEqual({ row: 0, col: 0 });
    expect(move('End', 5, 2, { rtl: false, ctrl: true })).toEqual({ row: 11, col: 3 });
    expect(move('PageDown', 0, 1)).toEqual({ row: 10, col: 1 });
    expect(move('PageDown', 5, 1)).toEqual({ row: 11, col: 1 });
    expect(move('PageUp', 5, 1)).toEqual({ row: 0, col: 1 });
  });

  it('leaves other keys alone', () => {
    expect(move('Enter', 0, 0)).toBeNull();
    expect(move('a', 0, 0)).toBeNull();
    expect(move('Tab', 0, 0)).toBeNull();
  });
});
