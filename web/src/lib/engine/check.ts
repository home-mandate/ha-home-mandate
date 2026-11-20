// SPDX-License-Identifier: AGPL-3.0-or-later

// checkDraft finds what makes a mandate draft invalid per SPEC-v0 section 3.1,
// as far as the editor can produce it, with a JSON pointer per problem so the editor can
// show it at the field. The server is the authority: it parses every stored version with
// spec/evaluator and rejects anything this check misses.

import type { Approval, MandateDraft, Rule } from '../api/types.ts';
import { displayable } from './displaytext.ts';
import { type ConstraintSpec, knownAction, lookupAction, parameterFits, validValue } from './vocabulary.ts';

export type ProblemCode =
  | 'required'
  | 'format'
  | 'range'
  | 'duplicate'
  | 'unknown'
  | 'order'
  | 'empty'
  | 'too_many'
  | 'any_alone'
  | 'vocabulary'
  | 'allow_only'
  | 'ask_only';

export interface Problem {
  /** JSON pointer into the draft, e.g. "/rules/2/actions/0". */
  field: string;
  code: ProblemCode;
}

export const MAX_RULES = 200;
export const MIN_RATE = 1;
export const MAX_RATE = 1000;
const RULE_ID = /^[A-Za-z0-9_-]{1,64}$/;
// Opaque identifiers: printable ASCII without space (SPEC-v0 section 3.4).
const ENTITY_ID = /^[!-~]{1,255}$/;
const AREA = /^[!-~]{1,64}$/;
const PARAMETER = /^[a-z][a-z0-9_]{0,63}$/;
export const MAX_LIMIT = Number.MAX_SAFE_INTEGER;
/** Largest number of permitted values of a constraint (SPEC-v0 section 4.5). */
export const MAX_ONE_OF = 32;
const EXTENSION_CATEGORY = /^[a-z][a-z0-9_-]*:[a-z][a-z0-9_]*$/;
const ACTION = /^(\*|[a-z][a-z0-9_]{0,63})$/;
const TIMEOUT = /^PT(?:([0-9]{1,5})H)?(?:([0-9]{1,5})M)?(?:([0-9]{1,5})S)?$/;
const TIME_WINDOW = /^([01]\d|2[0-3]):([0-5]\d)-([01]\d|2[0-3]):([0-5]\d)$/;
const DATE_TIME = /^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]{1,9})?(?:Z|[+-]([0-9]{2}):([0-9]{2}))$/;
const WEEKDAYS = new Set(['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']);
const DECISIONS = new Set(['allow', 'ask', 'deny']);
const MIN_TIMEOUT_S = 10;
const MAX_TIMEOUT_S = 3600;
const DATE = /^([0-9]{4})-([0-9]{2})-([0-9]{2})$/;
/** Largest number of ranges of dates of a rule (SPEC-v0 section 3.1 item 3). */
export const MAX_DATE_RANGES = 16;
/** Bounds of the limits of a rule of its own (SPEC-v0 section 3.1 item 12). */
const MAX_RULE_PER_DAY = 24_000;
const MIN_INTERVAL_S = 1;
const MAX_INTERVAL_S = 86_400;

function daysInMonth(year: number, month: number): number {
  return new Date(Date.UTC(year, month, 0)).getUTCDate();
}

/**
 * parseDateTime returns the epoch milliseconds of an RFC 3339 timestamp, or NaN. It is as
 * strict as Go's time.Parse(time.RFC3339): Date.parse alone would roll "Feb 30" over to
 * March and accept "24:00".
 */
export function parseDateTime(value: unknown): number {
  const match = typeof value === 'string' ? DATE_TIME.exec(value) : null;
  if (!match) return NaN;
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map(Number) as [number, number, number, number, number, number];
  const offsetHour = Number(match[8] ?? 0);
  const offsetMinute = Number(match[9] ?? 0);
  const valid =
    year !== 0 &&
    !(value as string).endsWith('-00:00') &&
    month >= 1 &&
    month <= 12 &&
    day >= 1 &&
    day <= daysInMonth(year, month) &&
    hour <= 23 &&
    minute <= 59 &&
    second <= 59 &&
    offsetHour <= 23 &&
    offsetMinute <= 59;
  return valid ? Date.parse(value as string) : NaN;
}

