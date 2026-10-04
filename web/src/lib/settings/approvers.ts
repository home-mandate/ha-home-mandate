// SPDX-License-Identifier: AGPL-3.0-or-later

// The approvers section of the settings (decisions F2, S1, S2): what one change does to an
// approver, which devices can still be added, and what the list says as a whole. The
// server checks every save and computes each person's reach; nothing here decides who
// gets a request.

import type { Approver, ApproverCandidates, ApproverDevice, ApproverUpdate, ReachChannel } from '../api/types.ts';

/** At most this many devices per person (internal/approval). */
export const MAX_DEVICES = 5;

/**
 * none: requests reach nobody; ui_only: only in an open Home-Mandate; no_critical: critical
 * ones reach nobody; critical_ui_only: critical ones only in an open Home-Mandate; ok.
 */
export type Summary = 'none' | 'ui_only' | 'no_critical' | 'critical_ui_only' | 'ok';

/** updateOf is the saved form of an approver. */
export function updateOf(a: Approver): ApproverUpdate {
  return { devices: a.devices.map((d) => ({ ...d })), ui: a.ui, ui_critical: a.ui_critical, language: a.language };
}

type Candidate = ApproverCandidates['devices'][number];

/**
 * suggestedCritical: critical requests start switched on only for the person's own device
 * with the server's suggestion; someone else's device or an unknown owner starts off.
 */
export function suggestedCritical(device: Candidate, userId: string): boolean {
  return device.suggest_critical && device.owner_user_id === userId;
}

/** ownDevices are the person's own devices, as the server knows them. */
export function ownDevices(devices: readonly Candidate[], userId: string): Candidate[] {
  return devices.filter((d) => d.owner_user_id === userId);
}

/** newApprover is what "Add person" saves: a chosen device, or (service null) only the UI. */
export function newApprover(userId: string, device: Candidate | null): ApproverUpdate {
  return device
    ? { devices: [{ service: device.service, critical: suggestedCritical(device, userId) }], ui: false, ui_critical: false, language: null }
    : { devices: [], ui: true, ui_critical: false, language: null };
}

/** freeDevices are the candidate devices the approver does not use yet. */
export function freeDevices(a: Pick<ApproverUpdate, 'devices'>, candidates: ApproverCandidates): ApproverCandidates['devices'] {
  return candidates.devices.filter((d) => !a.devices.some((x) => x.service === d.service));
}

/** withDevice adds a device for userId, with critical requests as suggestedCritical says. */
export function withDevice(u: ApproverUpdate, device: Candidate, userId: string): ApproverUpdate {
  return { ...u, devices: [...u.devices, { service: device.service, critical: suggestedCritical(device, userId) }] };
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

/** summary says how normal and critical requests reach anyone, from the server's reach (decision S9). */
export function summary(approvers: readonly Approver[]): Summary {
  const best = (kind: 'normal' | 'critical'): ReachChannel =>
    approvers.some((a) => a.reach[kind] === 'push') ? 'push' : approvers.some((a) => a.reach[kind] === 'ui') ? 'ui' : 'none';
  const normal = best('normal');
  const critical = best('critical');
  if (normal === 'none') return 'none';
  if (normal === 'ui') return 'ui_only';
  if (critical === 'none') return 'no_critical';
  return critical === 'ui' ? 'critical_ui_only' : 'ok';
}

/** isLastReachable tells whether removing userId would leave nobody who gets requests. */
export function isLastReachable(approvers: readonly Approver[], userId: string): boolean {
  return !approvers.some((a) => a.user_id !== userId && a.reach.normal !== 'none');
}

/** isLastCritical tells whether userId is the only one critical requests reach. */
export function isLastCritical(approvers: readonly Approver[], userId: string): boolean {
  const me = approvers.find((a) => a.user_id === userId);
  return me !== undefined && me.reach.critical !== 'none' && !approvers.some((a) => a.user_id !== userId && a.reach.critical !== 'none');
}

/** canGetCritical tells whether a saved form still has a channel for critical requests. */
export function canGetCritical(u: ApproverUpdate): boolean {
  return u.devices.some((d) => d.critical) || (u.ui && u.ui_critical);
}
