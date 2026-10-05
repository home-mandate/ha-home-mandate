// SPDX-License-Identifier: AGPL-3.0-or-later

// The approvers placeholder of templates (docs/ARCHITECTURE.md section 6): "$approvers"
// stands for the human who admits the agent plus every approver set up. Admission puts
// those people in its place; a mandate never contains it, so the mandate editor never
// offers it. The UI shows it as a sentence, never as the raw value.

import type { Approval, MandateDraft } from '../api/types.ts';

export const APPROVERS_PLACEHOLDER = '$approvers';

export const isPlaceholder = (id: string): boolean => id === APPROVERS_PLACEHOLDER;

function approvals(draft: MandateDraft): Approval[] {
  return [draft.approval, ...draft.rules.flatMap((r) => (r.approval ? [r.approval] : []))];
}

/** usesPlaceholder tells whether the draft names the placeholder anywhere. */
export function usesPlaceholder(draft: MandateDraft): boolean {
  return approvals(draft).some((a) => a.approvers.some(isPlaceholder));
}

/**
 * withApprovers puts people in the place of the placeholder, at the mandate and in every
 * rule, without duplicates. Null when the placeholder is used and there is nobody.
 */
export function withApprovers(draft: MandateDraft, people: readonly string[]): MandateDraft | null {
  if (usesPlaceholder(draft) && people.length === 0) return null;
  const resolve = (approval: Approval): Approval => ({
    ...approval,
    approvers: [...new Set(approval.approvers.flatMap((id) => (isPlaceholder(id) ? people : [id])))],
  });
  return {
    ...draft,
    approval: resolve(draft.approval),
    rules: draft.rules.map((r) => (r.approval ? { ...r, approval: resolve(r.approval) } : r)),
  };
}
