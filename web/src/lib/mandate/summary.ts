// SPDX-License-Identifier: AGPL-3.0-or-later

// Texts of the save summary and the version compare: changed settings as "old → new" and
// the outcome of a cell in words.

import type { Cell } from '../engine/analysis.ts';
import { parseDateTime } from '../engine/check.ts';
import { formatDate, formatNumber, type FormatContext } from '../format.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted } from '../untrusted.ts';
import { settingChanges, type Edited, type Setting } from './changes.ts';
import { decisionLabel } from './labels.ts';
import { listText } from './text.ts';
import { timeoutText } from './timeout.ts';

export interface SettingLine {
  setting: Setting;
  label: string;
  /** Empty for a change that has no values to compare (order of the rules). */
  from: string;
  to: string;
}

const LABELS: Readonly<Record<Setting, () => string>> = {
  name: () => m.editor_name(),
  valid_from: () => m.editor_valid_from(),
  expires: () => m.mandates_col_valid_until(),
  limit: () => m.editor_rate(),
  timeout: () => m.timeout_label(),
  approvers: () => m.editor_approvers(),
  order: () => m.save_order_changed(),
};

/** People by Home Assistant user id; an id without a known name is shown as it is. */
export type People = ReadonlyMap<string, string>;

function value(setting: Setting, of: Edited, ctx: FormatContext, people: People): string {
  const draft = of.draft;
  switch (setting) {
    case 'name':
      return cleanUntrusted(of.name);
    case 'valid_from': {
      const at = parseDateTime(draft.valid_from);
      return Number.isNaN(at) ? '' : formatDate(new Date(at), ctx);
    }
    case 'expires': {
      const at = parseDateTime(draft.expires);
      // The end is exclusive: the last valid day is the one before it.
      return Number.isNaN(at) ? m.mandates_valid_open() : formatDate(new Date(at - 1), ctx);
    }
    case 'limit':
      return formatNumber(draft.limits.max_actions_per_hour, ctx);
    case 'timeout':
      return timeoutText(draft.approval.timeout, ctx.locale);
    case 'approvers':
      return listText(draft.approval.approvers.map((id) => cleanUntrusted(people.get(id)) || id), ctx.locale);
    case 'order':
      return '';
  }
}

/** settingLines describes the changed settings with their old and new value. */
export function settingLines(prev: Edited, next: Edited, ctx: FormatContext, people: People): SettingLine[] {
  return settingChanges(prev, next).map((setting) => ({
    setting,
    label: LABELS[setting](),
    from: value(setting, prev, ctx, people),
    to: value(setting, next, ctx, people),
  }));
}

/** cellText names the outcome of a cell, with the outcome at times if it depends on time. */
export function cellText(cell: Pick<Cell, 'decision' | 'timed'>): string {
  const decision = decisionLabel(cell.decision);
  return cell.timed ? m.preview_cell_timed({ decision, timed: decisionLabel(cell.timed.decision) }) : decision;
}
