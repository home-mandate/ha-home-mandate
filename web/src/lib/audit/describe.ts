// SPDX-License-Identifier: AGPL-3.0-or-later

// What an audit entry means for the UI (design README 6.8): the decision of a request
// (deny without a matching rule is the default, a form of its own), whether the action was
// critical, and the name of the device. Overview, audit log, approvals and agent details
// all show entries through these functions, so they say the same everywhere.

import type { AuditEntry, Category, DecisionFilter, DeviceCatalog } from '../api/types.ts';
import { isCritical } from '../engine/vocabulary.ts';
import { cleanUntrusted } from '../untrusted.ts';

/** decisionOf returns the decision of a request, or null for anything that is no decision. */
export function decisionOf(entry: AuditEntry): DecisionFilter | null {
  const evaluation = entry.event === 'decision' ? entry.evaluation : undefined;
  if (!evaluation) return null;
  return evaluation.decision === 'deny' && evaluation.reason === 'no_match' ? 'default' : evaluation.decision;
}

/** isAdminEvent tells administrative events (no decision columns) from requests. */
export function isAdminEvent(entry: AuditEntry): boolean {
  return entry.event !== 'decision';
}

/**
 * isCriticalRequest tells whether the requested action is critical: on a device the
 * household marked, every action but read; otherwise by the vocabulary. Without a category or
 * with one the UI does not know, it cannot tell and says yes: never harmless by mistake.
 */
export function isCriticalRequest(entry: AuditEntry): boolean {
  const request = entry.request;
  if (!request) return false;
  if (request.resource.critical === true && request.action !== 'read') return true;
  return isCritical(request.resource.category as Category | undefined, request.action);
}

/** deviceName is the device's name from Home Assistant, else its entity ID, both cleaned. */
export function deviceName(entityId: string, catalog: DeviceCatalog | null): string {
  const device = catalog?.devices.find((d) => d.entity_id === entityId);
  return cleanUntrusted(device?.name || entityId);
}
