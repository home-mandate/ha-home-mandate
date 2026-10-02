// SPDX-License-Identifier: AGPL-3.0-or-later

// In-memory ApiClient that behaves like internal/api on the fixtures: for component
// tests and for building the UI before the backend exists. State is replaced, never
// changed in place, and every answer is a copy.

import { ApiError, type ApiClient } from './client.ts';
import {
  agentsFixture,
  approversFixture,
  auditFixture,
  devicesFixture,
  sessionFixture,
  templatesFixture,
  voiceAssistantMandate,
} from './fixtures.ts';
import { evaluateDraft } from './mock-preview.ts';
import { needsCriticalConfirmation } from './vocabulary.ts';
import type {
  Agent,
  ApiErrorCode,
  ApproverList,
  AuditEntry,
  AuditEvent,
  MandateDetail,
  MandateDocument,
  MandateDraft,
  MandateVersion,
  Session,
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
  too_large: 413,
  rate_limited: 429,
  unavailable: 0,
  internal: 500,
};

const fail = (code: ApiErrorCode, field?: string): never => {
  throw new ApiError(code, STATUS[code], field);
};

interface StoredMandate {
  status: 'active' | 'revoked';
  /** Newest first. */
  versions: { meta: MandateVersion; document: MandateDocument }[];
}

interface State {
  session: Session;
  agents: Agent[];
  mandates: Record<string, StoredMandate>;
  templates: Template[];
  audit: AuditEntry[];
  approvers: ApproverList;
  emergencyStop: boolean;
}

export interface MockOptions {
  /** Makes a method fail with this code, e.g. { agents: 'unavailable' }. */
  failures?: Partial<Record<keyof ApiClient, ApiErrorCode>>;
  now?: () => Date;
}

function initialMandates(): Record<string, StoredMandate> {
  const docs: MandateDocument[] = agentsFixture
    .filter((a) => a.mandate !== null)
    .map((a) => ({
      ...voiceAssistantMandate,
      id: a.mandate?.id ?? '',
      agent: { client_id: a.client_id, display_name: a.display_name },
    }));
  return Object.fromEntries(
    docs.map((d, i) => [
      d.id,
      { status: 'active', versions: [{ meta: { digest: `sha256:fixture-${i}`, created_at: d.created_at, created_by: d.created_by }, document: d }] },
    ]),
  );
}

/** validateDraft mirrors the schema checks the UI can trigger; the server uses evaluator.Parse. */
function validateDraft(draft: MandateDraft): void {
  const { max_actions_per_hour: limit } = draft.limits;
  if (!Number.isInteger(limit) || limit < 1 || limit > 1000) fail('invalid_mandate', '/draft/limits/max_actions_per_hour');
  const seen = new Set<string>();
  draft.rules.forEach((rule, i) => {
    const at = `/draft/rules/${i}`;
    if (!/^[A-Za-z0-9_-]{1,64}$/.test(rule.id) || seen.has(rule.id)) fail('invalid_mandate', `${at}/id`);
    seen.add(rule.id);
    if (rule.actions.length === 0) fail('invalid_mandate', `${at}/actions`);
    if (rule.allow_critical && rule.decision !== 'allow') fail('invalid_mandate', `${at}/allow_critical`);
    if (rule.approval && rule.decision !== 'ask') fail('invalid_mandate', `${at}/approval`);
  });
}

const draftOf = (d: MandateDocument): MandateDraft => ({
  rules: d.rules,
  approval: d.approval,
  limits: d.limits,
  valid_from: d.valid_from,
  ...(d.expires === undefined ? {} : { expires: d.expires }),
});

