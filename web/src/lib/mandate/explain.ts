// SPDX-License-Identifier: AGPL-3.0-or-later

// Words for a cell of the preview: its accessible name ("Front door, unlock: Ask first.
// From rule 3.") and the explanation shown when it is selected: which rule decides, how
// many rules matched, whether a critical action was downgraded, what applies at times and
// what the stored version said.

import type { Device, DeviceCatalog, MandateDraft } from '../api/types.ts';
import type { Cell, CellDecision } from '../engine/analysis.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted, isolate } from '../untrusted.ts';
import { actionLabel, decisionLabel } from './labels.ts';
import type { MatrixCell } from './matrix.ts';
import { cellText } from './summary.ts';
import { limitsText } from './limits.ts';
import { conditionsText, ruleLine, ruleText } from './text.ts';

/** shortLabel is the word inside a cell; the default is abbreviated. */
export function shortLabel(decision: CellDecision): string {
  return decision === 'default' ? m.decision_default_short() : decisionLabel(decision);
}

/** cellReason names the rule that decides, or that none applies. */
export function cellReason(cell: Pick<Cell, 'rule'>): string {
  return cell.rule === null ? m.matrix_cell_no_rule() : m.matrix_cell_from_rule({ rule: m.rule_ref({ n: cell.rule + 1 }) });
}

/** cellWhy is the line under a decision on mobile: the reason and what the corner marks of the grid say. */
export function cellWhy(cell: Pick<Cell, 'rule' | 'demoted' | 'timed' | 'limited'>): string {
  const timed = cell.timed ? `${m.preview_timed()}: ${decisionLabel(cell.timed.decision)}` : '';
  return [cellReason(cell), cell.demoted ? m.demoted_label() : '', cell.limited ? m.preview_limited() : '', timed].filter(Boolean).join(' · ');
}

/** cellLabel is the accessible name of a cell. */
export function cellLabel(device: Device, c: MatrixCell): string {
  const label = m.matrix_cell_label({
    device: isolate(cleanUntrusted(device.name) || device.entity_id),
    action: actionLabel(device.category, c.action),
    decision: cellText(c.cell),
    reason: cellReason(c.cell),
  });
  const marks = [c.cell.demoted ? m.demoted_label() : '', c.cell.limited ? m.preview_limited() : '', c.changed ? m.preview_change_dot() : ''].filter(Boolean);
  return [label, ...marks].join('. ');
}

export interface DetailLine {
  kind: 'rule' | 'critical' | 'danger' | 'timed' | 'limits' | 'changed';
  text: string;
}

/** cellDetail explains a cell: the deciding rule first, then what else is worth knowing. */
/** version: of the stored mandate the draft is compared with; null for a template, which has no versions. */
export function cellDetail(c: MatrixCell, draft: MandateDraft, catalog: DeviceCatalog, version: number | null, locale: string): DetailLine[] {
  const { cell } = c;
  const lines: DetailLine[] = [];
  const rule = cell.rule === null ? undefined : draft.rules[cell.rule];
  if (rule && cell.rule !== null) {
    const sentence = `${ruleLine(ruleText(rule, catalog, locale), true)} → ${decisionLabel(rule.decision)}`;
    lines.push({ kind: 'rule', text: `${m.rule_ref({ n: cell.rule + 1 })}: ${sentence} · ${m.preview_matching({ count: cell.matching })}` });
  } else {
    lines.push({ kind: 'rule', text: `${m.matrix_cell_no_rule()}. ${m.decision_default_desc()}` });
  }
  if (cell.demoted) lines.push({ kind: 'critical', text: `${m.demoted_label()}: ${m.demoted_hint()}` });
  if (cell.limited && rule) lines.push({ kind: 'limits', text: m.preview_limited_detail({ limits: limitsText(rule, locale) }) });
  // A critical action that runs without anyone being asked is never just "allowed".
  if (cell.critical && cell.decision === 'allow') lines.push({ kind: 'danger', text: m.critical_override_active() });
  const timedRule = cell.timed?.rule == null ? undefined : draft.rules[cell.timed.rule];
  if (cell.timed && timedRule && cell.timed.rule !== null) {
    const inside = m.preview_inside({ window: conditionsText(timedRule, locale) });
    const who = m.rule_ref({ n: cell.timed.rule + 1 });
    lines.push({
      kind: 'timed',
      text: `${inside}: ${decisionLabel(cell.timed.decision)} (${who}). ${m.preview_otherwise()}: ${decisionLabel(cell.decision)}.`,
    });
  }
  if (c.changed) {
    const from = cellText(c.previous);
    const before = version === null ? m.change_from_saved({ from }) : m.change_from_to({ version, from });
    lines.push({ kind: 'changed', text: `${m.preview_change_dot()} · ${before}` });
  }
  return lines;
}
