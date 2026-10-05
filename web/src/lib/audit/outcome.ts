// SPDX-License-Identifier: AGPL-3.0-or-later

// Texts of an audit entry (design README 6.8): what happened with a request, kept apart
// from the decision ("allowed, but failed" exists), the event names, the reason codes in
// plain words and who answered an approval, where and how fast.

import type { AuditEntry, AuditEvent, Reason } from '../api/types.ts';
import { m } from '../i18n.ts';
import { cleanUntrusted, isolate } from '../untrusted.ts';

export interface Outcome {
  tone: 'positive' | 'danger' | 'warning' | 'ask';
  text: string;
  /** Why, e.g. the rate limit or the error from Home Assistant; null if the text says it. */
  why: string | null;
}

const SEPARATOR = ' · ';
const MAX_ERROR = 200;

const EVENTS: Record<AuditEvent, () => string> = {
  decision: () => m.event_decision(),
  'agent.registered': () => m.event_agent_approved(),
  'agent.revoked': () => m.event_agent_revoked(),
  'mandate.created': () => m.event_mandate_created(),
  'mandate.updated': () => m.event_mandate_changed(),
  'mandate.revoked': () => m.event_mandate_revoked(),
  'emergency_stop.activated': () => m.event_estop_on(),
  'emergency_stop.released': () => m.event_estop_off(),
  'auth.rejected': () => m.event_login_rejected(),
  'log.truncated': () => m.event_log_pruned(),
  'log.checkpoint': () => m.event_log_checkpoint(),
  'directory.changed': () => m.event_directory_changed(),
};

const REASONS: Record<Reason, () => string> = {
  no_mandate: () => m.code_no_mandate(),
  ambiguous_mandate: () => m.code_ambiguous_mandate(),
  invalid_mandate: () => m.code_invalid_mandate(),
  invalid_request: () => m.code_invalid_request(),
  unknown_resource: () => m.code_unknown_resource(),
  unknown_category: () => m.code_unknown_category(),
  unknown_action: () => m.code_unknown_action(),
  revoked: () => m.code_revoked(),
  not_yet_valid: () => m.code_not_yet_valid(),
  expired: () => m.code_expired(),
  no_match: () => m.code_no_match(),
  critical_demotion: () => m.code_critical_demotion(),
  rule: () => m.code_rule(),
};

export function eventLabel(event: AuditEvent): string {
  return EVENTS[event]();
}

/** reasonText explains a reason code; a code the UI does not know is shown cleaned. */
export function reasonText(reason: Reason): string {
  const text = REASONS[reason] as (() => string) | undefined;
  return text ? text() : cleanUntrusted(reason);
}

/** person is who answered, isolated: a right-to-left name cannot reorder the sentence. */
function person(approval: NonNullable<AuditEntry['approval']>): string {
  return isolate(approval.by_name ?? approval.by ?? '');
}

/** outcomeOf says what happened with a request; null for administrative events. */
export function outcomeOf(entry: AuditEntry): Outcome | null {
  if (entry.event !== 'decision') return null;
  const result = entry.result;
  const approval = entry.approval;
  if (!result) return { tone: 'ask', text: m.result_pending(), why: null };
  switch (result.status) {
    case 'executed':
      return {
        tone: 'positive',
        text: m.result_executed(),
        why: approval?.outcome === 'approved' ? m.result_approved_by({ person: person(approval) }) : null,
      };
    case 'failed':
      return { tone: 'danger', text: m.result_failed(), why: result.error ? cleanUntrusted(result.error, MAX_ERROR) : null };
    case 'denied':
      break;
  }
  switch (result.denied_by) {
    case 'emergency_stop':
      return { tone: 'danger', text: m.result_estop(), why: null };
    case 'rate_limit':
      return { tone: 'danger', text: m.result_denied(), why: m.reason_rate() };
    case 'authentication':
      return { tone: 'danger', text: m.result_denied(), why: m.reason_auth() };
    case 'approval':
      switch (approval?.outcome) {
        case 'rejected':
          return { tone: 'danger', text: m.result_declined_by({ person: person(approval) }), why: null };
        case 'timeout':
          return { tone: 'danger', text: m.result_timeout(), why: null };
        case 'invalid_response':
          return { tone: 'warning', text: m.result_invalid(), why: null };
        default:
          return { tone: 'danger', text: m.result_denied(), why: m.reason_approval() };
      }
    default:
      return { tone: 'danger', text: m.result_denied(), why: m.reason_mandate() };
  }
}

/** approvalText: who answered and through which channel; null without an answer by a person. */
export function approvalText(entry: AuditEntry): string | null {
  const approval = entry.approval;
  if (!approval || (approval.outcome !== 'approved' && approval.outcome !== 'rejected')) return null;
  const who =
    approval.outcome === 'approved' ? m.result_approved_by({ person: person(approval) }) : m.result_declined_by({ person: person(approval) });
  const via = approval.via === 'ui' ? m.audit_via_ui() : approval.via === 'push' ? m.audit_via_push() : null;
  return via ? who + SEPARATOR + via : who;
}

/** answeredAfter is the time between two moments as m:ss or h:mm:ss; empty if unreadable. */
export function answeredAfter(from: string, to: string): string {
  const ms = Date.parse(to) - Date.parse(from);
  if (Number.isNaN(ms)) return '';
  const total = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(total / 3600);
  const min = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(min).padStart(2, '0')}:${s}` : `${min}:${s}`;
}