export function createMockClient(options: MockOptions = {}): ApiClient {
  const now = options.now ?? (() => new Date());
  let state: State = {
    session: sessionFixture,
    agents: agentsFixture,
    mandates: initialMandates(),
    templates: templatesFixture,
    audit: auditFixture,
    approvers: approversFixture,
    emergencyStop: false,
  };
  let digests = 0;

  const copy = <T>(value: T): T => structuredClone(value);

  function log(event: AuditEvent, extra: Partial<AuditEntry> = {}): void {
    const seq = (state.audit.at(-1)?.seq ?? 0) + 1;
    const entry: AuditEntry = {
      id: `mock-${seq}`,
      seq,
      recorded_at: now().toISOString(),
      event,
      actor: { kind: 'user', id: state.session.user.id },
      ...extra,
    };
    state = { ...state, audit: [...state.audit, entry] };
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
        client_id: doc.agent.client_id,
        agent_display_name: doc.agent.display_name,
        status: m.status,
        digest: current.meta.digest,
        max_actions_per_hour: doc.limits.max_actions_per_hour,
        updated_at: current.meta.created_at,
      },
      document: doc,
      versions: m.versions.map((v) => v.meta),
    });
  }

  function setMandateStatus(id: string, status: 'active' | 'revoked'): void {
    state = {
      ...state,
      mandates: { ...state.mandates, [id]: { ...stored(id), status } },
      agents: state.agents.map((a) => (a.mandate?.id === id ? { ...a, mandate: { id, status } } : a)),
    };
  }

  const api: ApiClient = {
    async session() {
      return copy(state.session);
    },
    async setLanguage(language) {
      state = { ...state, session: { ...state.session, language } };
      return copy(state.session);
    },

    async agents() {
      return copy(state.agents);
    },
    async revokeAgent(clientId) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.mandate) setMandateStatus(agent.mandate.id, 'revoked');
      state = {
        ...state,
        agents: state.agents.map((a) => (a.client_id === clientId ? { ...a, status: 'revoked' as const } : a)),
      };
      log('agent.revoked', { agent: { client_id: agent.client_id, display_name: agent.display_name } });
      return copy(state.agents.find((a) => a.client_id === clientId) ?? fail('internal'));
    },
    async pairing() {
      return { url: 'https://home.example:8765/pair' };
    },

    async devices() {
      return copy(devicesFixture);
    },

    async mandates() {
      return Object.keys(state.mandates).map((id) => detail(id).summary);
    },
    async createMandate({ client_id: clientId, template: name }) {
      const agent = state.agents.find((a) => a.client_id === clientId) ?? fail('not_found');
      if (agent.status !== 'active' || agent.mandate?.status === 'active') return fail('conflict');
      const template = state.templates.find((t) => t.name === name) ?? fail('invalid_input', '/template');
      const createdAt = now().toISOString();
      const id = `mandate-mock-${++digests}`;
      const document: MandateDocument = {
        ...voiceAssistantMandate,
        ...template.draft,
        id,
        agent: { client_id: agent.client_id, display_name: agent.display_name },
        created_by: state.session.user.id,
        created_at: createdAt,
      };
      const meta = { digest: `sha256:mock-${digests}`, created_at: createdAt, created_by: state.session.user.id };
      state = {
        ...state,
        mandates: { ...state.mandates, [id]: { status: 'active', versions: [{ meta, document }] } },
        agents: state.agents.map((a) => (a.client_id === clientId ? { ...a, mandate: { id, status: 'active' as const } } : a)),
      };
      log('mandate.created', { agent: document.agent, mandate: { id, digest: meta.digest } });
      return detail(id);
    },
    async mandate(id) {
      return detail(id);
    },
    async mandateVersion(id, digest) {
      const v = stored(id).versions.find((x) => x.meta.digest === digest) ?? fail('not_found');
      return copy(v.document);
    },
    async putMandate(id, update) {
      const m = stored(id);
      const [current] = m.versions;
      if (!current || m.status !== 'active' || current.meta.digest !== update.base_digest) return fail('conflict');
      validateDraft(update.draft);
      if (needsCriticalConfirmation(draftOf(current.document), update.draft) && update.confirm_critical !== true) {
        return fail('critical_confirmation_required');
      }
      const createdAt = now().toISOString();
      const document: MandateDocument = {
        ...current.document,
        ...update.draft,
        created_by: state.session.user.id,
        created_at: createdAt,
      };
      const meta = { digest: `sha256:mock-${++digests}`, created_at: createdAt, created_by: state.session.user.id };
      state = { ...state, mandates: { ...state.mandates, [id]: { ...m, versions: [{ meta, document }, ...m.versions] } } };
      log('mandate.updated', {
        agent: document.agent,
        mandate: { id, digest: meta.digest, previous_digest: current.meta.digest },
      });
      return detail(id);
    },
    async revokeMandate(id) {
      setMandateStatus(id, 'revoked');
      log('mandate.revoked', { mandate: { id, digest: detail(id).summary.digest } });
      return detail(id).summary;
    },
    async preview(request) {
      const base = request.mandate_id ? draftOf(detail(request.mandate_id).document) : undefined;
      const at = request.at ?? now().toISOString();
      if (Number.isNaN(Date.parse(at))) fail('invalid_input', '/at');
      return evaluateDraft(request.draft, devicesFixture.devices, at, state.session.household.time_zone, base);
    },

    async templates() {
      return state.templates.map((t) => ({ name: t.name, created_at: '2026-10-01T08:00:00Z', created_by: 'u-admin' }));
    },
    async template(name) {
      return copy(state.templates.find((t) => t.name === name) ?? fail('not_found'));
    },
    async putTemplate(name, update) {
      validateDraft(update.draft);
      const existing = state.templates.find((t) => t.name === name);
      if (needsCriticalConfirmation(existing?.draft ?? null, update.draft) && update.confirm_critical !== true) {
        return fail('critical_confirmation_required');
      }
      const template: Template = { name, draft: update.draft };
      state = { ...state, templates: [...state.templates.filter((t) => t.name !== name), template] };
      return copy(template);
    },
    async deleteTemplate(name) {
      if (!state.templates.some((t) => t.name === name)) fail('not_found');
      state = { ...state, templates: state.templates.filter((t) => t.name !== name) };
    },

    async audit(query) {
      const limit = query.limit ?? 50;
      if (!Number.isInteger(limit) || limit < 1 || limit > 100) fail('invalid_input', '/limit');
      const matching = state.audit
        .filter((e) => query.before === undefined || e.seq < query.before)
        .filter((e) => query.agent === undefined || e.agent?.client_id === query.agent)
        .filter((e) => query.entity_id === undefined || e.request?.resource.entity_id === query.entity_id)
        .filter((e) => query.event === undefined || e.event === query.event)
        .filter((e) => query.decision === undefined || e.evaluation?.decision === query.decision)
        .toReversed();
      const entries = matching.slice(0, limit);
      const more = matching.length > limit;
      return copy({ entries, next_before: more ? (entries.at(-1)?.seq ?? null) : null });
    },
    async verifyAudit() {
      return { valid: true, broken_at_seq: null, checked: state.audit.length };
    },

    async approvers() {
      return copy(state.approvers);
    },
    async putApprover(userId, update) {
      const { candidates, approvers } = state.approvers;
      const person = candidates.people.find((p) => p.user_id === userId) ?? fail('invalid_input', '/user_id');
      if (!candidates.notify_services.includes(update.notify_service)) fail('invalid_input', '/notify_service');
      const approver = { user_id: userId, name: person.name, ...update };
      const others = approvers.filter((a) => a.user_id !== userId);
      state = { ...state, approvers: { candidates, approvers: [...others, approver] } };
      return copy(state.approvers);
    },
    async testApprover(userId) {
      if (!state.approvers.approvers.some((a) => a.user_id === userId)) fail('not_found');
    },
    async deleteApprover(userId) {
      const { approvers } = state.approvers;
      state = { ...state, approvers: { ...state.approvers, approvers: approvers.filter((a) => a.user_id !== userId) } };
    },

    async emergencyStop() {
      return { active: state.emergencyStop };
    },
    async setEmergencyStop(active) {
      if (active !== state.emergencyStop) {
        state = { ...state, emergencyStop: active };
        log(active ? 'emergency_stop.activated' : 'emergency_stop.released');
      }
      return { active: state.emergencyStop };
    },
  };

  return withFailures(api, options.failures ?? {});
}

/** withFailures wraps the methods named in failures so they reject with that code. */
function withFailures(api: ApiClient, failures: MockOptions['failures'] & object): ApiClient {
  const wrapped = { ...api };
  for (const [name, code] of Object.entries(failures)) {
    if (code === undefined) continue;
    (wrapped as Record<string, unknown>)[name] = async () => fail(code);
  }
  return wrapped;
}
