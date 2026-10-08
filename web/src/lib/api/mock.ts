// SPDX-License-Identifier: AGPL-3.0-or-later

// In-memory ApiClient that behaves like internal/api on the fixtures: for component
// tests, Playwright against the static build and building the UI before the backend.
// State is replaced, never changed in place, and every answer is a copy. `control`
// simulates what only the server can cause: approval requests, HA going down, a broken
// audit chain.

import { cleanSearch } from '../audit/filters.ts';
import { cleanUntrusted } from '../untrusted.ts';
import { checkDraft, timeoutSeconds } from '../engine/check.ts';
import { canonical, needsCriticalConfirmation } from '../engine/vocabulary.ts';
import { withApprovers } from '../mandate/placeholder.ts';
import { RESERVED_PREFIX, TEMPLATE_NAME } from '../mandate/template.ts';
import { ApiError, type ApiClient, type EventHandlers } from './client.ts';
import type { EventsConnection, EventsState } from './events.ts';
import {
  agentsFixture,
  approvalsHistoryFixture,
  approvalsOpenFixture,
  approversFixture,
  auditFixture,
  defaultsFixture,
  devicesFixture,
  digestOf,
  NOW,
  sessionFixture,
  systemFixture,
  templatesFixture,
  voiceAssistantMandate,
  WORST_NAME,
  WORST_REASON,
} from './fixtures.ts';
import type {
  Agent,
  ApiErrorCode,
  ApprovalHistoryEntry,
  ApprovalRequest,
  Approvals,
  Approver,
  ApproverList,
  ApproverUpdate,
  ReachChannel,
  Rename,
  Rule,
  StaleReference,
  AuditEntry,
  AuditEvent,
  AuditQuery,
  DeviceCatalog,
  Defaults,
  MandateDetail,
  MandateDocument,
  MandateDraft,
  MandateVersion,
  RulesFrom,
  PairingCandidate,
  ServerEvent,
  Session,
  SystemStatus,
  Template,
  TemplateSummary,
} from './types.ts';

const STATUS: Record<ApiErrorCode, number> = {
  unauthenticated: 401,
  forbidden: 403,
  csrf_invalid: 403,
  not_found: 404,
  conflict: 409,
  invalid_input: 400,
  invalid_mandate: 422,
  critical_confirmation_required: 422,
  builtin_template: 409,
  no_approvers: 422,
  pairing_code_invalid: 400,
  pairing_code_expired: 410,
  pairing_locked: 429,
  too_large: 413,
  rate_limited: 429,
  unavailable: 0,
  internal: 500,
};

const fail = (code: ApiErrorCode, field?: string, retryAfter?: number): never => {
  throw new ApiError(code, STATUS[code], field, retryAfter);
};

/** Pairing codes of the mock: one waiting agent, one expired code. */
export const MOCK_PAIRING_CODE = 'BCDF-GHJK';
export const MOCK_EXPIRED_CODE = 'ZZZZ-ZZZZ';
const PAIRING_ATTEMPTS = 5;
const PAIRING_LOCK_S = 600;
const pairingFixture: PairingCandidate = {
  pairing_id: 'pg-kitchen-tablet',
  claimed_name: 'Küchen-Tablet',
  client: 'kitchen-tablet',
  client_verified: false,
  requested_at: '2026-10-02T17:40:00Z',
  expires_at: '2026-10-02T17:50:00Z',
  requested_from: '192.168.1.42',
};

interface StoredMandate {
  name: string;
  status: 'active' | 'revoked';
  /** Newest first. */
  versions: { meta: MandateVersion; document: MandateDocument }[];
}

interface State {
  session: Session;
  system: SystemStatus;
  defaults: Defaults;
  devices: DeviceCatalog;
  /** Renames nobody resolved: current entity ID → former IDs, nearest first. */
  renames: Record<string, string[]>;
  agents: Agent[];
  mandates: Record<string, StoredMandate>;
  templates: Template[];
  audit: AuditEntry[];
  approvals: Approvals;
  approvers: Omit<ApproverList, 'version'>;
  /** Counts the changes of the approvers; its hex form is the version. */
  approversVersion: number;
  pairing: { codes: Record<string, 'open' | 'expired'>; wrong: number; lockedUntil: number };
}

export interface MockControls {
  emit(event: ServerEvent): void;
  openApproval(request: ApprovalRequest): void;
  closeApproval(id: string, outcome: ApprovalHistoryEntry['outcome'], byName: string | null, via?: 'push' | 'ui'): void;
  setHaConnected(connected: boolean): void;
  /** As a host clock behind the newest audit entry. */
  setClockBehind(behind: boolean): void;
  /** As renames of Home Assistant that cannot be stored (since: when it began to fail). */
  setDirectoryStore(since: string | null, overflow: boolean): void;
  /** As Home Assistant renaming this many entities in the last hour. */
  setRenamesLastHour(count: number): void;
  /** As a rename in Home Assistant: the device gets another entity ID, rules keep the old one. */
  renameDevice(from: string, to: string): void;
  breakChain(seq: number): void;
  setEmergencyStop(active: boolean): Promise<void>;
}

export type MockClient = ApiClient & { control: MockControls };

