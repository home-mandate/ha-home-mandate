// SPDX-License-Identifier: AGPL-3.0-or-later

// What an audit entry means for the UI (design README 6.8): the decision of a request
// (deny without a matching rule is the default, a form of its own), whether the action was
// critical, and the name of the device. Overview, audit log, approvals and agent details
// all show entries through these functions, so they say the same everywhere.

import type { AuditEntry, Category, DecisionFilter, DeviceCatalog, DirectoryChange } from '../api/types.ts';
import { isCritical } from '../engine/vocabulary.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted, isolate } from '../untrusted.ts';

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

const DIRECTORY: Record<DirectoryChange, (v: { device: string; former: string }) => string> = {
  critical_marked: (v) => m.directory_critical_marked(v),
  critical_unmarked: (v) => m.directory_critical_unmarked(v),
  renamed: (v) => m.directory_renamed(v),
  rename_applied: (v) => m.directory_rename_applied(v),
  rename_dismissed: (v) => m.directory_rename_dismissed(v),
};

/** directoryText says what changed in the resource directory; empty for other entries. */
export function directoryText(entry: AuditEntry, catalog: DeviceCatalog | null): string {
  const d = entry.directory;
  const text = d ? (DIRECTORY[d.change] as ((v: { device: string; former: string }) => string) | undefined) : undefined;
  if (!d || !text) return '';
  return text({ device: isolate(deviceName(d.entity_id, catalog)), former: isolate(cleanUntrusted(d.previous_entity_id ?? '')) });
}

/** deviceName is the device's name from Home Assistant, else its entity ID, both cleaned. */
export function deviceName(entityId: string, catalog: DeviceCatalog | null): string {
  const device = catalog?.devices.find((d) => d.entity_id === entityId);
  return cleanUntrusted(device?.name || entityId);
}
