// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { Approver, ApproverCandidates } from '../api/types.ts';
import {
  freeDevices,
  isLastReachable,
  newApprover,
  summary,
  updateOf,
  withDevice,
  withDeviceCritical,
  withoutDevice,
  withUi,
} from './approvers.ts';

const candidates: ApproverCandidates = {
  people: [],
  devices: [
    { service: 'mobile_app_pixel', name: 'Pixel', suggest_critical: true },
    { service: 'mobile_app_mac', name: 'Mac', suggest_critical: false },
  ],
};

const approver = (user: string, reach: Approver['reach'], extra: Partial<Approver> = {}): Approver => ({
  user_id: user,
  name: user,
  devices: [{ service: 'mobile_app_pixel', critical: true }],
  ui: false,
  ui_critical: false,
  language: null,
  reach,
  ...extra,
});

describe('approver changes', () => {
  it('adds a device with its suggestion and lists only free ones', () => {
    const u = updateOf(approver('a', { normal: true, critical: true }));
    expect(freeDevices(u, candidates).map((d) => d.service)).toEqual(['mobile_app_mac']);
    const more = withDevice(u, candidates.devices[1] as ApproverCandidates['devices'][number]);
    expect(more.devices).toEqual([
      { service: 'mobile_app_pixel', critical: true },
      { service: 'mobile_app_mac', critical: false },
    ]);
    expect(freeDevices(more, candidates)).toEqual([]);
    expect(withoutDevice(more, 'mobile_app_pixel').devices.map((d) => d.service)).toEqual(['mobile_app_mac']);
    expect(withDeviceCritical(more, 'mobile_app_mac', true).devices[1]?.critical).toBe(true);
  });

  it('takes critical requests in the UI away with the UI', () => {
    const u = { ...updateOf(approver('a', { normal: true, critical: true })), ui: true, ui_critical: true };
    expect(withUi(u, false)).toMatchObject({ ui: false, ui_critical: false });
    expect(withUi({ ...u, ui: false, ui_critical: false }, true)).toMatchObject({ ui: true, ui_critical: false });
  });

  it('starts a new person with the first free device, or the UI for an admin without one', () => {
    expect(newApprover(false, candidates.devices)?.devices).toEqual([{ service: 'mobile_app_pixel', critical: true }]);
    expect(newApprover(true, [])).toMatchObject({ devices: [], ui: true, ui_critical: false });
    expect(newApprover(false, [])).toBeNull();
  });

  it('does not change the original', () => {
    const a = approver('a', { normal: true, critical: true });
    const u = updateOf(a);
    withDeviceCritical(u, 'mobile_app_pixel', false);
    expect(u.devices[0]?.critical).toBe(true);
    expect(a.devices[0]?.critical).toBe(true);
  });
});

describe('summary', () => {
  it('says whether normal and critical requests reach anyone', () => {
    expect(summary([])).toBe('none');
    expect(summary([approver('a', { normal: false, critical: false })])).toBe('none');
    expect(summary([approver('a', { normal: true, critical: false })])).toBe('no_critical');
    expect(summary([approver('a', { normal: true, critical: false }), approver('b', { normal: true, critical: true })])).toBe('ok');
  });

  it('knows when a removal would leave nobody', () => {
    const list = [approver('a', { normal: true, critical: true }), approver('b', { normal: false, critical: false })];
    expect(isLastReachable(list, 'a')).toBe(true);
    expect(isLastReachable(list, 'b')).toBe(false);
    expect(isLastReachable([...list, approver('c', { normal: true, critical: false })], 'a')).toBe(false);
  });
});