export interface MockOptions {
  /** Makes a method fail with this code, e.g. { agents: 'unavailable' }. */
  failures?: Partial<Record<keyof ApiClient, ApiErrorCode>>;
  /** State the event stream reaches after connecting; default "open". */
  eventsState?: EventsState;
  /** Starts a household without agents, mandates, log entries and requests (onboarding). */
  empty?: boolean;
  /** Gives the voice assistant WORST_NAME everywhere, an open request with WORST_REASON, and the pairing candidate WORST_NAME. */
  hostile?: boolean;
  /** Direct mode: signed in through Home-Mandate, so the session offers to sign out. */
  direct?: boolean;
  now?: () => Date;
}

const HOUR_MS = 3_600_000;

/** Version of the approvers of a new mock household (ApproverList.version). */
export const MOCK_APPROVERS_VERSION = '1'.padStart(32, '0');

/** householdDay is the calendar day of a moment in the household's time zone, e.g. "2026-10-02". */
function householdDay(at: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(at);
}

const normalizeCode = (code: string) => code.toUpperCase().replace(/[\s-]/g, '');

const HOSTILE_AGENT = 'pair:voice-assistant';

/** renameAgent returns value with every {client_id, display_name} of the agent renamed. */
function renameAgent<T>(value: T, clientId: string, name: string): T {
  if (Array.isArray(value)) return value.map((v: unknown) => renameAgent(v, clientId, name)) as T;
  if (value === null || typeof value !== 'object') return value;
  const out: Record<string, unknown> = Object.fromEntries(Object.entries(value).map(([k, v]) => [k, renameAgent(v, clientId, name)]));
  return (out.client_id === clientId && 'display_name' in out ? { ...out, display_name: name } : out) as T;
}

/** hostileState is the household of the option "hostile". */
function hostileState(state: State): State {
  const renamed = renameAgent(state, HOSTILE_AGENT, WORST_NAME);
  const request: ApprovalRequest = {
    ...approvalsOpenFixture[0]!,
    id: 'apr-hostile',
    agent: { client_id: HOSTILE_AGENT, display_name: WORST_NAME },
    reason: WORST_REASON,
    // Service data from the agent: shown cleaned, as hostile as the name.
    params: [
      { name: 'brightness_pct', value: '100' },
      { name: WORST_NAME, value: WORST_REASON },
    ],
    critical: false,
    can_answer: true,
  };
  return { ...renamed, approvals: { ...renamed.approvals, open: [...renamed.approvals.open, request] } };
}

/** The fixture mandates; the voice assistant's rules came from the template of that name, the others' are of before origins. */
function initialMandates(templates: readonly Template[]): Record<string, StoredMandate> {
  const voice = templates.find((t) => t.name === 'voice-assistant');
  return Object.fromEntries(
    agentsFixture
      .filter((a) => a.mandate !== null)
      .map((a, i): [string, StoredMandate] => {
        const id = a.mandate?.id ?? '';
        const document: MandateDocument = { ...voiceAssistantMandate, id, agent: { client_id: a.client_id, display_name: a.display_name } };
        const origin: Pick<MandateVersion, 'origin' | 'template' | 'template_digest'> =
          id === 'mandate-voice' && voice
            ? { origin: 'template', template: voice.name, template_digest: voice.digest }
            : { origin: 'unknown', template: null, template_digest: null };
        const meta = { number: 1, digest: `sha256:fixture-${i}`, created_at: document.created_at, created_by: 'u-admin', created_by_name: 'Markus', ...origin };
        return [id, { name: a.mandate?.name ?? id, status: 'active', versions: [{ meta, document }] }];
      }),
  );
}

const draftOf = (d: MandateDocument): MandateDraft => ({
  rules: d.rules,
  approval: d.approval,
  limits: d.limits,
  valid_from: d.valid_from,
  ...(d.expires === undefined ? {} : { expires: d.expires }),
});

/** rulesFrom is the template a mandate's rules were last taken from (versions newest first). */
function rulesFrom(m: StoredMandate): RulesFrom | null {
  const i = m.versions.findIndex((v) => v.meta.origin === 'template');
  const v = m.versions[i];
  if (!v || v.meta.template === null || v.meta.template_digest === null) return null;
  return { template: v.meta.template, template_digest: v.meta.template_digest, at: v.meta.created_at, edited_since: i > 0 };
}

const EDITED: Pick<MandateVersion, 'origin' | 'template' | 'template_digest'> = { origin: 'edit', template: null, template_digest: null };

/** validate rejects a draft the server would reject, with the pointer under /draft. */
function validate(draft: MandateDraft): void {
  const [first] = checkDraft(draft);
  if (first) fail('invalid_mandate', `/draft${first.field}`);
}

const DIGEST = /^sha256:[0-9a-f]{64}$/;
const FNV_OFFSET = 0x811c9dc5;
const FNV_PRIME = 0x01000193;
const DIGEST_WORDS = 8;

/** contentDigest stands in for the server's SHA-256: the same content gives the same "sha256:<64 hex>". */
export function contentDigest(text: string): string {
  const words = Array.from({ length: DIGEST_WORDS }, (_, seed) => {
    let h = (FNV_OFFSET ^ seed) >>> 0;
    for (const ch of text) h = Math.imul(h ^ (ch.codePointAt(0) ?? 0), FNV_PRIME) >>> 0;
    return h.toString(16).padStart(8, '0');
  });
  return `sha256:${words.join('')}`;
}

/** As the server stores a template: without expiry, with its rule count and digest. */
/** withoutExpires is the draft without an expiry: a template carries none. */
function withoutExpires(d: MandateDraft): MandateDraft {
  return { rules: d.rules, approval: d.approval, limits: d.limits, valid_from: d.valid_from };
}

