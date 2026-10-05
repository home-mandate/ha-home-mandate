// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { AuditEntry, DeviceCatalog } from '../api/types.ts';
import { auditFixture, devicesFixture } from '../api/fixtures.ts';
import { decisionOf, deviceName, isAdminEvent, isCriticalRequest, directoryText } from './describe.ts';

const bySeq = (seq: number): AuditEntry => {
  const entry = auditFixture.find((e) => e.seq === seq);
  if (!entry) throw new Error(`no fixture entry ${seq}`);
  return entry;
};

describe('decisionOf', () => {
  it('reads the decision; deny without a matching rule is the default', () => {
    expect(decisionOf(bySeq(3))).toBe('allow');
    expect(decisionOf(bySeq(4))).toBe('ask');
    expect(decisionOf(bySeq(5))).toBe('deny');
    expect(decisionOf(bySeq(6))).toBe('default');
  });

  it('has no decision for administrative events and for requests refused before the evaluation', () => {
    expect(decisionOf(bySeq(1))).toBeNull();
    expect(decisionOf(bySeq(12))).toBeNull();
    const early: AuditEntry = { ...bySeq(14), evaluation: undefined, result: { status: 'denied', denied_by: 'rate_limit' } };
    expect(decisionOf(early)).toBeNull();
  });
});

describe('isAdminEvent', () => {
  it('separates requests from everything else', () => {
    expect(isAdminEvent(bySeq(3))).toBe(false);
    for (const seq of [1, 2, 12, 13, 15]) expect(isAdminEvent(bySeq(seq))).toBe(true);
  });
});

describe('isCriticalRequest', () => {
  it('follows the vocabulary for the category and action of the request', () => {
    expect(isCriticalRequest(bySeq(4))).toBe(true); // lock.unlock
    expect(isCriticalRequest(bySeq(5))).toBe(true); // camera.snapshot
    expect(isCriticalRequest(bySeq(9))).toBe(true); // gate.open
    expect(isCriticalRequest(bySeq(3))).toBe(false); // light.turn_on
  });

  it('counts a request without category or with an unknown one as critical, never as harmless', () => {
    const base = bySeq(3);
    const req = base.request;
    if (!req) throw new Error('fixture without request');
    expect(isCriticalRequest({ ...base, request: { ...req, resource: { entity_id: 'light.x' } } })).toBe(true);
    expect(isCriticalRequest({ ...base, request: { ...req, resource: { entity_id: 'x.y', category: 'vendor:robot' } } })).toBe(true);
    expect(isCriticalRequest(bySeq(1))).toBe(false); // no request at all
  });

  it('counts every action but read on a device marked as critical (resource.critical)', () => {
    const base = bySeq(3);
    const req = base.request;
    if (!req) throw new Error('fixture without request');
    const marked = { ...req.resource, critical: true };
    expect(isCriticalRequest({ ...base, request: { ...req, resource: marked } })).toBe(true);
    expect(isCriticalRequest({ ...base, request: { ...req, resource: marked, action: 'read' } })).toBe(false);
  });
});

describe('deviceName', () => {
  const catalog: DeviceCatalog = devicesFixture;

  it('shows the name from Home Assistant, else the entity ID', () => {
    const known = catalog.devices[0];
    if (!known) throw new Error('empty fixture');
    expect(deviceName(known.entity_id, catalog)).toBe(known.name);
    expect(deviceName('light.gone', catalog)).toBe('light.gone');
    expect(deviceName('light.gone', null)).toBe('light.gone');
  });

  it('never shows hidden characters or line breaks of a device name', () => {
    const hostile: DeviceCatalog = { areas: [], devices: [{ entity_id: 'light.x', name: 'Lamp\u202E\nkcab', category: 'light', area: null, actions: [], critical: false, suggest_critical: false }] };
    expect(deviceName('light.x', hostile)).toBe('Lamp kcab');
  });
});

describe('directoryText', () => {
  const entry = (directory: AuditEntry['directory']): AuditEntry => ({
    id: 'x', seq: 1, recorded_at: '2026-10-05T08:00:00.000Z', event: 'directory.changed',
    actor: { kind: 'system', id: 'directory' }, directory, digest: 'sha256:x', prev: null,
  });
  it('says what changed in the directory, with the device name and the former ID', () => {
    const catalog = devicesFixture;
    expect(directoryText(entry({ change: 'critical_marked', entity_id: 'lock.front_door' }), catalog)).toMatch(/^.Haustür. marked as critical$/);
    expect(directoryText(entry({ change: 'renamed', entity_id: 'lock.front_door', previous_entity_id: 'lock.old_door' }), catalog)).toMatch(
      /^.lock\.old_door. renamed to .Haustür. in Home Assistant$/,
    );
    expect(directoryText(entry({ change: 'rename_dismissed', entity_id: 'lock.gone', previous_entity_id: 'lock.old' }), null)).toMatch(/not taken over$/);
    expect(directoryText({ ...entry(undefined), event: 'decision' }, catalog)).toBe('');
  });
});