/** validDate: a calendar date "YYYY-MM-DD" that exists, not in year 0000 (SPEC-v0 section 3.1 item 0). */
export function validDate(value: unknown): value is string {
  const match = typeof value === 'string' ? DATE.exec(value) : null;
  if (!match) return false;
  const [year, month, day] = match.slice(1, 4).map(Number) as [number, number, number];
  return year !== 0 && month >= 1 && month <= 12 && day >= 1 && day <= daysInMonth(year, month);
}

/** timeoutSeconds returns the seconds of "PT[nH][nM][nS]" with at least one component, or NaN. */
export function timeoutSeconds(value: unknown): number {
  const match = typeof value === 'string' ? TIMEOUT.exec(value) : null;
  if (!match || match.slice(1).every((part) => part === undefined)) return NaN;
  return Number(match[1] ?? 0) * 3600 + Number(match[2] ?? 0) * 60 + Number(match[3] ?? 0);
}

/** windowMinutes returns start and end of "HH:MM-HH:MM" in minutes, or null if malformed. */
export function windowMinutes(value: unknown): { start: number; end: number } | null {
  const match = typeof value === 'string' ? TIME_WINDOW.exec(value) : null;
  if (!match) return null;
  const [, h1, m1, h2, m2] = match.map(Number) as [number, number, number, number, number];
  return { start: h1 * 60 + m1, end: h2 * 60 + m2 };
}

/** uniqueStrings reports, per position, entries that are not strings or repeat earlier ones. */
function eachUnique(
  items: unknown[],
  at: string,
  check: (item: string, field: string) => Problem[],
): Problem[] {
  const seen = new Set<string>();
  return items.flatMap((item, i) => {
    const field = `${at}/${i}`;
    if (typeof item !== 'string') return [{ field, code: 'format' as const }];
    if (seen.has(item)) return [{ field, code: 'duplicate' as const }];
    seen.add(item);
    return check(item, field);
  });
}

function checkApproval(approval: Approval | undefined, at: string): Problem[] {
  if (approval === undefined || approval === null || typeof approval !== 'object') return [{ field: at, code: 'required' }];
  const problems: Problem[] = [];
  const seconds = timeoutSeconds(approval.timeout);
  if (Number.isNaN(seconds)) problems.push({ field: `${at}/timeout`, code: 'format' });
  else if (seconds < MIN_TIMEOUT_S || seconds > MAX_TIMEOUT_S) problems.push({ field: `${at}/timeout`, code: 'range' });
  const approvers: unknown[] = Array.isArray(approval.approvers) ? approval.approvers : [];
  if (approvers.length === 0) problems.push({ field: `${at}/approvers`, code: 'required' });
  problems.push(
    ...eachUnique(approvers, `${at}/approvers`, (id, field) =>
      [...id].length > 64 || !displayable(id) ? [{ field, code: 'format' }] : [],
    ),
  );
  return problems;
}

function checkResource(rule: Rule, at: string): Problem[] {
  const r = (rule.resource ?? {}) as Record<string, unknown>;
  const keys = Object.keys(r).filter((k) => r[k] !== undefined);
  if (keys.length === 0) return [{ field: at, code: 'required' }];
  if ('any' in r && r.any !== undefined) return keys.length === 1 && r.any === true ? [] : [{ field: at, code: 'any_alone' }];
  const problems: Problem[] = [];
  if (r.entity_id !== undefined && (typeof r.entity_id !== 'string' || !ENTITY_ID.test(r.entity_id))) {
    problems.push({ field: `${at}/entity_id`, code: 'format' });
  }
  if (r.area !== undefined && (typeof r.area !== 'string' || !AREA.test(r.area))) {
    problems.push({ field: `${at}/area`, code: 'format' });
  }
  if (r.category !== undefined) {
    const known = typeof r.category === 'string' && lookupAction(r.category, 'read').categoryKnown;
    const extension = typeof r.category === 'string' && EXTENSION_CATEGORY.test(r.category);
    if (!known && !extension) problems.push({ field: `${at}/category`, code: 'unknown' });
  }
  return problems;
}

