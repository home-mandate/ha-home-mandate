// SPDX-License-Identifier: AGPL-3.0-or-later

// Client for the JSON API (contract in ./types.ts). ApiClient is the seam between the
// UI and the server: createHttpClient talks to internal/api, createMockClient (./mock.ts)
// serves fixtures for component tests and for building the UI before the backend.

import { connectEvents, eventsUrl, type EventSocket, type EventsConnection, type EventsState } from './events.ts';
import type {
  Agent,
  ApiErrorBody,
  ApiErrorCode,
  ApplyTemplate,
  ApprovalAnswer,
  ApprovalHistoryEntry,
  Approvals,
  ApproverList,
  ApproverUpdate,
  AuditPage,
  AuditQuery,
  AuditVerification,
  Defaults,
  DeviceCatalog,
  EmergencyStop,
  Language,
  MandateCreate,
  MandateDetail,
  MandateDocument,
  MandateSummary,
  MandateUpdate,
  PairingApprove,
  PairingCandidate,
  PairingDecision,
  ServerEvent,
  Session,
  SystemStatus,
  Template,
  TemplateSummary,
  TemplateUpdate,
} from './types.ts';

export interface EventHandlers {
  onEvent: (event: ServerEvent) => void;
  onState: (state: EventsState) => void;
}

export interface ApiClient {
  /** Loads the session; must be called first, it carries the CSRF token for writes. */
  session(): Promise<Session>;
  setLanguage(language: Language | null): Promise<Session>;
  system(): Promise<SystemStatus>;

  agents(): Promise<Agent[]>;
  revokeAgent(clientId: string): Promise<Agent>;
  pairingCheck(code: string): Promise<PairingCandidate>;
  /** Admits the agent; answers with it (and its new mandate). */
  pairingApprove(approve: PairingApprove): Promise<Agent>;
  pairingDeny(decision: PairingDecision): Promise<void>;

  devices(): Promise<DeviceCatalog>;

  mandates(): Promise<MandateSummary[]>;
  createMandate(create: MandateCreate): Promise<MandateDetail>;
  mandate(id: string): Promise<MandateDetail>;
  /** The document of one version, by its number within the mandate. */
  mandateVersion(id: string, number: number): Promise<MandateDocument>;
  putMandate(id: string, update: MandateUpdate): Promise<MandateDetail>;
  applyTemplate(id: string, apply: ApplyTemplate): Promise<MandateDetail>;
  revokeMandate(id: string): Promise<MandateSummary>;

  templates(): Promise<TemplateSummary[]>;
  template(name: string): Promise<Template>;
  putTemplate(name: string, update: TemplateUpdate): Promise<Template>;
  deleteTemplate(name: string): Promise<void>;

  settings(): Promise<Defaults>;
  putSettings(defaults: Defaults): Promise<Defaults>;

  approvals(): Promise<Approvals>;
  answerApproval(id: string, approve: boolean): Promise<ApprovalHistoryEntry>;

  audit(query: AuditQuery): Promise<AuditPage>;
  verifyAudit(): Promise<AuditVerification>;

  approvers(): Promise<ApproverList>;
  /** baseVersion: the version of the approvers the change is based on (ApproverList.version). */
  putApprover(userId: string, update: ApproverUpdate, baseVersion: string): Promise<ApproverList>;
  testApprover(userId: string): Promise<void>;
  deleteApprover(userId: string, baseVersion: string): Promise<void>;

  setEmergencyStop(active: boolean): Promise<EmergencyStop>;

  /** Opens the live event stream; it reconnects on its own until closed. */
  events(handlers: EventHandlers): EventsConnection;
}

const ERROR_CODES: ReadonlySet<string> = new Set<ApiErrorCode>([
  'unauthenticated',
  'forbidden',
  'csrf_invalid',
  'not_found',
  'conflict',
  'invalid_input',
  'invalid_mandate',
  'critical_confirmation_required',
  'pairing_code_invalid',
  'pairing_code_expired',
  'pairing_locked',
  'too_large',
  'rate_limited',
  'unavailable',
  'internal',
]);

/** Code for an answer without a usable body, e.g. a page from a proxy on the way. */
const BY_STATUS: Readonly<Record<number, ApiErrorCode>> = {
  400: 'invalid_input',
  401: 'unauthenticated',
  403: 'forbidden',
  404: 'not_found',
  409: 'conflict',
  413: 'too_large',
  422: 'invalid_input',
  429: 'rate_limited',
  502: 'unavailable',
  503: 'unavailable',
  504: 'unavailable',
};

