// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { Approver, ApproverCandidates } from '../api/types.ts';
import {
  canGetCritical,
  freeDevices,
  isLastCritical,
  isLastReachable,
  newApprover,
  ownDevices,
  suggestedCritical,
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
    { service: 'mobile_app_pixel', name: 'Pixel', suggest_critical: true, owner_user_id: 'a' },
    { service: 'mobile_app_mac', name: 'Mac', suggest_critical: false, owner_user_id: 'a' },
    { service: 'mobile_app_other', name: 'Other', suggest_critical: true, owner_user_id: 'b' },
  ],
};

const R = (normal: Approver['reach']['normal'], critical: Approver['reach']['critical']): Approver['reach'] => ({ normal, critical });
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
    const u = updateOf(approver('a', R('push', 'push')));
    expect(freeDevices(u, candidates).map((d) => d.service)).toEqual(['mobile_app_mac', 'mobile_app_other']);
    const more = withDevice(u, candidates.devices[1] as ApproverCandidates['devices'][number], 'a');
    expect(more.devices).toEqual([
      { service: 'mobile_app_pixel', critical: true },
      { service: 'mobile_app_mac', critical: false },
    ]);
    expect(freeDevices(more, candidates).map((d) => d.service)).toEqual(['mobile_app_other']);
    expect(withoutDevice(more, 'mobile_app_pixel').devices.map((d) => d.service)).toEqual(['mobile_app_mac']);
    expect(withDeviceCritical(more, 'mobile_app_mac', true).devices[1]?.critical).toBe(true);
  });

  it('takes critical requests in the UI away with the UI', () => {
    const u = { ...updateOf(approver('a', R('push', 'push'))), ui: true, ui_critical: true };
    expect(withUi(u, false)).toMatchObject({ ui: false, ui_critical: false });
    expect(withUi({ ...u, ui: false, ui_critical: false }, true)).toMatchObject({ ui: true, ui_critical: false });
  });

  it('switches critical requests on only for the person\'s own device with the suggestion', () => {
    const [pixel, mac, other] = candidates.devices as [ApproverCandidates['devices'][number], ApproverCandidates['devices'][number], ApproverCandidates['devices'][number]];
    expect(suggestedCritical(pixel, 'a')).toBe(true);
    expect(suggestedCritical(mac, 'a')).toBe(false);
    expect(suggestedCritical(other, 'a')).toBe(false); // someone else's phone
    expect(suggestedCritical({ ...pixel, owner_user_id: null }, 'a')).toBe(false); // owner unknown
    expect(ownDevices(candidates.devices, 'b').map((d) => d.service)).toEqual(['mobile_app_other']);
    expect(withDevice(updateOf(approver('a', R('push', 'push'))), other, 'a').devices.at(-1)).toEqual({ service: 'mobile_app_other', critical: false });
  });

  it('starts a new person with the chosen device, or only the UI', () => {
    expect(newApprover('b', candidates.devices[2] ?? null).devices).toEqual([{ service: 'mobile_app_other', critical: true }]);
    expect(newApprover('b', candidates.devices[0] ?? null).devices).toEqual([{ service: 'mobile_app_pixel', critical: false }]);
    expect(newApprover('a', null)).toMatchObject({ devices: [], ui: true, ui_critical: false });
  });

  it('does not change the original', () => {
    const a = approver('a', R('push', 'push'));
    const u = updateOf(a);
    withDeviceCritical(u, 'mobile_app_pixel', false);
    expect(u.devices[0]?.critical).toBe(true);
    expect(a.devices[0]?.critical).toBe(true);
  });
});

describe('summary', () => {
  it('says how normal and critical requests reach anyone (decision S9)', () => {
    expect(summary([])).toBe('none');
    expect(summary([approver('a', R('none', 'none'))])).toBe('none');
    expect(summary([approver('a', R('ui', 'ui'))])).toBe('ui_only');
    expect(summary([approver('a', R('push', 'none'))])).toBe('no_critical');
    expect(summary([approver('a', R('push', 'ui'))])).toBe('critical_ui_only');
    expect(summary([approver('a', R('push', 'none')), approver('b', R('ui', 'ui'))])).toBe('critical_ui_only');
    expect(summary([approver('a', R('push', 'none')), approver('b', R('push', 'push'))])).toBe('ok');
  });

  it('knows when a removal would leave nobody, or nobody for critical requests', () => {
    const list = [approver('a', R('push', 'push')), approver('b', R('none', 'none'))];
    expect(isLastReachable(list, 'a')).toBe(true);
    expect(isLastReachable(list, 'b')).toBe(false);
    expect(isLastReachable([...list, approver('c', R('ui', 'none'))], 'a')).toBe(false);
    expect(isLastCritical(list, 'a')).toBe(true);
    expect(isLastCritical(list, 'b')).toBe(false);
    expect(isLastCritical([...list, approver('c', R('ui', 'ui'))], 'a')).toBe(false);
  });

  it('knows whether a saved form still has a channel for critical requests', () => {
    const u = updateOf(approver('a', R('push', 'push')));
    expect(canGetCritical(u)).toBe(true);
    expect(canGetCritical(withDeviceCritical(u, 'mobile_app_pixel', false))).toBe(false);
    expect(canGetCritical({ ...withDeviceCritical(u, 'mobile_app_pixel', false), ui: true, ui_critical: true })).toBe(true);
  });
});