function checkActions(rule: Rule, at: string): Problem[] {
  const actions: unknown[] = Array.isArray(rule.actions) ? rule.actions : [];
  if (actions.length === 0) return [{ field: at, code: 'required' }];
  const category = (rule.resource as { category?: unknown } | undefined)?.category;
  const core = typeof category === 'string' && lookupAction(category, 'read').categoryKnown;
  return eachUnique(actions, at, (action, field) => {
    if (!ACTION.test(action)) return [{ field, code: 'format' }];
    if (action === '*') return [];
    // A core category has its own actions; without a category any action of the vocabulary
    // will do; an extension category is not checked (SPEC-v0 section 3.1 item 4).
    const known = core ? lookupAction(category, action).actionKnown : category !== undefined || knownAction(action);
    return known ? [] : [{ field, code: 'vocabulary' }];
  });
}

/** checkConstraints implements SPEC-v0 section 3.1 item 10. */
function checkConstraints(rule: Rule, at: string): Problem[] {
  const constraints = rule.constraints as unknown;
  if (constraints === undefined) return [];
  if (constraints === null || typeof constraints !== 'object' || Object.keys(constraints).length === 0) return [{ field: at, code: 'required' }];
  const actions: unknown[] = Array.isArray(rule.actions) ? rule.actions : [];
  if (rule.decision !== 'allow' || actions.includes('*')) return [{ field: at, code: 'allow_only' }];
  const category = (rule.resource as { category?: unknown } | undefined)?.category;
  const core = typeof category === 'string' && lookupAction(category, 'read').categoryKnown;
  const checked = category === undefined || core;
  return Object.entries(constraints as Record<string, unknown>).flatMap(([name, given]): Problem[] => {
    const field = `${at}/${name}`;
    if (!PARAMETER.test(name) || given === null || typeof given !== 'object') return [{ field, code: 'format' }];
    const list = 'one_of' in given ? 'one_of' : 'none_of' in given ? 'none_of' : undefined;
    const parsed = list ? checkValueList(given as Record<string, unknown>, list, field) : checkLimits(given as Record<string, unknown>, field);
    if (Array.isArray(parsed)) return parsed;
    const fits = actions.every((a) => typeof a === 'string' && parameterFits(core ? category : undefined, a, name, parsed));
    return checked && !fits ? [{ field, code: 'unknown' }] : [];
  });
}

/** checkLimits reads limits of an integer parameter: min, max or both. */
function checkLimits(given: Record<string, unknown>, field: string): ConstraintSpec | Problem[] {
  const { min, max, ...rest } = given;
  if (Object.keys(rest).length > 0) return [{ field, code: 'format' }];
  if (min === undefined && max === undefined) return [{ field, code: 'required' }];
  const limit = (v: unknown) => v === undefined || (Number.isInteger(v) && Math.abs(v as number) <= MAX_LIMIT);
  if (!limit(min) || !limit(max)) return [{ field, code: 'format' }];
  if (min !== undefined && max !== undefined && (min as number) > (max as number)) return [{ field, code: 'order' }];
  return given as ConstraintSpec;
}

/**
 * checkValueList reads a list of permitted (one_of) or excluded (none_of) values: 1 to 32
 * distinct values of one type, alone (SPEC-v0 section 4.5).
 */