const JSON_POINTER = /^(\/[A-Za-z0-9_~.-]*)+$/;
const MAX_RETRY_AFTER_S = 24 * 60 * 60;

/** Longer than the server's own limits; the emergency stop must never hang silently. */
const DEFAULT_TIMEOUT_MS = 15_000;

/** ApiError carries only a known code (and a JSON pointer); the UI maps it to a message. */
export class ApiError extends Error {
  readonly code: ApiErrorCode;
  /** HTTP status; 0 when the server was not reached or did not answer in time. */
  readonly status: number;
  readonly field: string | undefined;
  /** Seconds until a lock or rate limit ends. */
  readonly retryAfter: number | undefined;

  constructor(code: ApiErrorCode, status: number, field?: string, retryAfter?: number) {
    super(`api: ${code}`);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.field = field;
    this.retryAfter = retryAfter;
  }
}

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;
type Method = 'GET' | 'POST' | 'PUT' | 'DELETE';

export interface HttpClientOptions {
  fetch?: Fetch;
  /** URL the "api/…" paths resolve against; the page itself under its Ingress prefix. */
  base?: string;
  timeoutMs?: number;
  /** WebSocket factory for the event stream; tests pass a fake. */
  socket?: (url: string) => EventSocket;
}

/** segment encodes one path segment and refuses values that could change the path. */
function segment(value: string): string {
  if (value === '' || value === '.' || value === '..') throw new ApiError('invalid_input', 0);
  return encodeURIComponent(value);
}

/** versionSegment accepts a version number (from 1) and nothing else. */
function versionSegment(number: number): string {
  if (!Number.isSafeInteger(number) || number < 1) throw new ApiError('invalid_input', 0);
  return String(number);
}

function auditQuery(q: AuditQuery): string {
  const params = new URLSearchParams();
  if (q.before !== undefined) params.set('before', String(q.before));
  if (q.limit !== undefined) params.set('limit', String(q.limit));
  if (q.since !== undefined) params.set('since', q.since);
  if (q.until !== undefined) params.set('until', q.until);
  if (q.agent !== undefined) params.set('agent', q.agent);
  if (q.device !== undefined) params.set('device', q.device);
  if (q.q !== undefined) params.set('q', q.q);
  if (q.group !== undefined) params.set('group', q.group);
  if (q.event !== undefined) params.set('event', q.event);
  for (const d of q.decisions ?? []) params.append('decision', d);
  const s = params.toString();
  return s ? `?${s}` : '';
}

/** retryAfter accepts whole seconds up to a day, from the body or a proxy's header. */
function retryAfterOf(body: unknown, header: string | null): number | undefined {
  const value = body ?? (header !== null && /^\d{1,6}$/.test(header) ? Number(header) : undefined);
  return Number.isInteger(value) && (value as number) > 0 && (value as number) <= MAX_RETRY_AFTER_S ? (value as number) : undefined;
}

async function errorFrom(res: Response, path: string): Promise<ApiError> {
  let body: Partial<ApiErrorBody> = {};
  try {
    body = (await res.json()) as Partial<ApiErrorBody>;
  } catch {
    // Not JSON: a proxy page or a crash; never show it.
  }
  // A bare 410 only means an expired code where codes are involved, not from a proxy elsewhere.
  const fallback = res.status === 410 && path.startsWith('pairing/') ? 'pairing_code_expired' : (BY_STATUS[res.status] ?? 'internal');
  const code = typeof body.code === 'string' && ERROR_CODES.has(body.code) ? body.code : fallback;
  const field = typeof body.field === 'string' && JSON_POINTER.test(body.field) ? body.field : undefined;
  return new ApiError(code, res.status, field, retryAfterOf(body.retry_after, res.headers.get('Retry-After')));
}

function checkSession(s: Session): Session {
  if (typeof s?.csrf_token !== 'string' || s.csrf_token === '') throw new ApiError('internal', 200);
  return s;
}

