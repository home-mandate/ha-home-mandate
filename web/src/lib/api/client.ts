// SPDX-License-Identifier: AGPL-3.0-or-later

// Client for the JSON API (contract in ./types.ts). ApiClient is the seam between the
// UI and the server: createHttpClient talks to internal/api, createMockClient (./mock.ts)
// serves fixtures for component tests and for building the UI before the backend.

import type {
  Agent,
  ApiErrorBody,
  ApiErrorCode,
  ApproverList,
  ApproverUpdate,
  AuditPage,
  AuditQuery,
  AuditVerification,
  DeviceCatalog,
  EmergencyStop,
  Language,
  MandateCreate,
  MandateDetail,
  MandateDocument,
  MandateSummary,
  MandateUpdate,
  Pairing,
  Preview,
  PreviewRequest,
  Session,
  Template,
  TemplateSummary,
  TemplateUpdate,
} from './types.ts';

export interface ApiClient {
  /** Loads the session; must be called first, it carries the CSRF token for writes. */
  session(): Promise<Session>;
  setLanguage(language: Language | null): Promise<Session>;

  agents(): Promise<Agent[]>;
  revokeAgent(clientId: string): Promise<Agent>;
  pairing(): Promise<Pairing>;

  devices(): Promise<DeviceCatalog>;

  mandates(): Promise<MandateSummary[]>;
  createMandate(create: MandateCreate): Promise<MandateDetail>;
  mandate(id: string): Promise<MandateDetail>;
  mandateVersion(id: string, digest: string): Promise<MandateDocument>;
  putMandate(id: string, update: MandateUpdate): Promise<MandateDetail>;
  revokeMandate(id: string): Promise<MandateSummary>;
  preview(request: PreviewRequest): Promise<Preview>;

  templates(): Promise<TemplateSummary[]>;
  template(name: string): Promise<Template>;
  putTemplate(name: string, update: TemplateUpdate): Promise<Template>;
  deleteTemplate(name: string): Promise<void>;

  audit(query: AuditQuery): Promise<AuditPage>;
  verifyAudit(): Promise<AuditVerification>;

  approvers(): Promise<ApproverList>;
  putApprover(userId: string, update: ApproverUpdate): Promise<ApproverList>;
  testApprover(userId: string): Promise<void>;
  deleteApprover(userId: string): Promise<void>;

  emergencyStop(): Promise<EmergencyStop>;
  setEmergencyStop(active: boolean): Promise<EmergencyStop>;
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

/** Longer than the server's own limits; the emergency stop must never hang silently. */
const DEFAULT_TIMEOUT_MS = 15_000;

/** ApiError carries only a known code (and a JSON pointer); the UI maps it to a message. */
export class ApiError extends Error {
  readonly code: ApiErrorCode;
  /** HTTP status; 0 when the server was not reached or did not answer in time. */
  readonly status: number;
  readonly field: string | undefined;

  constructor(code: ApiErrorCode, status: number, field?: string) {
    super(`api: ${code}`);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.field = field;
  }
}

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;
type Method = 'GET' | 'POST' | 'PUT' | 'DELETE';

export interface HttpClientOptions {
  fetch?: Fetch;
  /** URL the "api/…" paths resolve against; the page itself under its Ingress prefix. */
  base?: string;
  timeoutMs?: number;
}

/** segment encodes one path segment and refuses values that could change the path. */
function segment(value: string): string {
  if (value === '' || value === '.' || value === '..') throw new ApiError('invalid_input', 0);
  return encodeURIComponent(value);
}

function auditQuery(q: AuditQuery): string {
  const params = new URLSearchParams();
  if (q.before !== undefined) params.set('before', String(q.before));
  if (q.limit !== undefined) params.set('limit', String(q.limit));
  if (q.agent !== undefined) params.set('agent', q.agent);
  if (q.entity_id !== undefined) params.set('entity_id', q.entity_id);
  if (q.event !== undefined) params.set('event', q.event);
  if (q.decision !== undefined) params.set('decision', q.decision);
  const s = params.toString();
  return s ? `?${s}` : '';
}

async function errorFrom(res: Response): Promise<ApiError> {
  let body: Partial<ApiErrorBody> = {};
  try {
    body = (await res.json()) as Partial<ApiErrorBody>;
  } catch {
    // Not JSON: a proxy page or a crash; never show it.
  }
  const fallback = BY_STATUS[res.status] ?? 'internal';
  const code = typeof body.code === 'string' && ERROR_CODES.has(body.code) ? body.code : fallback;
  const field = typeof body.field === 'string' && JSON_POINTER.test(body.field) ? body.field : undefined;
  return new ApiError(code, res.status, field);
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

  async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
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
      const err = await errorFrom(res);
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

    agents: () => get('agents'),
    revokeAgent: (clientId) => request('POST', 'agents/revoke', { client_id: clientId }),
    pairing: () => get('pairing'),

    devices: () => get('devices'),

    mandates: () => get('mandates'),
    createMandate: (create) => request('POST', 'mandates', create),
    mandate: async (id) => get(`mandates/${segment(id)}`),
    mandateVersion: async (id, digest) => get(`mandates/${segment(id)}/versions/${segment(digest)}`),
    putMandate: async (id, update) => request('PUT', `mandates/${segment(id)}`, update),
    revokeMandate: async (id) => request('POST', `mandates/${segment(id)}/revoke`),
    preview: (req) => request('POST', 'mandates/preview', req),

    templates: () => get('templates'),
    template: async (name) => get(`templates/${segment(name)}`),
    putTemplate: async (name, update) => request('PUT', `templates/${segment(name)}`, update),
    deleteTemplate: async (name) => request('DELETE', `templates/${segment(name)}`),

    audit: (q) => get(`audit${auditQuery(q)}`),
    verifyAudit: () => get('audit/verify'),

    approvers: () => get('approvers'),
    putApprover: async (id, update) => request('PUT', `approvers/${segment(id)}`, update),
    testApprover: async (id) => request('POST', `approvers/${segment(id)}/test`),
    deleteApprover: async (id) => request('DELETE', `approvers/${segment(id)}`),

    emergencyStop: () => get('emergency-stop'),
    setEmergencyStop: (active) => request('PUT', 'emergency-stop', { active }),
  };
}