function checkValueList(given: Record<string, unknown>, member: 'one_of' | 'none_of', field: string): ConstraintSpec | Problem[] {
  const { [member]: values, ...rest } = given;
  if (Object.keys(rest).length > 0 || !Array.isArray(values)) return [{ field, code: 'format' }];
  if (values.length === 0) return [{ field: `${field}/${member}`, code: 'required' }];
  if (values.length > MAX_ONE_OF) return [{ field: `${field}/${member}`, code: 'too_many' }];
  const seen = new Set<unknown>();
  for (const [i, v] of values.entries()) {
    if (!validValue(v) || typeof v !== typeof values[0]) return [{ field: `${field}/${member}/${i}`, code: 'format' }];
    if (seen.has(v)) return [{ field: `${field}/${member}/${i}`, code: 'duplicate' }];
    seen.add(v);
  }
  const list = values as (number | string)[];
  return member === 'one_of' ? { one_of: list } : { none_of: list };
}

function checkConditions(rule: Rule, at: string): Problem[] {
  const c = rule.conditions;
  if (c === undefined) return [];
  const problems: Problem[] = [];
  if (c.time_window === undefined && c.weekdays === undefined && c.dates === undefined) return [{ field: at, code: 'required' }];
  if (c.time_window !== undefined) {
    const w = windowMinutes(c.time_window);
    if (!w) problems.push({ field: `${at}/time_window`, code: 'format' });
    else if (w.start === w.end) problems.push({ field: `${at}/time_window`, code: 'empty' });
  }
  if (c.weekdays !== undefined) {
    const days: unknown[] = Array.isArray(c.weekdays) ? c.weekdays : [];
    if (days.length === 0) problems.push({ field: `${at}/weekdays`, code: 'required' });
    problems.push(
      ...eachUnique(days, `${at}/weekdays`, (day, field) => (WEEKDAYS.has(day) ? [] : [{ field, code: 'unknown' }])),
    );
  }
  if (c.dates !== undefined) problems.push(...checkDates(c.dates as unknown, `${at}/dates`));
  return problems;
}

/** checkDates: 1 to 16 distinct ranges of dates that exist, from not after to (SPEC-v0 section 3.1 item 3). */
function checkDates(dates: unknown, at: string): Problem[] {
  if (!Array.isArray(dates) || dates.length === 0) return [{ field: at, code: 'required' }];
  if (dates.length > MAX_DATE_RANGES) return [{ field: at, code: 'too_many' }];
  const seen = new Set<string>();
  return dates.flatMap((range: unknown, i): Problem[] => {
    const field = `${at}/${i}`;
    if (range === null || typeof range !== 'object') return [{ field, code: 'format' }];
    const { from, to, ...rest } = range as Record<string, unknown>;
    if (Object.keys(rest).length > 0 || !validDate(from) || !validDate(to)) return [{ field, code: 'format' }];
    if (from > to) return [{ field, code: 'order' }];
    const key = `${from}/${to}`;
    if (seen.has(key)) return [{ field, code: 'duplicate' }];
    seen.add(key);
    return [];
  });
}

/** checkPeriod: the rule's own valid_from and expires, expires after valid_from (SPEC-v0 section 3.1 items 0 and 6). */
function checkPeriod(rule: Rule, at: string): Problem[] {
  const problems: Problem[] = [];
  const from = rule.valid_from === undefined ? undefined : parseDateTime(rule.valid_from);
  const until = rule.expires === undefined ? undefined : parseDateTime(rule.expires);
  if (from !== undefined && Number.isNaN(from)) problems.push({ field: `${at}/valid_from`, code: 'format' });
  if (until !== undefined && Number.isNaN(until)) problems.push({ field: `${at}/expires`, code: 'format' });
  else if (until !== undefined && from !== undefined && !Number.isNaN(from) && until <= from) problems.push({ field: `${at}/expires`, code: 'order' });
  return problems;
}

