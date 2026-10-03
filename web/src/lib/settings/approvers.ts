// SPDX-License-Identifier: AGPL-3.0-or-later

// The approvers section of the settings (decisions F2, S1, S2): what one change does to an
// approver, which devices can still be added, and what the list says as a whole. The
// server checks every save and computes each person's reach; nothing here decides who
// gets a request.

import type { Approver, ApproverCandidates, ApproverDevice, ApproverUpdate } from '../api/types.ts';

/** At most this many devices per person (internal/approval). */
export const MAX_DEVICES = 5;

export type Summary = 'ok' | 'no_critical' | 'none';

/** updateOf is the saved form of an approver. */
export function updateOf(a: Approver): ApproverUpdate {
  return { devices: a.devices.map((d) => ({ ...d })), ui: a.ui, ui_critical: a.ui_critical, language: a.language };
}

/**
 * newApprover is what "Add person" saves: the first free device with its suggestion, or
 * for an administrator without a free device the UI channel. null when neither works.
 */
export function newApprover(isAdmin: boolean, free: ApproverCandidates['devices']): ApproverUpdate | null {
  const [first] = free;
  if (first) return { devices: [{ service: first.service, critical: first.suggest_critical }], ui: false, ui_critical: false, language: null };
  return isAdmin ? { devices: [], ui: true, ui_critical: false, language: null } : null;
}

/** freeDevices are the candidate devices the approver does not use yet. */
export function freeDevices(a: Pick<ApproverUpdate, 'devices'>, candidates: ApproverCandidates): ApproverCandidates['devices'] {
  return candidates.devices.filter((d) => !a.devices.some((x) => x.service === d.service));
}

/** withDevice adds a device with its suggestion for critical requests. */
export function withDevice(u: ApproverUpdate, device: ApproverCandidates['devices'][number]): ApproverUpdate {
  return { ...u, devices: [...u.devices, { service: device.service, critical: device.suggest_critical }] };
}

/** withoutDevice removes a device. */
export function withoutDevice(u: ApproverUpdate, service: string): ApproverUpdate {
  return { ...u, devices: u.devices.filter((d) => d.service !== service) };
}

/** withDeviceCritical switches critical requests for one device. */
export function withDeviceCritical(u: ApproverUpdate, service: string, critical: boolean): ApproverUpdate {
  return { ...u, devices: u.devices.map((d): ApproverDevice => (d.service === service ? { ...d, critical } : d)) };
}

/** withUi switches the UI channel; switching it off also ends critical requests there. */
export function withUi(u: ApproverUpdate, ui: boolean): ApproverUpdate {
  return { ...u, ui, ui_critical: ui && u.ui_critical };
}

/** summary says whether normal and critical requests reach anyone, from the server's reach. */
export function summary(approvers: readonly Approver[]): Summary {
  if (!approvers.some((a) => a.reach.normal)) return 'none';
  return approvers.some((a) => a.reach.critical) ? 'ok' : 'no_critical';
}

/** isLastReachable tells whether removing a would leave nobody who gets requests. */
export function isLastReachable(approvers: readonly Approver[], userId: string): boolean {
  return approvers.filter((a) => a.user_id !== userId && a.reach.normal).length === 0;
}