/** createHttpClient talks to internal/api on the same origin, relative to the page. */
export function createHttpClient(options: HttpClientOptions = {}): ApiClient {
  const fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
  const base = options.base ?? document.baseURI;
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  let csrf: string | null = null;

  /**
   * request sends one call. A write the server refuses with csrf_invalid (token rotated or
   * expired) was not carried out: the client fetches a fresh session and repeats it once,
   * so writes, the emergency stop above all, do not stay blocked until a reload (S2).
   */
  async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
    try {
      return await send<T>(method, path, body);
    } catch (err) {
      if (method === 'GET' || !(err instanceof ApiError) || err.code !== 'csrf_invalid' || err.status === 0) throw err;
      try {
        useSession(await send<Session>('GET', 'session'));
      } catch {
        throw err;
      }
      return send<T>(method, path, body);
    }
  }

  async function send<T>(method: Method, path: string, body?: unknown): Promise<T> {
    const headers = new Headers({ Accept: 'application/json' });
    if (method !== 'GET') {
      if (csrf === null) throw new ApiError('csrf_invalid', 0);
      headers.set('X-HM-CSRF', csrf);
    }
    if (body !== undefined) headers.set('Content-Type', 'application/json');

    let res: Response;
    try {
      res = await fetch(new URL(`api/${path}`, base).href, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        credentials: 'same-origin',
        redirect: 'error',
        cache: 'no-store',
        signal: AbortSignal.timeout(timeoutMs),
      });
    } catch {
      throw new ApiError('unavailable', 0);
    }

    if (!res.ok) {
      const err = await errorFrom(res, path);
      if (err.code === 'csrf_invalid') csrf = null;
      throw err;
    }
    if (res.status === 204) return undefined as T;
    if (!(res.headers.get('Content-Type') ?? '').startsWith('application/json')) {
      throw new ApiError('internal', res.status);
    }
    try {
      return (await res.json()) as T;
    } catch {
      throw new ApiError('internal', res.status);
    }
  }

  const get = <T>(path: string) => request<T>('GET', path);

  /** useSession keeps the CSRF token of every session the server returns. */
  const useSession = (s: Session): Session => {
    csrf = checkSession(s).csrf_token;
    return s;
  };

  return {
    session: async () => useSession(await get<Session>('session')),
    setLanguage: async (language) => useSession(await request<Session>('PUT', 'session/language', { language })),
    system: () => get('system'),

    agents: () => get('agents'),
    revokeAgent: (clientId) => request('POST', 'agents/revoke', { client_id: clientId }),
    pairingCheck: (code) => request('POST', 'pairing/check', { code }),
    pairingApprove: (approve) => request('POST', 'pairing/approve', approve),
    pairingDeny: (decision) => request('POST', 'pairing/deny', decision),

    devices: () => get('devices'),

    mandates: () => get('mandates'),
    createMandate: (create) => request('POST', 'mandates', create),
    mandate: async (id) => get(`mandates/${segment(id)}`),
    mandateVersion: async (id, number) => get(`mandates/${segment(id)}/versions/${versionSegment(number)}`),
    putMandate: async (id, update) => request('PUT', `mandates/${segment(id)}`, update),
    applyTemplate: async (id, apply) => request('POST', `mandates/${segment(id)}/apply-template`, apply),
    revokeMandate: async (id) => request('POST', `mandates/${segment(id)}/revoke`),

    templates: () => get('templates'),
    template: async (name) => get(`templates/${segment(name)}`),
    putTemplate: async (name, update) => request('PUT', `templates/${segment(name)}`, update),
    deleteTemplate: async (name) => request('DELETE', `templates/${segment(name)}`),

    settings: () => get('settings'),
    putSettings: (defaults) => request('PUT', 'settings', defaults),

    approvals: () => get('approvals'),
    answerApproval: (id, approve) => request('POST', `approvals/${segment(id)}/answer`, { approve } satisfies ApprovalAnswer),

    audit: (q) => get(`audit${auditQuery(q)}`),
    verifyAudit: () => request('POST', 'audit/verify'),

    approvers: () => get('approvers'),
    putApprover: async (id, update, baseVersion) => request('PUT', `approvers/${segment(id)}`, { ...update, base_version: baseVersion }),
    testApprover: async (id) => request('POST', `approvers/${segment(id)}/test`),
    deleteApprover: async (id, baseVersion) => request('DELETE', `approvers/${segment(id)}?base_version=${encodeURIComponent(baseVersion)}`),

    setEmergencyStop: (active) => request('PUT', 'emergency-stop', { active }),

    events: (handlers) =>
      connectEvents({
        url: eventsUrl(base),
        csrf: () => csrf,
        ...handlers,
        ...(options.socket ? { socket: options.socket } : {}),
      }),
  };
}