/** checkRuleLimits: only on an allow rule, at least one known member, each within its bounds (SPEC-v0 section 3.1 item 12). */
function checkRuleLimits(rule: Rule, at: string): Problem[] {
  const limits = rule.limits as unknown;
  if (limits === undefined) return [];
  if (rule.decision !== 'allow') return [{ field: at, code: 'allow_only' }];
  if (limits === null || typeof limits !== 'object' || Object.keys(limits).length === 0) return [{ field: at, code: 'required' }];
  const { max_actions_per_hour: perHour, max_actions_per_day: perDay, min_interval: interval, ...rest } = limits as Record<string, unknown>;
  if (Object.keys(rest).length > 0) return [{ field: at, code: 'format' }];
  const count = (v: unknown, max: number) => v === undefined || (Number.isInteger(v) && (v as number) >= 1 && (v as number) <= max);
  const problems: Problem[] = [];
  if (!count(perHour, MAX_RATE)) problems.push({ field: `${at}/max_actions_per_hour`, code: 'range' });
  if (!count(perDay, MAX_RULE_PER_DAY)) problems.push({ field: `${at}/max_actions_per_day`, code: 'range' });
  if (interval !== undefined) {
    const seconds = timeoutSeconds(interval);
    if (Number.isNaN(seconds)) problems.push({ field: `${at}/min_interval`, code: 'format' });
    else if (seconds < MIN_INTERVAL_S || seconds > MAX_INTERVAL_S) problems.push({ field: `${at}/min_interval`, code: 'range' });
  }
  return problems;
}

function checkRule(rule: Rule, at: string, ids: Set<string>): Problem[] {
  const problems: Problem[] = [];
  if (typeof rule.id !== 'string' || !RULE_ID.test(rule.id)) problems.push({ field: `${at}/id`, code: 'format' });
  else if (ids.has(rule.id)) problems.push({ field: `${at}/id`, code: 'duplicate' });
  else ids.add(rule.id);
  problems.push(...checkResource(rule, `${at}/resource`), ...checkActions(rule, `${at}/actions`));
  if (!DECISIONS.has(rule.decision)) problems.push({ field: `${at}/decision`, code: 'unknown' });
  const wildcard = Array.isArray(rule.actions) && rule.actions.includes('*');
  if (rule.allow_critical !== undefined && (rule.allow_critical !== true || rule.decision !== 'allow' || wildcard)) {
    problems.push({ field: `${at}/allow_critical`, code: 'allow_only' });
  }
  if (rule.approval !== undefined) {
    if (rule.decision !== 'ask') problems.push({ field: `${at}/approval`, code: 'ask_only' });
    else problems.push(...checkApproval(rule.approval, `${at}/approval`));
  }
  problems.push(
    ...checkConditions(rule, `${at}/conditions`),
    ...checkConstraints(rule, `${at}/constraints`),
    ...checkPeriod(rule, at),
    ...checkRuleLimits(rule, `${at}/limits`),
  );
  return problems;
}

/** checkDraft returns every problem of draft; an empty list means valid for the editor. */
export function checkDraft(draft: MandateDraft): Problem[] {
  const problems: Problem[] = [];
  const limit = draft.limits?.max_actions_per_hour;
  if (!Number.isInteger(limit) || limit < MIN_RATE || limit > MAX_RATE) {
    problems.push({ field: '/limits/max_actions_per_hour', code: 'range' });
  }
  problems.push(...checkApproval(draft.approval, '/approval'));
  // enforce has no other value than false (SPEC-v0 section 3.1 item 11).
  if ('enforce' in draft && draft.enforce !== undefined && (draft.enforce as unknown) !== false) problems.push({ field: '/enforce', code: 'format' });

  const validFrom = parseDateTime(draft.valid_from);
  if (Number.isNaN(validFrom)) problems.push({ field: '/valid_from', code: 'format' });
  if (draft.expires !== undefined) {
    const expires = parseDateTime(draft.expires);
    if (Number.isNaN(expires)) problems.push({ field: '/expires', code: 'format' });
    else if (!Number.isNaN(validFrom) && expires <= validFrom) problems.push({ field: '/expires', code: 'order' });
  }

  const rules: Rule[] = Array.isArray(draft.rules) ? draft.rules : [];
  if (rules.length > MAX_RULES) return [...problems, { field: '/rules', code: 'too_many' }];
  const ids = new Set<string>();
  rules.forEach((rule, i) => problems.push(...checkRule(rule, `/rules/${i}`, ids)));
  return problems;
}
