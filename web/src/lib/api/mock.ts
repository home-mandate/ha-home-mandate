// SPDX-License-Identifier: AGPL-3.0-or-later

// In-memory ApiClient that behaves like internal/api on the fixtures: for component
// tests, Playwright against the static build and building the UI before the backend.
// State is replaced, never changed in place, and every answer is a copy. `control`
// simulates what only the server can cause: approval requests, HA going down, a broken
// audit chain.

import { checkDraft, timeoutSeconds } from '../engine/check.ts';
import { canonical, needsCriticalConfirmation } from '../engine/vocabulary.ts';
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
  USERS,
  voiceAssistantMandate,
} from './fixtures.ts';
import type {
  Agent,
  ApiErrorCode,
  ApprovalHistoryEntry,
  ApprovalRequest,
  Approvals,
  ApproverList,
  AuditEntry,
  AuditEvent,
  AuditQuery,
  Defaults,
  MandateDetail,
  MandateDocument,
  MandateDraft,
  MandateVersion,
  PairingCandidate,
  ServerEvent,
  Session,
  SystemStatus,
  Template,
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
const pairingCandidate: PairingCandidate = {
  claimed_name: 'Küchen-Tablet',
  client: 'kitchen-tablet',
  client_verified: false,
  requested_at: '2026-10-02T17:40:00Z',
  expires_at: '2026-10-02T17:50:00Z',
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
  agents: Agent[];
  mandates: Record<string, StoredMandate>;
  templates: Template[];
  audit: AuditEntry[];
  approvals: Approvals;
  approvers: ApproverList;
  pairing: { codes: Record<string, 'open' | 'expired'>; wrong: number; lockedUntil: number };
}

export interface MockControls {
  emit(event: ServerEvent): void;
  openApproval(request: ApprovalRequest): void;
  closeApproval(id: string, outcome: ApprovalHistoryEntry['outcome'], byName: string | null): void;
  setHaConnected(connected: boolean): void;
  breakChain(seq: number): void;
}

export type MockClient = ApiClient & { control: MockControls };

export interface MockOptions {
  /** Makes a method fail with this code, e.g. { agents: 'unavailable' }. */
  failures?: Partial<Record<keyof ApiClient, ApiErrorCode>>;
  /** State the event stream reaches after connecting; default "open". */
  eventsState?: EventsState;
  now?: () => Date;
}

const normalizeCode = (code: string) => code.toUpperCase().replace(/[\s-]/g, '');
const nameOf = (userId: string) => USERS[userId] ?? null;

function initialMandates(): Record<string, StoredMandate> {
  return Object.fromEntries(
    agentsFixture
      .filter((a) => a.mandate !== null)
      .map((a, i): [string, StoredMandate] => {
        const id = a.mandate?.id ?? '';
        const document: MandateDocument = { ...voiceAssistantMandate, id, agent: { client_id: a.client_id, display_name: a.display_name } };
        const meta = { number: 1, digest: `sha256:fixture-${i}`, created_at: document.created_at, created_by: 'u-admin', created_by_name: 'Markus' };
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

/** validate rejects a draft the server would reject, with the pointer under /draft. */
function validate(draft: MandateDraft): void {
  const [first] = checkDraft(draft);
  if (first) fail('invalid_mandate', `/draft${first.field}`);
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
  let state: State = {
    session: sessionFixture,
    system: systemFixture,
    defaults: defaultsFixture,
    agents: agentsFixture,
    mandates: initialMandates(),
    templates: templatesFixture,
    audit: auditFixture,
    approvals: { open: approvalsOpenFixture, history: approvalsHistoryFixture },
    approvers: approversFixture,
    pairing: { codes: { [normalizeCode(MOCK_PAIRING_CODE)]: 'open', [normalizeCode(MOCK_EXPIRED_CODE)]: 'expired' }, wrong: 0, lockedUntil: 0 },
  };
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
      },
      document: doc,
      versions: m.versions.map((v) => v.meta),
    });
  }

  function putStored(id: string, m: StoredMandate): void {
    state = {
      ...state,
      mandates: { ...state.mandates, [id]: m },
      agents: state.agents.map((a) => (a.mandate?.id === id ? { ...a, mandate: { id, name: m.name, status: m.status } } : a)),
    };
    emit({ type: 'mandates.changed', id });
  }

  /** storeVersion adds a version after the conflict and U9 checks of the server. */
  function storeVersion(id: string, baseDigest: string, draft: MandateDraft, confirm: boolean | undefined, name?: string) {
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
    const meta = { number: m.versions.length + 1, digest: `sha256:mock-${++counter}`, created_at: createdAt, created_by: user().id, created_by_name: user().name };
    putStored(id, { ...m, name: name ?? m.name, versions: [{ meta, document }, ...m.versions] });
    log('mandate.updated', { agent: document.agent, mandate: { id, digest: meta.digest, previous_digest: current.meta.digest } });
    return detail(id);
  }

  function setMandateStatus(id: string, status: 'active' | 'revoked'): void {
    putStored(id, { ...stored(id), status });
  }

  function template(name: string, field = '/template'): Template {
    return state.templates.find((t) => t.name === name) ?? fail('invalid_input', field);
  }

  function createFromTemplate(clientId: string, name: string, mandateName?: string): string {
    const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
    const t = template(name);
    const createdAt = now().toISOString();
    const id = `mandate-mock-${++counter}`;
    const document: MandateDocument = {
      ...voiceAssistantMandate,
      ...t.draft,
      id,
      agent: { client_id: agent.client_id, display_name: agent.display_name },
      created_by: user().id,
      created_at: createdAt,
    };
    const meta = { number: 1, digest: `sha256:mock-${counter}`, created_at: createdAt, created_by: user().id, created_by_name: user().name };
    state = { ...state, agents: state.agents.map((a) => (a.client_id === clientId ? { ...a, mandate: { id, name: '', status: 'active' as const } } : a)) };
    putStored(id, { name: mandateName ?? name, status: 'active', versions: [{ meta, document }] });
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

  function closeApproval(id: string, outcome: ApprovalHistoryEntry['outcome'], byName: string | null): void {
    const request = state.approvals.open.find((r) => r.id === id);
    if (!request) return;
    const entry: ApprovalHistoryEntry = {
      seq: (state.audit.at(-1)?.seq ?? 0) + 1,
      agent: request.agent,
      entity_id: request.entity_id,
      device_name: request.device_name,
      action: request.action,
      outcome,
      by_name: byName,
      created_at: request.created_at,
      answered_at: now().toISOString(),
    };
    state = { ...state, approvals: { open: state.approvals.open.filter((r) => r.id !== id), history: [entry, ...state.approvals.history] } };
    emit({ type: 'approval.closed', id, entry });
  }

  const control: MockControls = {
    emit,
    openApproval(request) {
      state = { ...state, approvals: { ...state.approvals, open: [...state.approvals.open, request] } };
      emit({ type: 'approval.opened', request });
    },
    closeApproval,
    setHaConnected(connected) {
      setSystem({ ...state.system, ha: { ...state.system.ha, connected, since: now().toISOString() } });
    },
    breakChain(seq) {
      setSystem({ ...state.system, chain: { valid: false, broken_at_seq: seq, checked_at: now().toISOString() } });
    },
  };

  const api: ApiClient = {
    async session() {
      return copy(state.session);
    },
    async setLanguage(language) {
      state = { ...state, session: { ...state.session, language } };
      return copy(state.session);
    },
    async system() {
      return copy({ ...state.system, server_time: now().toISOString() });
    },

    async agents() {
      return copy(state.agents);
    },
    async revokeAgent(clientId) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.mandate) setMandateStatus(agent.mandate.id, 'revoked');
      state = { ...state, agents: state.agents.map((a) => (a.client_id === clientId ? { ...a, status: 'revoked' as const } : a)) };
      for (const r of state.approvals.open.filter((x) => x.agent.client_id === clientId)) closeApproval(r.id, 'revoked', null);
      log('agent.revoked', { agent: { client_id: agent.client_id, display_name: agent.display_name } });
      emit({ type: 'agents.changed' });
      return copy(state.agents.find((a) => a.client_id === clientId) ?? fail('internal'));
    },
    async pairingCheck(code) {
      checkPairing(code);
      return copy(pairingCandidate);
    },
    async pairingApprove(req) {
      const name = req.display_name.trim();
      if (name.length < 1 || name.length > 80) fail('invalid_input', '/display_name');
      template(req.template);
      const key = checkPairing(req.code);
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
        mandate: null,
      };
      state = { ...state, agents: [...state.agents, agent], pairing: { ...state.pairing, codes: { ...state.pairing.codes, [key]: 'expired' } } };
      log('agent.registered', { agent: { client_id: clientId, display_name: name } });
      createFromTemplate(clientId, req.template, req.mandate_name);
      emit({ type: 'agents.changed' });
    },
    async pairingDeny(code) {
      const key = checkPairing(code);
      state = { ...state, pairing: { ...state.pairing, codes: { ...state.pairing.codes, [key]: 'expired' } } };
    },

    async devices() {
      return copy(devicesFixture);
    },

    async mandates() {
      return Object.keys(state.mandates).map((id) => detail(id).summary);
    },
    async createMandate({ client_id: clientId, template: name, name: mandateName }) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.status !== 'active' || agent.mandate?.status === 'active') return fail('conflict');
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
      const current = detail(id).document;
      const t = template(apply.template);
      const draft = { ...draftOf(current), rules: t.draft.rules, approval: t.draft.approval, limits: t.draft.limits };
      return storeVersion(id, apply.base_digest, draft, apply.confirm_critical);
    },
    async revokeMandate(id) {
      setMandateStatus(id, 'revoked');
      log('mandate.revoked', { mandate: { id, digest: detail(id).summary.digest } });
      return detail(id).summary;
    },

    async templates() {
      return state.templates.map((t) => ({
        name: t.name,
        rule_count: t.draft.rules.length,
        created_at: '2026-10-01T08:00:00Z',
        created_by: 'u-admin',
        created_by_name: nameOf('u-admin'),
      }));
    },
    async template(name) {
      return copy(state.templates.find((t) => t.name === name) ?? fail('not_found'));
    },
    async putTemplate(name, update) {
      validate(update.draft);
      const existing = state.templates.find((t) => t.name === name);
      if (needsCriticalConfirmation(existing?.draft ?? null, update.draft) && update.confirm_critical !== true) {
        return fail('critical_confirmation_required');
      }
      const t: Template = { name, draft: update.draft };
      state = { ...state, templates: [...state.templates.filter((x) => x.name !== name), t] };
      emit({ type: 'templates.changed' });
      return copy(t);
    },
    async deleteTemplate(name) {
      if (!state.templates.some((t) => t.name === name)) fail('not_found');
      state = { ...state, templates: state.templates.filter((t) => t.name !== name) };
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

    async audit(query) {
      const limit = query.limit ?? 50;
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) fail('invalid_input', '/limit');
      const matching = state.audit.filter((e) => matchesQuery(e, query)).toReversed();
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
      return copy(state.approvers);
    },
    async putApprover(userId, update) {
      const { candidates, approvers } = state.approvers;
      const person = candidates.people.find((p) => p.user_id === userId) ?? fail('invalid_input', '/user_id');
      if (!candidates.notify_services.includes(update.notify_service)) fail('invalid_input', '/notify_service');
      const others = approvers.filter((a) => a.user_id !== userId);
      state = { ...state, approvers: { candidates, approvers: [...others, { user_id: userId, name: person.name, ...update }] } };
      emit({ type: 'approvers.changed' });
      setSystem({ ...state.system, approvers_configured: state.approvers.approvers.length });
      return copy(state.approvers);
    },
    async testApprover(userId) {
      if (!state.approvers.approvers.some((a) => a.user_id === userId)) fail('not_found');
    },
    async deleteApprover(userId) {
      const approvers = state.approvers.approvers.filter((a) => a.user_id !== userId);
      state = { ...state, approvers: { ...state.approvers, approvers } };
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
