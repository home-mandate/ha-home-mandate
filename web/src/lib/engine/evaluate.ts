// SPDX-License-Identifier: AGPL-3.0-or-later

// Evaluation rule of SPEC-v0 section 4, ported from the reference evaluator
// (spec/evaluator) for the editor: preview matrix, save summary, version compare.
// It passes every conformance case of the pinned version of the specification (evaluate.test.ts).
// It never decides anything real: every agent request is decided by the PDP on the server.

import type { Approval, Decision, MandateDraft, Reason, Rule, Weekday } from '../api/types.ts';
import { checkDraft, parseDateTime, windowMinutes } from './check.ts';
import { lookupAction } from './vocabulary.ts';

export interface EvalRequest {
  resource: { entity_id: string; category?: string; area?: string; critical?: boolean };
  action: string;
  /** Integer parameters of the action in the units of the vocabulary (SPEC-v0 section 4.5). */
  parameters?: Record<string, number>;
  /** RFC 3339 point in time. */
  time: string;
  /** IANA time zone of the household; absent or empty: the offset in `time` applies. */
  timezone?: string;
  revoked?: boolean;
}

export interface EvalResult {
  decision: Decision;
  reason: Reason;
  rule_id: string | null;
  /** Approval settings, only for "ask". */
  approval?: Approval;
}

/** Household local time: weekday and minute since midnight. */
export interface LocalTime {
  weekday: Weekday;
  minute: number;
}

const ENTITY_ID = /^[!-~]{1,255}$/;
const AREA = /^[!-~]{1,64}$/;
const OFFSET = /(Z|([+-])(\d{2}):(\d{2}))$/;
const ZONE_PART = /^[A-Z][A-Za-z0-9_+-]*$/;
const MAX_ZONE_LENGTH = 64;
const WEEKDAYS: readonly Weekday[] = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];
const STRICTNESS: Record<Decision, number> = { allow: 0, ask: 1, deny: 2 };
const MINUTE_MS = 60_000;

// Valid zones only; the bound keeps spelling variants ("Europe/BERLIN") from filling memory.
const formatters = new Map<string, Intl.DateTimeFormat>();
const MAX_CACHED_ZONES = 256;

/**
 * validZoneName implements SPEC-v0 section 4.2: "UTC" or "Area/Location", each part
 * starting with an upper-case letter. This excludes host-dependent names ("Local",
 * "localtime"), abbreviations ("CET"), paths and control characters.
 */
function validZoneName(zone: string): boolean {
  if (zone === 'UTC') return true;
  const parts = zone.split('/');
  return zone.length <= MAX_ZONE_LENGTH && parts.length >= 2 && parts.every((p) => ZONE_PART.test(p));
}

function zoneFormatter(zone: string): Intl.DateTimeFormat | null {
  const cached = formatters.get(zone);
  if (cached) return cached;
  if (!validZoneName(zone)) return null;
  try {
    const f = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      weekday: 'short',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    });
    // Intl matches zone names without regard to case; a name that differs from its zone
    // only in case is not a name of the time zone database (SPEC-v0 section 4.2).
    const canonical = f.resolvedOptions().timeZone;
    if (canonical !== zone && canonical.toLowerCase() === zone.toLowerCase()) return null;
    if (formatters.size < MAX_CACHED_ZONES) formatters.set(zone, f);
    return f;
  } catch {
    return null; // zone unknown to this browser
  }
}

/** localTime converts an RFC 3339 time to household local time, or null if invalid. */
export function localTime(time: string, zone?: string): LocalTime | null {
  const at = parseDateTime(time);
  if (Number.isNaN(at)) return null;
  if (zone === undefined || zone === '') {
    const match = OFFSET.exec(time);
    const sign = match?.[2] === '-' ? -1 : 1;
    const offset = match?.[1] === 'Z' ? 0 : sign * (Number(match?.[3]) * 60 + Number(match?.[4]));
    const shifted = new Date(at + offset * MINUTE_MS);
    return { weekday: WEEKDAYS[shifted.getUTCDay()] ?? 'sun', minute: shifted.getUTCHours() * 60 + shifted.getUTCMinutes() };
  }
  const f = zoneFormatter(zone);
  if (!f) return null;
  const parts = f.formatToParts(at);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? '';
  const weekday = WEEKDAYS.find((d) => d === part('weekday').toLowerCase());
  if (!weekday) return null;
  return { weekday, minute: Number(part('hour')) * 60 + Number(part('minute')) };
}

