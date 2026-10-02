// SPDX-License-Identifier: AGPL-3.0-or-later

// What a template does, in the decision language, before someone picks it (design
// "Mandates", template row): one chip per decision that names what its rules cover, and
// always the fixed default at the end.

import type { DeviceCatalog, MandateDraft, Decision } from '../api/types.ts';
import type { CellDecision } from '../engine/analysis.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted } from '../untrusted.ts';
import { decisionLabel } from './labels.ts';
import { listText, ruleLine, ruleText } from './text.ts';

export interface TemplateChip {
  kind: CellDecision;
  text: string;
}

const ORDER: readonly Decision[] = ['allow', 'ask', 'deny'];
/** A single rule with up to this many actions is shown with its actions. */
const MAX_ACTIONS_SHOWN = 2;

/** templateChips summarizes a template's rules per decision. */
export function templateChips(draft: MandateDraft, catalog: DeviceCatalog, locale: string): TemplateChip[] {
  const chips: TemplateChip[] = [];
  for (const kind of ORDER) {
    const rules = draft.rules.filter((r) => r.decision === kind);
    const [only] = rules;
    if (!only) continue;
    const texts = rules.map((r) => ruleText(r, catalog, locale));
    const single = rules.length === 1 && (only.actions.length <= MAX_ACTIONS_SHOWN || only.actions.includes('*'));
    const subjects = [...new Set(texts.map((t) => (t.area ? `${t.subject} · ${t.area}` : t.subject)))];
    chips.push({ kind, text: single && texts[0] ? ruleLine(texts[0]) : listText(subjects, locale) });
  }
  return [...chips, { kind: 'default', text: decisionLabel('default') }];
}

/** Templates Home-Mandate ships; others carry the name their author gave them. */
const SHIPPED: Readonly<Record<string, () => string>> = {
  'read-only': () => m.tpl_read_only(),
  'voice-assistant': () => m.tpl_voice(),
  empty: () => m.tpl_empty(),
};

/** templateName shows a shipped template in the UI language, any other by its own name. */
export function templateName(name: string): string {
  return Object.hasOwn(SHIPPED, name) ? (SHIPPED[name] as () => string)() : cleanUntrusted(name);
}