function storedTemplate(t: Template): Template {
  const draft = withoutExpires(t.draft);
  return { ...t, draft, rule_count: draft.rules.length, digest: contentDigest(canonical(draft)) };
}

/** Base templates first in their order, then the household's own by name (as GET api/templates). */
function templateOrder(list: readonly Template[]): Template[] {
  const builtins = list.filter((t) => t.builtin);
  const own = list.filter((t) => !t.builtin).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  return [...builtins, ...own];
}

const MAX_DEVICES = 5;
const SERVICE = /^[a-z0-9_]{1,64}$/;

/** checkApprover applies the server's rules for saving an approver (decision F2). */
function checkApprover(update: ApproverUpdate, admin: boolean, known: readonly string[]): void {
  const services = update.devices.map((d) => d.service);
  if (services.length > MAX_DEVICES || new Set(services).size !== services.length) fail('invalid_input', '/devices');
  if (services.some((s) => !SERVICE.test(s) || !known.includes(s))) fail('invalid_input', '/devices');
  if (services.length === 0 && !update.ui) fail('invalid_input', '/devices');
  if (update.ui_critical && !update.ui) fail('invalid_input', '/ui_critical');
  if (update.ui && !admin) fail('invalid_input', '/ui');
}

/**
 * reachOf is what the server reports per kind of request (decision S9): by push when a
 * device gets it, else in the UI for an admin (critical only with ui_critical), else none.
 */
function reachOf(update: ApproverUpdate, admin: boolean): Approver['reach'] {
  const ui = update.ui && admin;
  const channel = (push: boolean, inUi: boolean): ReachChannel => (push ? 'push' : inUi ? 'ui' : 'none');
  return {
    normal: channel(update.devices.length > 0, ui),
    critical: channel(update.devices.some((d) => d.critical), ui && update.ui_critical),
  };
}

/** As the server: rules that name a device or area the catalog does not have. */
function staleReferences(rules: readonly Rule[], catalog: DeviceCatalog, renames: Readonly<Record<string, readonly string[]>>): StaleReference[] {
  const renamedTo = new Map(Object.entries(renames).flatMap(([to, formers]) => formers.map((f) => [f, to] as const)));
  const out: StaleReference[] = [];
  rules.forEach((r, i) => {
    const { entity_id: entity, area } = r.resource;
    const ref: StaleReference = { rule: i, rule_id: r.id };
    if (entity !== undefined && !catalog.devices.some((d) => d.entity_id === entity)) {
      ref.entity_id = entity;
      const to = renamedTo.get(entity);
      if (to !== undefined) ref.renamed_to = to;
    }
    if (area !== undefined && !catalog.areas.some((a) => a.id === area)) ref.area = area;
    if (ref.entity_id !== undefined || ref.area !== undefined) out.push(ref);
  });
  return out;
}

/**
 * searchMatcher is the search of the audit log as the server does it: the text, ignoring
 * case, in the entry's entity_id, area_id, agent name or client_id, or in the name of its
 * device or area in the current catalog. Null means no search.
 */
function searchMatcher(text: string | undefined, catalog: DeviceCatalog): ((e: AuditEntry) => boolean) | null {
  const q = cleanSearch(text ?? '') ?? fail('invalid_input', '/q');
  if (q === '') return null;
  const needle = q.toLowerCase();
  const has = (value: string | null | undefined) => value?.toLowerCase().includes(needle) ?? false;
  // Names as the UI shows them, so search and display agree.
  const devices = new Set(catalog.devices.filter((d) => has(cleanUntrusted(d.name))).map((d) => d.entity_id));
  const areas = new Set(catalog.areas.filter((a) => has(cleanUntrusted(a.name))).map((a) => a.id));
  return (e) => {
    const res = e.request?.resource;
    return (
      has(res?.entity_id) ||
      has(res?.area) ||
      (res !== undefined && devices.has(res.entity_id)) ||
      (res?.area !== undefined && areas.has(res.area)) ||
      has(cleanUntrusted(e.agent?.display_name)) ||
      has(e.agent?.client_id)
    );
  };
}

/** matchesQuery applies the filters; the cursor (before) is applied after counting. */
function matchesQuery(e: AuditEntry, q: AuditQuery): boolean {
  const res = e.request?.resource;
  const decision = e.evaluation?.reason === 'no_match' ? 'default' : e.evaluation?.decision;
  const at = Date.parse(e.recorded_at);
  return (
    (q.since === undefined || at >= Date.parse(q.since)) &&
    (q.until === undefined || at < Date.parse(q.until)) &&
    (q.agent === undefined || e.agent?.client_id === q.agent) &&
    (q.device === undefined || res?.entity_id === q.device || res?.area === q.device) &&
    (q.group === undefined || (q.group === 'decision') === (e.event === 'decision')) &&
    (q.event === undefined || e.event === q.event) &&
    (q.decisions === undefined || q.decisions.length === 0 || (decision !== undefined && q.decisions.includes(decision)))
  );
}

