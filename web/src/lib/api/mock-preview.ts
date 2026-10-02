// SPDX-License-Identifier: AGPL-3.0-or-later

// A simplified evaluation (SPEC-v0 section 4, steps 1–5) for the mock client only, so the
// "may afterwards" preview reacts while the UI is built without a server. It skips the
// pre-check (schema validation). The real preview comes from mandate-spec/evaluator via
// POST api/mandates/preview; never use this to decide anything.

import { isCritical } from './vocabulary.ts';
import type { Approval, Decision, Device, MandateDraft, Preview, PreviewEntry, Reason, Rule, Weekday } from './types.ts';

const RANK: Record<Decision, number> = { allow: 0, ask: 1, deny: 2 };
const WEEKDAYS: readonly Weekday[] = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];

interface LocalTime {
  weekday: Weekday;
  minutes: number;
}

function localTime(at: Date, timeZone: string): LocalTime {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    weekday: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(at);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? '';
  const weekday = WEEKDAYS.find((d) => d === part('weekday').toLowerCase()) ?? 'mon';
  return { weekday, minutes: Number(part('hour')) * 60 + Number(part('minute')) };
}

function toMinutes(hhmm: string): number {
  const [h = '0', m = '0'] = hhmm.split(':');
  return Number(h) * 60 + Number(m);
}

function conditionsHold(rule: Rule, now: LocalTime): boolean {
  const c = rule.conditions;
  if (!c) return true;
  if (c.weekdays && !c.weekdays.includes(now.weekday)) return false;
  if (c.time_window) {
    const [from = '', to = ''] = c.time_window.split('-');
    const start = toMinutes(from);
    const end = toMinutes(to);
    const inside = start <= end ? now.minutes >= start && now.minutes < end : now.minutes >= start || now.minutes < end;
    if (!inside) return false;
  }
  return true;
}

function resourceMatches(rule: Rule, device: Device): boolean {
  const r = rule.resource;
  if ('any' in r) return true;
  return (
    (r.entity_id === undefined || r.entity_id === device.entity_id) &&
    (r.category === undefined || r.category === device.category) &&
    (r.area === undefined || r.area === device.area)
  );
}

interface Outcome {
  decision: Decision;
  reason: Reason;
  rule_id: string | null;
  approval?: Approval;
}

function decide(draft: MandateDraft, device: Device, action: string, at: Date, now: LocalTime): Outcome {
  const deny = (reason: Reason): Outcome => ({ decision: 'deny', reason, rule_id: null });
  // Approval settings for "ask": the first matching ask rule with its own, else the mandate's.
  const approvalOf = (matching: Rule[]): Approval =>
    matching.find((r) => r.decision === 'ask' && r.approval)?.approval ?? draft.approval;
  if (at < new Date(draft.valid_from)) return deny('not_yet_valid');
  if (draft.expires !== undefined && at >= new Date(draft.expires)) return deny('expired');

  const matching = draft.rules.filter(
    (r) => resourceMatches(r, device) && (r.actions.includes('*') || r.actions.includes(action)) && conditionsHold(r, now),
  );
  if (matching.length === 0) return deny('no_match');

  const decision = matching.reduce<Decision>((d, r) => (RANK[r.decision] > RANK[d] ? r.decision : d), 'allow');
  if (decision === 'allow' && isCritical(device.category, action)) {
    const unconfirmed = matching.find((r) => r.allow_critical !== true);
    if (unconfirmed) {
      return { decision: 'ask', reason: 'critical_demotion', rule_id: unconfirmed.id, approval: approvalOf(matching) };
    }
  }
  const rule = matching.find((r) => r.decision === decision);
  const outcome: Outcome = { decision, reason: 'rule', rule_id: rule?.id ?? null };
  return decision === 'ask' ? { ...outcome, approval: approvalOf(matching) } : outcome;
}

/** evaluateDraft computes the preview of draft for every action of every device. */
export function evaluateDraft(
  draft: MandateDraft,
  devices: readonly Device[],
  at: string,
  timeZone: string,
  previous?: MandateDraft,
): Preview {
  const when = new Date(at);
  const now = localTime(when, timeZone);
  const entries: PreviewEntry[] = devices.flatMap((device) =>
    device.actions.map((action) => {
      const result = decide(draft, device, action, when, now);
      const entry: PreviewEntry = {
        entity_id: device.entity_id,
        action,
        ...result,
        critical: isCritical(device.category, action),
      };
      return previous ? { ...entry, previous: decide(previous, device, action, when, now).decision } : entry;
    }),
  );
  return { at, time_zone: timeZone, entries };
}