/** conditionsMet checks a rule's conditions at a household local time. */
export function conditionsMet(rule: Rule, local: LocalTime): boolean {
  const c = rule.conditions;
  if (!c) return true;
  if (c.time_window !== undefined) {
    const w = windowMinutes(c.time_window);
    if (!w) return false;
    const inside = w.start < w.end ? local.minute >= w.start && local.minute < w.end : local.minute >= w.start || local.minute < w.end;
    if (!inside) return false;
  }
  return c.weekdays === undefined || c.weekdays.includes(local.weekday);
}

/** resourceMatches implements step 2: every given field matches exactly. */
export function resourceMatches(rule: Rule, resource: EvalRequest['resource']): boolean {
  const r = rule.resource;
  if ('any' in r && r.any === true) return true;
  return (
    (r.entity_id === undefined || r.entity_id === resource.entity_id) &&
    (r.category === undefined || r.category === resource.category) &&
    (r.area === undefined || r.area === resource.area)
  );
}

/** constraintsMet: the request carries every constrained parameter within the limits. */
export function constraintsMet(rule: Rule, parameters: EvalRequest['parameters']): boolean {
  return Object.entries(rule.constraints ?? {}).every(([name, { min = -Infinity, max = Infinity }]) => {
    const value = parameters?.[name];
    return value !== undefined && value >= min && value <= max;
  });
}

export function coversAction(rule: Rule, action: string): boolean {
  return rule.actions.includes('*') || rule.actions.includes(action);
}

const deny = (reason: Reason): EvalResult => ({ decision: 'deny', reason, rule_id: null });
const clone = (a: Approval): Approval => ({ timeout: a.timeout, approvers: [...a.approvers] });

/** precheck implements steps 0 and 1 in the reason order of section 4.1. */
function precheck(mandate: MandateDraft, req: EvalRequest): { local: LocalTime; critical: boolean } | EvalResult {
  const local = localTime(req.time, req.timezone);
  const res = req.resource;
  const integers = Object.values(req.parameters ?? {}).every((v) => Number.isInteger(v) && Math.abs(v) <= Number.MAX_SAFE_INTEGER);
  const validRequest =
    typeof res.entity_id === 'string' && ENTITY_ID.test(res.entity_id) && (res.area === undefined || res.area === '' || AREA.test(res.area)) && integers;
  if (!local || !validRequest) return deny('invalid_request');
  // A resource without category is not in the directory of the PEP.
  if (typeof res.category !== 'string' || res.category === '') return deny('unknown_resource');

  const { categoryKnown, actionKnown, critical } = lookupAction(res.category ?? '', req.action);
  if (!categoryKnown) return deny('unknown_category');
  if (!actionKnown) return deny('unknown_action');
  if (req.revoked === true) return deny('revoked');
  const at = parseDateTime(req.time);
  if (at < parseDateTime(mandate.valid_from)) return deny('not_yet_valid');
  if (mandate.expires !== undefined && at >= parseDateTime(mandate.expires)) return deny('expired');
  // The directory can mark a resource as critical: then every action except read is.
  return { local, critical: critical || (res.critical === true && req.action !== 'read') };
}

/** decide implements steps 4 and 5 and section 4.1 for a non-empty list of matching rules. */
export function decide(mandate: MandateDraft, matched: Rule[], critical: boolean): EvalResult {
  const final = matched.reduce<Decision>((d, r) => (STRICTNESS[r.decision] > STRICTNESS[d] ? r.decision : d), 'allow');
  const first = matched.find((r) => r.decision === final);
  const result: EvalResult = { decision: final, reason: 'rule', rule_id: first?.id ?? null };
  if (final === 'ask') {
    return { ...result, approval: clone(matched.find((r) => r.decision === 'ask' && r.approval)?.approval ?? mandate.approval) };
  }
  if (final === 'allow' && critical) {
    const unconfirmed = matched.find((r) => r.allow_critical !== true);
    if (unconfirmed) {
      return { decision: 'ask', reason: 'critical_demotion', rule_id: unconfirmed.id, approval: clone(mandate.approval) };
    }
  }
  return result;
}

/** matchingRules implements step 2 in document order at a given local time. */
export function matchingRules(mandate: MandateDraft, req: EvalRequest, local: LocalTime): Rule[] {
  return mandate.rules.filter(
    (r) => resourceMatches(r, req.resource) && coversAction(r, req.action) && conditionsMet(r, local) && constraintsMet(r, req.parameters),
  );
}

/** evaluate evaluates req against mandate per SPEC-v0 section 4. */
export function evaluate(mandate: MandateDraft, req: EvalRequest): EvalResult {
  if (checkDraft(mandate).length > 0) return deny('invalid_mandate');
  const pre = precheck(mandate, req);
  if ('decision' in pre) return pre;
  const matched = matchingRules(mandate, req, pre.local);
  if (matched.length === 0) return deny('no_match');
  return decide(mandate, matched, pre.critical);
}
