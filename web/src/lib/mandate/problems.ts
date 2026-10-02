// SPDX-License-Identifier: AGPL-3.0-or-later

// Turns the problems of a draft (engine/check.ts, JSON pointers) into messages at the
// fields of the editor. The display name is checked here too: it is Home-Mandate metadata
// next to the document. The server checks everything again when it stores a version.

import type { MandateDraft } from '../api/types.ts';
import { checkDraft, MAX_RATE, MAX_RULES, MIN_RATE, type Problem } from '../engine/check.ts';
import { m } from '../i18n.ts';
import { actionLabel, categoryLabel } from './labels.ts';

/** The control a problem belongs to; with a rule index it is inside that rule's form. */
export type Part =
  | 'name'
  | 'valid_from'
  | 'expires'
  | 'limit'
  | 'timeout'
  | 'approvers'
  | 'rules'
  | 'scope'
  | 'actions'
  | 'decision'
  | 'window'
  | 'weekdays';

export interface FieldProblem {
  /** Index of the rule, or null for the settings of the mandate. */
  rule: number | null;
  part: Part;
  text: string;
  /** The action a vocabulary problem is about. */
  action: string | null;
}

export const NAME_MAX = 80;

type Described = Pick<FieldProblem, 'part' | 'text'> & { action?: string };

function settingProblem(path: readonly string[], code: Problem['code']): Described {
  const [first, second] = path;
  switch (first) {
    case 'limits':
      return { part: 'limit', text: m.validation_limit({ min: MIN_RATE, max: MAX_RATE }) };
    case 'approval':
      return second === 'approvers' ? { part: 'approvers', text: m.validation_approvers() } : { part: 'timeout', text: m.timeout_error() };
    case 'valid_from':
      return { part: 'valid_from', text: m.validation_date() };
    case 'expires':
      return { part: 'expires', text: code === 'order' ? m.validation_expires_order() : m.validation_date() };
    default:
      return { part: 'rules', text: m.validation_too_many_rules({ max: MAX_RULES }) };
  }
}

function ruleProblem(draft: MandateDraft, index: number, path: readonly string[], code: Problem['code']): Described {
  const rule = draft.rules[index];
  const [first, second] = path;
  const invalid = m.validation_rule();
  switch (first) {
    case 'actions': {
      if (code === 'required') return { part: 'actions', text: m.validation_actions_required() };
      const action = rule?.actions[Number(second)];
      const category = rule && 'category' in rule.resource ? rule.resource.category : undefined;
      if (code !== 'vocabulary' || action === undefined || category === undefined) return { part: 'actions', text: invalid };
      const names = { action: actionLabel(category, action), category: categoryLabel(category) };
      return { part: 'actions', text: m.rule_error_action_category(names), action };
    }
    case 'conditions':
      if (second === 'time_window') return { part: 'window', text: code === 'empty' ? m.time_error_equal() : m.validation_window_incomplete() };
      if (second === 'weekdays') return { part: 'weekdays', text: m.validation_weekdays_required() };
      return { part: 'window', text: invalid };
    case 'approval':
      if (second === 'approvers') return { part: 'approvers', text: m.validation_approvers() };
      if (second === 'timeout') return { part: 'timeout', text: m.timeout_error() };
      return { part: 'decision', text: invalid };
    case 'resource':
      return { part: 'scope', text: invalid };
    default:
      return { part: 'decision', text: invalid };
  }
}

/** describeProblems lists every problem of the edited mandate once per field and message. */
export function describeProblems(name: string, draft: MandateDraft): FieldProblem[] {
  const problems: FieldProblem[] = [];
  const seen = new Set<string>();
  const add = (rule: number | null, d: Described) => {
    const key = `${rule}|${d.part}|${d.text}`;
    if (seen.has(key)) return;
    seen.add(key);
    problems.push({ rule, part: d.part, text: d.text, action: d.action ?? null });
  };

  const length = [...name.trim()].length;
  if (length < 1 || length > NAME_MAX) add(null, { part: 'name', text: m.validation_name({ max: NAME_MAX }) });

  for (const problem of checkDraft(draft)) {
    const [first = '', second, ...rest] = problem.field.split('/').slice(1);
    if (first === 'rules' && second !== undefined) add(Number(second), ruleProblem(draft, Number(second), rest, problem.code));
    else add(null, settingProblem([first, ...(second === undefined ? [] : [second])], problem.code));
  }
  return problems;
}
