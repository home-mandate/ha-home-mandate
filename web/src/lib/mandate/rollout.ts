// SPDX-License-Identifier: AGPL-3.0-or-later

// Taking a changed template over into the mandates that use it (#18): which mandates are
// chosen at first, the request's targets, and the result per mandate. The server decides
// per mandate; this only prepares the human's choice and shows what came of it.

import type { MandateDraft, RolloutResult, TemplateRollout, TemplateRolloutRequest, TemplateUser } from '../api/types.ts';
import { m } from '../i18n.ts';

/** Results by mandate ID. */
export type Results = Readonly<Record<string, RolloutResult>>;

/**
 * preselected chooses the mandates whose rules are the template's of before: not those
 * edited since (taking over would replace the edits) nor those already up to date.
 */
export function preselected(users: readonly TemplateUser[]): Set<string> {
  return new Set(users.filter((u) => !u.edited_since && !u.up_to_date).map((u) => u.mandate_id));
}

/** targetsOf names the chosen mandates with the version the human saw, in the order of the list. */
export function targetsOf(users: readonly TemplateUser[], chosen: ReadonlySet<string>): TemplateRolloutRequest['targets'] {
  return users.filter((u) => chosen.has(u.mandate_id)).map((u) => ({ mandate_id: u.mandate_id, base_digest: u.digest }));
}

const TEXTS: Record<RolloutResult, () => string> = {
  updated: () => m.rollout_result_updated(),
  unchanged: () => m.rollout_result_unchanged(),
  conflict: () => m.rollout_result_conflict(),
  revoked: () => m.rollout_result_revoked(),
  not_found: () => m.rollout_result_not_found(),
  failed: () => m.rollout_result_failed(),
  skipped: () => m.rollout_result_skipped(),
};

export function resultText(result: RolloutResult): string {
  return TEXTS[result]();
}

/**
 * retryable: a mandate changed meantime, a failure or a skipped one can be loaded again
 * and chosen anew; a revoked or removed one cannot.
 */
export function retryable(result: RolloutResult): boolean {
  return result === 'conflict' || result === 'failed' || result === 'skipped';
}

/** refusedCount counts the mandates that did not take the change. */
export function refusedCount(results: Results): number {
  return Object.values(results).filter((r) => r !== 'updated' && r !== 'unchanged').length;
}

/** merged adds the results of an answer, the newest per mandate. */
export function merged(results: Results, answer: TemplateRollout['results']): Results {
  return { ...results, ...Object.fromEntries(answer.map((r) => [r.mandate_id, r.result])) };
}

/** grantsCritical tells whether a template has a rule allowing critical actions without approval. */
export function grantsCritical(draft: MandateDraft): boolean {
  return draft.rules.some((r) => r.allow_critical === true);
}
