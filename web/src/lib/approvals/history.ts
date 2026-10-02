// SPDX-License-Identifier: AGPL-3.0-or-later

// The history of approval requests (design README 6.9): five, with F1 six, endings, each
// with its own icon, form and words, so they cannot be confused at a glance.

import type { ApprovalHistoryEntry } from '../api/types.ts';
import { answeredAfter } from '../audit/outcome.ts';
import type { IconName } from '../components/Icon.svelte';
import { m } from '../i18n.ts';
import { cleanUntrusted } from '../untrusted.ts';

export interface HistoryOutcome {
  icon: IconName;
  tone: 'positive' | 'danger' | 'warning' | 'muted';
  text: string;
  /** Explanation under the text; only for an invalid answer. */
  hint: string | null;
  /** A dashed edge, for a request that nobody answered. */
  dashed: boolean;
}

const SEPARATOR = ' · ';

function answered(entry: ApprovalHistoryEntry, who: string): string {
  const parts = [who];
  if (entry.via === 'ui') parts.push(m.audit_via_ui());
  if (entry.via === 'push') parts.push(m.audit_via_push());
  parts.push(m.request_answered_after({ duration: answeredAfter(entry.created_at, entry.answered_at) }));
  return parts.join(SEPARATOR);
}

export function historyOutcome(entry: ApprovalHistoryEntry): HistoryOutcome {
  const person = cleanUntrusted(entry.by_name ?? '');
  const plain = { hint: null, dashed: false };
  switch (entry.outcome) {
    case 'approved':
      return { icon: 'allow', tone: 'positive', text: answered(entry, m.result_approved_by({ person })), ...plain };
    case 'rejected':
      return { icon: 'deny', tone: 'danger', text: answered(entry, m.result_declined_by({ person })), ...plain };
    case 'timeout':
      return { icon: 'ask', tone: 'muted', text: m.result_timeout(), hint: null, dashed: true };
    case 'invalid_response':
      return { icon: 'warning', tone: 'warning', text: m.result_invalid(), hint: m.result_invalid_hint(), dashed: false };
    case 'emergency_stop':
      return { icon: 'power', tone: 'danger', text: m.result_estop(), ...plain };
    case 'revoked':
      return { icon: 'deny', tone: 'danger', text: m.result_revoked(), ...plain };
  }
}
