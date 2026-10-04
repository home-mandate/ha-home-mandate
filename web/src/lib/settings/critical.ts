// SPDX-License-Identifier: AGPL-3.0-or-later

// Critical devices (SPEC-v0 section 4, step 5): which devices the settings show. The
// marked ones and the proposals always; any other device only through the search, since a
// house has hundreds. Devices that can only be read are not offered: marking them changes
// nothing.

import type { Device, DeviceCatalog } from '../api/types.ts';
import { cleanUntrusted } from '../untrusted.ts';

/** Most matches of a search shown at once; the rest is counted. */
export const MATCH_LIMIT = 20;

export interface CriticalGroups {
  marked: Device[];
  suggested: Device[];
  /** Unmarked devices that match the search and are not proposed. */
  matches: Device[];
  /** Matches beyond MATCH_LIMIT. */
  more: number;
}

const byName = (a: Device, b: Device) => cleanUntrusted(a.name).localeCompare(cleanUntrusted(b.name)) || a.entity_id.localeCompare(b.entity_id);

export function criticalGroups(catalog: DeviceCatalog, query: string): CriticalGroups {
  // Every marked device, also one that can only be read now: its mark must stay removable.
  const marked = catalog.devices.filter((d) => d.critical).sort(byName);
  const markable = catalog.devices.filter((d) => d.actions.some((a) => a !== 'read')).sort(byName);
  const suggested = markable.filter((d) => !d.critical && d.suggest_critical);
  const q = query.trim().toLowerCase();
  if (q === '') return { marked, suggested, matches: [], more: 0 };
  const areas = new Map(catalog.areas.map((a) => [a.id, cleanUntrusted(a.name).toLowerCase()]));
  const found = markable.filter(
    (d) =>
      !d.critical &&
      !d.suggest_critical &&
      [cleanUntrusted(d.name).toLowerCase(), d.entity_id.toLowerCase(), d.area ? (areas.get(d.area) ?? '') : ''].some((t) => t.includes(q)),
  );
  return { marked, suggested, matches: found.slice(0, MATCH_LIMIT), more: Math.max(0, found.length - MATCH_LIMIT) };
}
