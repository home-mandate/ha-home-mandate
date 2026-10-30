// SPDX-License-Identifier: AGPL-3.0-or-later

// Evaluation rule of SPEC-v0 section 4, ported from the reference evaluator
// (spec/evaluator) for the editor: preview matrix, save summary, version compare.
// It passes every conformance case of the pinned version of the specification (evaluate.test.ts).
// It never decides anything real: every agent request is decided by the PDP on the server.

import type { Approval, Decision, MandateDraft, Reason, Rule, Weekday } from '../api/types.ts';
import { checkDraft, parseDateTime, windowMinutes } from './check.ts';
import { lookupAction, MAX_PARAMETERS, opaque, parametersAdmitted, type ParameterValue, validParameterName, validValue } from './vocabulary.ts';

export interface EvalRequest {
  /** areas: the set of areas of the resource (SPEC-v0 section 3.4); absent or empty: none. */
  resource: { entity_id: string; category?: string; areas?: readonly string[]; critical?: boolean };
  action: string;
  /** Parameters of the action: integers in the units of the vocabulary, or strings (SPEC-v0 section 4.5). */
  parameters?: Record<string, ParameterValue>;
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
  /** Approval settings, only for "ask" and for a result with confirm. */
  approval?: Approval;
  /**
   * false only for a deny the PEP does not enforce: the mandate is in audit mode, the deny
   * results from its rules and the action is not critical (SPEC-v0 section 4.6).
   */
  enforced?: false;
  /**
   * true only for such a deny while a matching rule decides ask: the PEP executes it only
   * after a confirmation, as an ask (SPEC-v0 sections 4.1 and 4.6).
   */
  confirm?: true;
}

/** Household local time: weekday and minute since midnight. */
export interface LocalTime {
  weekday: Weekday;
  minute: number;
}

const MAX_AREAS = 16;
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
    (r.area === undefined || (resource.areas ?? []).includes(r.area))
  );
}

/**
 * constraintsMet: the request carries every constrained parameter, within the limits of an
 * integer or equal to one of the permitted values (an integer never equals a string).
 */
export function constraintsMet(rule: Rule, parameters: EvalRequest['parameters']): boolean {
  return Object.entries(rule.constraints ?? {}).every(([name, c]) => {
    const value = parameters !== undefined && Object.hasOwn(parameters, name) ? parameters[name] : undefined;
    if (value === undefined) return false;
    if ('one_of' in c) return c.one_of.includes(value);
    const { min = -Infinity, max = Infinity } = c;
    return typeof value === 'number' && value >= min && value <= max;
  });
}

/** validAreas: a set of at most 16 area identifiers without duplicates. */
function validAreas(areas: unknown): boolean {
  if (areas === undefined) return true;
  if (!Array.isArray(areas) || areas.length > MAX_AREAS) return false;
  return areas.every((area, i) => opaque(area, 64) && areas.indexOf(area) === i);
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
  const parameters = req.parameters ?? {};
  const validParameters =
    typeof parameters === 'object' &&
    parameters !== null &&
    Object.keys(parameters).length <= MAX_PARAMETERS &&
    Object.entries(parameters).every(([name, value]) => validParameterName(name) && validValue(value)) &&
    parametersAdmitted(res.category, req.action, parameters);
  const validRequest = opaque(res.entity_id, 255) && validAreas(res.areas) && validParameters;
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

/** askApproval is the approval of the first matching ask rule that has its own, otherwise the mandate's (section 4.1). */
function askApproval(mandate: MandateDraft, matched: Rule[]): Approval {
  return clone(matched.find((r) => r.decision === 'ask' && r.approval)?.approval ?? mandate.approval);
}

/** decide implements steps 4 and 5 and section 4.1 for a non-empty list of matching rules. */
export function decide(mandate: MandateDraft, matched: Rule[], critical: boolean): EvalResult {
  const final = matched.reduce<Decision>((d, r) => (STRICTNESS[r.decision] > STRICTNESS[d] ? r.decision : d), 'allow');
  const first = matched.find((r) => r.decision === final);
  const result: EvalResult = { decision: final, reason: 'rule', rule_id: first?.id ?? null };
  if (final === 'ask') return { ...result, approval: askApproval(mandate, matched) };
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
  const result = matched.length === 0 ? deny('no_match') : decide(mandate, matched, pre.critical);
  // Audit mode (section 4.6, step 6): only a deny by the rules, never one of a critical
  // action; it still requires a confirmation if a matching rule decides ask.
  if (mandate.enforce !== false || result.decision !== 'deny' || pre.critical) return result;
  if (!matched.some((r) => r.decision === 'ask')) return { ...result, enforced: false };
  return { ...result, enforced: false, confirm: true, approval: askApproval(mandate, matched) };
}