export function createMockClient(options: MockOptions = {}): MockClient {
  const now = options.now ?? (() => new Date(NOW));
  const pairingCandidate: PairingCandidate = options.hostile ? { ...pairingFixture, claimed_name: WORST_NAME } : pairingFixture;
  const initial: State = {
    session: sessionFixture,
    system: systemFixture,
    defaults: defaultsFixture,
    devices: devicesFixture,
    renames: {},
    agents: options.empty ? [] : agentsFixture,
    mandates: options.empty ? {} : initialMandates(templatesFixture.map(storedTemplate)),
    templates: templatesFixture.map(storedTemplate),
    audit: options.empty ? [] : auditFixture,
    approvals: options.empty ? { open: [], history: [] } : { open: approvalsOpenFixture, history: approvalsHistoryFixture },
    approvers: approversFixture,
    approversVersion: 1,
    pairing: { codes: { [normalizeCode(MOCK_PAIRING_CODE)]: 'open', [normalizeCode(MOCK_EXPIRED_CODE)]: 'expired' }, wrong: 0, lockedUntil: 0 },
  };
  let state = options.hostile ? hostileState(initial) : initial;
  let counter = 0;
  const listeners = new Set<EventHandlers>();

  const copy = <T>(value: T): T => structuredClone(value);
  const emit = (event: ServerEvent) => listeners.forEach((l) => l.onEvent(copy(event)));
  const user = () => state.session.user;
  const setSystem = (system: SystemStatus) => {
    state = { ...state, system };
    emit({ type: 'system', system });
  };

  function log(event: AuditEvent, extra: Partial<AuditEntry> = {}): void {
    const seq = (state.audit.at(-1)?.seq ?? 0) + 1;
    const entry: AuditEntry = {
      id: `mock-${seq}`,
      seq,
      recorded_at: now().toISOString(),
      event,
      actor: { kind: 'user', id: user().id, name: user().name },
      digest: digestOf(seq),
      prev: digestOf(seq - 1),
      ...extra,
    };
    state = { ...state, audit: [...state.audit, entry] };
    emit({ type: 'audit.appended', seq });
  }

  function stored(id: string): StoredMandate {
    return state.mandates[id] ?? fail('not_found');
  }

  function detail(id: string): MandateDetail {
    const m = stored(id);
    const [current] = m.versions;
    if (!current) return fail('internal');
    const doc = current.document;
    return copy({
      summary: {
        id,
        name: m.name,
        client_id: doc.agent.client_id,
        agent_display_name: doc.agent.display_name,
        status: m.status,
        digest: current.meta.digest,
        rule_count: doc.rules.length,
        valid_from: doc.valid_from,
        expires: doc.expires ?? null,
        max_actions_per_hour: doc.limits.max_actions_per_hour,
        updated_at: current.meta.created_at,
        stale_references: m.status === 'active' ? staleReferences(doc.rules, state.devices, state.renames) : [],
        rules_from: rulesFrom(m),
      },
      document: doc,
      versions: m.versions.map((v) => v.meta),
    });
  }

  function putStored(id: string, m: StoredMandate): void {
    state = {
      ...state,
      mandates: { ...state.mandates, [id]: m },
      agents: state.agents.map((a) =>
        a.mandate?.id === id ? { ...a, mandate: { id, name: m.name, status: m.status, max_actions_per_hour: null, digest: '', rules_from: null } } : a,
      ),
    };
    emit({ type: 'mandates.changed', id });
  }

  /**
   * storeVersion adds a version after the conflict and U9 checks of the server; a draft
   * equal to the current one stores none (the name is still taken).
   */
  function storeVersion(
    id: string,
    baseDigest: string,
    draft: MandateDraft,
    confirm: boolean | undefined,
    name?: string,
    origin: Pick<MandateVersion, 'origin' | 'template' | 'template_digest'> = EDITED,
  ) {
    const m = stored(id);
    const [current] = m.versions;
    if (!current || m.status !== 'active' || current.meta.digest !== baseDigest) return fail('conflict');
    validate(draft);
    if (needsCriticalConfirmation(draftOf(current.document), draft) && confirm !== true) {
      return fail('critical_confirmation_required');
    }
    const unchanged = canonical(draftOf(current.document)) === canonical(draft);
    if (unchanged) {
      putStored(id, { ...m, name: name ?? m.name });
      return detail(id);
    }
    const createdAt = now().toISOString();
    const document: MandateDocument = { ...current.document, ...draft, created_by: user().id, created_at: createdAt };
    const meta = {
      number: m.versions.length + 1,
      digest: `sha256:mock-${++counter}`,
      created_at: createdAt,
      created_by: user().id,
      created_by_name: user().name,
      ...origin,
    };
    putStored(id, { ...m, name: name ?? m.name, versions: [{ meta, document }, ...m.versions] });
    log('mandate.updated', { agent: document.agent, mandate: { id, digest: meta.digest, previous_digest: current.meta.digest } });
    return detail(id);
  }

  /**
   * present is an agent as the API shows it: activity counted from the log (the household
   * day in its time zone, the last 60 minutes) and the limit of its current mandate.
   */
  function present(a: Agent): Agent {
    const at = now();
    const day = householdDay(at, state.session.household.time_zone);
    const requests = state.audit.filter((e) => e.event === 'decision' && e.agent?.client_id === a.client_id);
    const m = a.mandate && stored(a.mandate.id);
    const current = m?.versions[0];
    const mandate = a.mandate && {
      ...a.mandate,
      max_actions_per_hour: current?.document.limits?.max_actions_per_hour ?? null,
      digest: current?.meta.digest ?? '',
      rules_from: m ? rulesFrom(m) : null,
    };
    return {
      ...a,
      mandate,
      requests_today: requests.filter((e) => householdDay(new Date(e.recorded_at), state.session.household.time_zone) === day).length,
      actions_last_hour: requests.filter((e) => at.getTime() - Date.parse(e.recorded_at) < HOUR_MS).length,
    };
  }

  function setMandateStatus(id: string, status: 'active' | 'revoked'): void {
    putStored(id, { ...stored(id), status });
  }

  /** A template to make a mandate from: hidden base templates are refused, as at admission. */
  function template(name: string, field = '/template'): Template {
    const t = state.templates.find((x) => x.name === name);
    return t && !t.hidden ? t : fail('invalid_input', field);
  }

  /**
   * resolved is a template's draft ready for a mandate: the approvers placeholder stands for
   * the admitting human and every approver set up, without duplicates (no_approvers if none).
   */
  function resolved(name: string): MandateDraft {
    const people = [...new Set([user().id, ...state.approvers.approvers.map((a) => a.user_id)])];
    return withApprovers(template(name).draft, people) ?? fail('no_approvers');
  }

  /** The server asks for the separate confirmation when a template allows critical actions (U9). */
  function checkCritical(name: string, confirm: boolean | undefined): void {
    if (needsCriticalConfirmation(null, template(name).draft) && confirm !== true) fail('critical_confirmation_required');
  }

  function createFromTemplate(clientId: string, name: string, mandateName?: string): string {
    const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
    const draft = resolved(name);
    const createdAt = now().toISOString();
    const id = `mandate-mock-${++counter}`;
    const document: MandateDocument = {
      ...voiceAssistantMandate,
      ...draft,
      valid_from: createdAt,
      id,
      agent: { client_id: agent.client_id, display_name: agent.display_name },
      created_by: user().id,
      created_at: createdAt,
    };
    const meta = {
      number: 1,
      digest: `sha256:mock-${counter}`,
      created_at: createdAt,
      created_by: user().id,
      created_by_name: user().name,
      origin: 'template' as const,
      template: name,
      template_digest: template(name).digest,
    };
    state = {
      ...state,
      agents: state.agents.map((a) =>
        a.client_id === clientId ? { ...a, mandate: { id, name: '', status: 'active' as const, max_actions_per_hour: null, digest: '', rules_from: null } } : a,
      ),
    };
    // As admission: named after the agent unless the human gave a name.
    putStored(id, { name: mandateName?.trim() || agent.display_name, status: 'active', versions: [{ meta, document }] });
    log('mandate.created', { agent: document.agent, mandate: { id, digest: meta.digest } });
    return id;
  }

  function checkPairing(code: string): string {
    const nowS = now().getTime() / 1000;
    const { pairing } = state;
    if (pairing.lockedUntil > nowS) return fail('pairing_locked', undefined, Math.ceil(pairing.lockedUntil - nowS));
    const key = normalizeCode(code);
    const status = pairing.codes[key];
    if (status === 'expired') return fail('pairing_code_expired');
    if (status !== 'open') {
      const wrong = pairing.wrong + 1;
      const locked = wrong >= PAIRING_ATTEMPTS;
      state = { ...state, pairing: { ...pairing, wrong: locked ? 0 : wrong, lockedUntil: locked ? nowS + PAIRING_LOCK_S : 0 } };
      return locked ? fail('pairing_locked', undefined, PAIRING_LOCK_S) : fail('pairing_code_invalid');
    }
    state = { ...state, pairing: { ...pairing, wrong: 0 } };
    return key;
  }

  /** approverList is the list with its version, as the server answers it. */
  function approverList(): ApproverList {
    return { ...state.approvers, version: state.approversVersion.toString(16).padStart(32, '0') };
  }

  /** The first change wins: one based on an older version is a conflict. */
  function checkVersion(base: string): void {
    if (!/^[0-9a-f]{32}$/.test(base)) fail('invalid_input', '/base_version');
    if (base !== approverList().version) fail('conflict');
  }

  function closeApproval(id: string, outcome: ApprovalHistoryEntry['outcome'], byName: string | null, via?: 'push' | 'ui'): ApprovalHistoryEntry | null {
    const request = state.approvals.open.find((r) => r.id === id);
    if (!request) return null;
    // The mock writes no audit entry here; the seq only has to be unique in the history.
    const last = Math.max(state.audit.at(-1)?.seq ?? 0, ...state.approvals.history.map((h) => h.seq));
    const entry: ApprovalHistoryEntry = {
      seq: last + 1,
      agent: request.agent,
      entity_id: request.entity_id,
      device_name: request.device_name,
      action: request.action,
      outcome,
      by_name: byName,
      created_at: request.created_at,
      answered_at: now().toISOString(),
      ...(via ? { via } : {}),
    };
    state = { ...state, approvals: { open: state.approvals.open.filter((r) => r.id !== id), history: [entry, ...state.approvals.history] } };
    emit({ type: 'approval.closed', id, entry });
    return entry;
  }

  const control: MockControls = {
    emit,
    openApproval(request) {
      state = { ...state, approvals: { ...state.approvals, open: [...state.approvals.open, request] } };
      emit({ type: 'approval.opened', request });
    },
    closeApproval,
    renameDevice(from, to) {
      const devices = state.devices.devices.map((d) => (d.entity_id === from ? { ...d, entity_id: to } : d));
      // As the server: rules on the former ID keep applying until a human resolves the rename.
      const { [from]: earlier = [], ...renames } = state.renames;
      state = { ...state, devices: { ...state.devices, devices }, renames: { ...renames, [to]: [from, ...earlier] } };
      log('directory.changed', { actor: { kind: 'system', id: 'directory' }, directory: { change: 'renamed', entity_id: to, previous_entity_id: from } });
      emit({ type: 'devices.changed' });
    },
    setDirectoryStore(since, overflow) {
      setSystem({ ...state.system, directory: { ...state.system.directory, store_failing_since: since, overflow } });
    },
    setRenamesLastHour(count) {
      setSystem({ ...state.system, directory: { ...state.system.directory, renames_last_hour: count } });
    },
    setClockBehind(behind) {
      setSystem({ ...state.system, clock_behind: behind });
    },
    setHaConnected(connected) {
      setSystem({ ...state.system, ha: { ...state.system.ha, connected, since: now().toISOString() } });
    },
    breakChain(seq) {
      setSystem({ ...state.system, chain: { valid: false, broken_at_seq: seq, checked_at: now().toISOString() } });
    },
    async setEmergencyStop(active) {
      await api.setEmergencyStop(active);
    },
  };

  /** Active mandates whose rules name one of formers, with those rules. */
  function affectedBy(formers: readonly string[]): { id: string; rules: Rule[] }[] {
    return Object.keys(state.mandates).flatMap((id) => {
      const m = state.mandates[id];
      if (!m || m.status !== 'active') return [];
      const rules = detail(id).document.rules.filter((r) => r.resource.entity_id !== undefined && formers.includes(r.resource.entity_id));
      return rules.length > 0 ? [{ id, rules }] : [];
    });
  }

  const sameIds = (a: readonly string[], b: readonly string[]) => [...a].sort().join('\n') === [...b].sort().join('\n');

  function resolveRename(entityId: string): void {
    const { [entityId]: _resolved, ...renames } = state.renames;
    void _resolved;
    state = { ...state, renames };
    emit({ type: 'devices.changed' });
  }

  let signedOut = false;
  const api: ApiClient = {
    async session() {
      if (signedOut) fail('unauthenticated');
      return copy({ ...state.session, sign_out: options.direct ?? false });
    },
    async signOut() {
      signedOut = true;
      for (const l of listeners) l.onState('signed_out');
      listeners.clear();
    },
    async setLanguage(language) {
      state = { ...state, session: { ...state.session, language } };
      return copy(state.session);
    },
    async system() {
      return copy({ ...state.system, server_time: now().toISOString() });
    },

    async agents() {
      return copy(state.agents.map(present));
    },
    async revokeAgent(clientId) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.mandate) setMandateStatus(agent.mandate.id, 'revoked');
      const revoked = { status: 'revoked' as const, revoked_at: now().toISOString(), revoked_by_name: user().name };
      state = { ...state, agents: state.agents.map((a) => (a.client_id === clientId ? { ...a, ...revoked } : a)) };
      for (const r of state.approvals.open.filter((x) => x.agent.client_id === clientId)) closeApproval(r.id, 'revoked', null);
      log('agent.revoked', { agent: { client_id: agent.client_id, display_name: agent.display_name } });
      emit({ type: 'agents.changed' });
      return copy(present(state.agents.find((a) => a.client_id === clientId) ?? fail('internal')));
    },
    async pairingCheck(code) {
      checkPairing(code);
      return copy(pairingCandidate);
    },
    async pairingApprove(req) {
      const name = req.display_name.trim();
      if (name.length < 1 || name.length > 80) fail('invalid_input', '/display_name');
      resolved(req.template);
      const key = checkPairing(req.code);
      if (req.pairing_id !== pairingCandidate.pairing_id) fail('conflict');
      // The template must still be the one the human saw.
      if (req.template_digest !== undefined) {
        if (!DIGEST.test(req.template_digest)) fail('invalid_input', '/template_digest');
        if (template(req.template).digest !== req.template_digest) fail('conflict');
      }
      checkCritical(req.template, req.confirm_critical);
      const clientId = `pair:${pairingCandidate.client}-${++counter}`;
      const agent: Agent = {
        client_id: clientId,
        display_name: name,
        status: 'active',
        created_at: now().toISOString(),
        created_by: user().id,
        created_by_name: user().name,
        last_active_at: null,
        oauth_client: pairingCandidate.client,
        client_verified: false,
        redirect_uris: [],
        revoked_at: null,
        revoked_by_name: null,
        requests_today: 0,
        actions_last_hour: 0,
        mandate: null,
      };
      state = { ...state, agents: [...state.agents, agent], pairing: { ...state.pairing, codes: { ...state.pairing.codes, [key]: 'expired' } } };
      log('agent.registered', { agent: { client_id: clientId, display_name: name } });
      createFromTemplate(clientId, req.template, req.mandate_name);
      emit({ type: 'agents.changed' });
      return copy(present(state.agents.find((a) => a.client_id === clientId) ?? fail('internal')));
    },
    async pairingDeny({ code, pairing_id }) {
      const key = checkPairing(code);
      if (pairing_id !== pairingCandidate.pairing_id) fail('conflict');
      state = { ...state, pairing: { ...state.pairing, codes: { ...state.pairing.codes, [key]: 'expired' } } };
    },

    async devices() {
      return copy(state.devices);
    },
    async putDeviceCritical(entityId, critical) {
      if (typeof entityId !== 'string' || entityId === '') fail('invalid_input', '/entity_id');
      if (typeof critical !== 'boolean') fail('invalid_input', '/critical');
      if (!state.devices.devices.some((d) => d.entity_id === entityId)) fail('not_found');
      // As the server: a marked device is not proposed; without the mark the proposal of the catalog applies again.
      const proposed = (id: string) => devicesFixture.devices.find((d) => d.entity_id === id)?.suggest_critical === true;
      const devices = state.devices.devices.map((d) => (d.entity_id === entityId ? { ...d, critical, suggest_critical: !critical && proposed(d.entity_id) } : d));
      state = { ...state, devices: { ...state.devices, devices } };
      log('directory.changed', { directory: { change: critical ? 'critical_marked' : 'critical_unmarked', entity_id: entityId } });
      emit({ type: 'devices.changed' });
    },

    async renames() {
      const out: Rename[] = [];
      for (const [entityId, formers] of Object.entries(state.renames)) {
        const mandates = affectedBy(formers).map(({ id, rules }) => ({
          id,
          name: state.mandates[id]?.name ?? id,
          rules: rules.map((r) => r.id),
          critical: rules.some((r) => r.allow_critical === true),
        }));
        if (mandates.length === 0) continue;
        const name = state.devices.devices.find((d) => d.entity_id === entityId)?.name ?? entityId;
        const inUse = formers.filter((f) => state.devices.devices.some((d) => d.entity_id === f));
        out.push({ entity_id: entityId, name, formers, formers_in_use: inUse, mandates });
      }
      return copy(out.sort((a, b) => a.entity_id.localeCompare(b.entity_id)));
    },
    async applyRename(entityId, seen, confirmCritical) {
      const formers = state.renames[entityId] ?? fail('not_found');
      if (!sameIds(formers, seen)) fail('conflict');
      if (!state.devices.devices.some((d) => d.entity_id === entityId)) fail('not_found');
      if (formers.some((f) => state.devices.devices.some((d) => d.entity_id === f))) fail('conflict');
      const affected = affectedBy(formers);
      if (!confirmCritical && affected.some(({ rules }) => rules.some((r) => r.allow_critical === true))) fail('critical_confirmation_required');
      for (const { id } of affected) {
        const { document: doc, summary } = detail(id);
        const rename = (r: Rule): Rule =>
          r.resource.entity_id !== undefined && formers.includes(r.resource.entity_id) ? { ...r, resource: { ...r.resource, entity_id: entityId } } : r;
        storeVersion(id, summary.digest, { ...draftOf(doc), rules: doc.rules.map(rename) }, confirmCritical);
      }
      for (const former of formers) log('directory.changed', { directory: { change: 'rename_applied', entity_id: entityId, previous_entity_id: former } });
      resolveRename(entityId);
    },
    async dismissRename(entityId, seen) {
      const formers = state.renames[entityId] ?? fail('not_found');
      if (!sameIds(formers, seen)) fail('conflict');
      for (const former of formers) log('directory.changed', { directory: { change: 'rename_dismissed', entity_id: entityId, previous_entity_id: former } });
      resolveRename(entityId);
    },

    async mandates() {
      return Object.keys(state.mandates).map((id) => detail(id).summary);
    },
    async createMandate({ client_id: clientId, template: name, name: mandateName, confirm_critical: confirm }) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.status !== 'active' || agent.mandate?.status === 'active') return fail('conflict');
      resolved(name);
      checkCritical(name, confirm);
      return detail(createFromTemplate(clientId, name, mandateName));
    },
    async mandate(id) {
      return detail(id);
    },
    async mandateVersion(id, number) {
      const v = stored(id).versions.find((x) => x.meta.number === number) ?? fail('not_found');
      return copy(v.document);
    },
    async putMandate(id, update) {
      const name = update.name.trim();
      if (name.length < 1 || name.length > 80) fail('invalid_input', '/name');
      return storeVersion(id, update.base_digest, update.draft, update.confirm_critical, name);
    },
    async applyTemplate(id, apply) {
      const name = apply.name?.trim();
      if (name !== undefined && (name.length < 1 || name.length > 80)) fail('invalid_input', '/name');
      const current = detail(id).document;
      const t = resolved(apply.template);
      const draft = { ...draftOf(current), rules: t.rules, approval: t.approval, limits: t.limits };
      const origin = { origin: 'template' as const, template: apply.template, template_digest: template(apply.template).digest };
      const after = storeVersion(id, apply.base_digest, draft, apply.confirm_critical, name, origin);
      return { ...after, result: after.summary.digest === apply.base_digest ? ('unchanged' as const) : ('updated' as const) };
    },
    async revokeMandate(id) {
      setMandateStatus(id, 'revoked');
      log('mandate.revoked', { mandate: { id, digest: detail(id).summary.digest } });
      return detail(id).summary;
    },

    async templates() {
      return copy(
        state.templates.map(({ draft: _draft, ...info }): TemplateSummary => {
          void _draft;
          return info;
        }),
      );
    },
    async template(name) {
      return copy(state.templates.find((t) => t.name === name) ?? fail('not_found'));
    },
    async putTemplate(name, update) {
      if (!TEMPLATE_NAME.test(name)) fail('invalid_input', '/name');
      if (update.base_digest !== null && !DIGEST.test(update.base_digest)) fail('invalid_input', '/base_digest');
      const existing = state.templates.find((t) => t.name === name);
      if (existing?.builtin) fail('builtin_template');
      if (name.startsWith(RESERVED_PREFIX)) fail('invalid_input', '/name');
      // Nobody overwrites a version they have not seen; a new template must not exist yet.
      if ((existing?.digest ?? null) !== update.base_digest) fail('conflict');
      validate(update.draft);
      if (needsCriticalConfirmation(existing?.draft ?? null, update.draft) && update.confirm_critical !== true) {
        return fail('critical_confirmation_required');
      }
      const t = storedTemplate({
        name,
        draft: update.draft,
        rule_count: 0,
        created_at: now().toISOString(),
        created_by: user().id,
        created_by_name: user().name,
        builtin: false,
        hidden: false,
        title: {},
        description: {},
        digest: '',
      });
      state = { ...state, templates: templateOrder([...state.templates.filter((x) => x.name !== name), t]) };
      emit({ type: 'templates.changed' });
      return copy(t);
    },
    async deleteTemplate(name) {
      const existing = state.templates.find((t) => t.name === name) ?? fail('not_found');
      if (existing.builtin) fail('builtin_template');
      state = { ...state, templates: state.templates.filter((t) => t.name !== name) };
      emit({ type: 'templates.changed' });
    },
    async setTemplateHidden(name, hidden) {
      if (typeof hidden !== 'boolean') fail('invalid_input', '/hidden');
      // Only base templates can be hidden; anything else is not found.
      if (!state.templates.some((t) => t.name === name && t.builtin)) fail('not_found');
      state = { ...state, templates: state.templates.map((t) => (t.name === name ? { ...t, hidden } : t)) };
      emit({ type: 'templates.changed' });
    },

    async settings() {
      return copy(state.defaults);
    },
    async putSettings(defaults) {
      const s = timeoutSeconds(defaults.approval_timeout);
      if (Number.isNaN(s) || s < 10 || s > 3600) fail('invalid_input', '/approval_timeout');
      const n = defaults.max_actions_per_hour;
      if (!Number.isInteger(n) || n < 1 || n > 1000) fail('invalid_input', '/max_actions_per_hour');
      state = { ...state, defaults };
      emit({ type: 'settings.changed' });
      return copy(defaults);
    },

    async approvals() {
      return copy(state.approvals);
    },
    async answerApproval(id, approve) {
      // Unknown, ended and not answerable look alike (no probing of IDs).
      const request = state.approvals.open.find((r) => r.id === id && r.can_answer) ?? fail('not_found');
      return copy(closeApproval(request.id, approve ? 'approved' : 'rejected', user().name, 'ui') ?? fail('not_found'));
    },

    async audit(query) {
      const limit = query.limit ?? 50;
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) fail('invalid_input', '/limit');
      const search = searchMatcher(query.q, state.devices);
      const matching = state.audit.filter((e) => matchesQuery(e, query) && (search?.(e) ?? true)).toReversed();
      const page = matching.filter((e) => query.before === undefined || e.seq < query.before);
      const entries = page.slice(0, limit);
      const more = page.length > limit;
      return copy({ entries, next_before: more ? (entries.at(-1)?.seq ?? null) : null, total: matching.length });
    },
    async verifyAudit() {
      const chain = { ...state.system.chain, checked_at: now().toISOString() };
      setSystem({ ...state.system, chain });
      return { ...chain, checked: state.audit.length };
    },

    async approvers() {
      return copy(approverList());
    },
    async putApprover(userId, update, baseVersion) {
      checkVersion(baseVersion);
      const { candidates, approvers } = state.approvers;
      const person = candidates.people.find((p) => p.user_id === userId) ?? fail('invalid_input', '/user_id');
      checkApprover(update, person.is_admin, candidates.devices.map((d) => d.service));
      const approver: Approver = { user_id: userId, name: person.name, ...update, reach: reachOf(update, person.is_admin) };
      const index = approvers.findIndex((a) => a.user_id === userId);
      const next = index < 0 ? [...approvers, approver] : approvers.map((a, i) => (i === index ? approver : a));
      state = { ...state, approvers: { candidates, approvers: next }, approversVersion: state.approversVersion + 1 };
      emit({ type: 'approvers.changed' });
      setSystem({ ...state.system, approvers_configured: state.approvers.approvers.length });
      return copy(approverList());
    },
    async testApprover(userId) {
      if (!state.approvers.approvers.some((a) => a.user_id === userId)) fail('not_found');
    },
    async deleteApprover(userId, baseVersion) {
      checkVersion(baseVersion);
      if (!state.approvers.approvers.some((a) => a.user_id === userId)) fail('not_found');
      const approvers = state.approvers.approvers.filter((a) => a.user_id !== userId);
      state = { ...state, approvers: { ...state.approvers, approvers }, approversVersion: state.approversVersion + 1 };
      emit({ type: 'approvers.changed' });
      setSystem({ ...state.system, approvers_configured: approvers.length });
    },

    async setEmergencyStop(active) {
      if (active !== state.system.emergency_stop.active) {
        const stop = active ? { active, since: now().toISOString(), by_name: user().name } : { active, since: null, by_name: null };
        if (active) for (const r of state.approvals.open) closeApproval(r.id, 'emergency_stop', null);
        setSystem({ ...state.system, emergency_stop: stop });
        log(active ? 'emergency_stop.activated' : 'emergency_stop.released');
      }
      return copy(state.system.emergency_stop);
    },

    events(handlers): EventsConnection {
      listeners.add(handlers);
      handlers.onState('connecting');
      handlers.onState(options.eventsState ?? 'open');
      return {
        reconnect: () => handlers.onState(options.eventsState ?? 'open'),
        close: () => listeners.delete(handlers),
      };
    },
  };

  return { ...withFailures(api, options.failures ?? {}), control };
}

/** withFailures wraps the methods named in failures so they reject with that code. */
function withFailures(api: ApiClient, failures: NonNullable<MockOptions['failures']>): ApiClient {
  const wrapped = { ...api };
  for (const [name, code] of Object.entries(failures)) {
    if (code === undefined) continue;
    (wrapped as Record<string, unknown>)[name] = async () => fail(code);
  }
  return wrapped;
}
