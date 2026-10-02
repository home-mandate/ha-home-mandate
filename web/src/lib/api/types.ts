// SPDX-License-Identifier: AGPL-3.0-or-later

// Contract of the JSON API between the UI and the gateway (internal/api). The Go side
// implements exactly these shapes; field names are snake_case as on the wire. All times
// are RFC 3339 strings in UTC and are formatted for display in the household time zone
// (src/lib/format.ts). Paths are relative ("api/…") because Ingress serves the UI under
// a per-installation prefix.

/** UI languages at release. */
export type Language = 'de' | 'en';

// ---------------------------------------------------------------------------
// Session: GET api/session, PUT api/session/language

export interface UnitSystem {
  /** As reported by Home Assistant, e.g. "°C" or "°F". */
  temperature: string;
  length: string;
  mass: string;
  volume: string;
  pressure: string;
  wind_speed: string;
}

export interface Household {
  /** IANA time zone of Home Assistant; all times are shown in it. */
  time_zone: string;
  /** Language of the Home Assistant configuration, the fallback after the browser. */
  language: string;
  unit_system: UnitSystem;
}

export interface Session {
  /** The Home Assistant user signed in through Ingress; always an administrator. */
  user: { id: string; name: string };
  /** Sent back in the X-HM-CSRF header on every request that changes something. */
  csrf_token: string;
  /** The user's language setting in Home-Mandate; null means browser language. */
  language: Language | null;
  household: Household;
}

export interface LanguageUpdate {
  language: Language | null;
}

// HTTP status per error code (see ApiErrorCode at the end):
// 400 invalid_input · 401 unauthenticated · 403 forbidden, csrf_invalid · 404 not_found ·
// 409 conflict · 413 too_large · 422 invalid_mandate, critical_confirmation_required ·
// 429 rate_limited · 500 internal · 503 unavailable (Home Assistant not reachable).

// ---------------------------------------------------------------------------
// Agents: GET api/agents, POST api/agents/revoke, GET api/pairing
//
// Client IDs are often URLs. They travel in the body, never in the path: an encoded "/"
// (%2F) does not survive every proxy on the Ingress path unchanged.

export type AgentStatus = 'active' | 'revoked';

export interface Agent {
  client_id: string;
  /** Chosen by the human at admission; untrusted text, rendered escaped. */
  display_name: string;
  status: AgentStatus;
  created_at: string;
  /** Home Assistant user ID of whoever admitted the agent. */
  created_by: string;
  /**
   * Client ID Metadata Document URL or the identifier the agent gave with a pairing code.
   * Untrusted; link it only through safeLink (./url.ts).
   */
  oauth_client: string;
  /** True only for a fetched and checked Client ID Metadata Document. */
  client_verified: boolean;
  /**
   * The agent's current mandate. A revoked mandate stays visible here; null only if the
   * agent never had one. An active agent with a revoked mandate may do nothing; it gets a
   * new mandate through POST api/mandates.
   */
  mandate: { id: string; status: MandateStatus } | null;
}

/** POST api/agents/revoke: revokes the agent, its tokens and its mandate at once. */
export interface AgentRevoke {
  client_id: string;
}

export interface Pairing {
  /**
   * Absolute URL of the pairing page; null while OAuth is not configured (no public_url).
   * Server-supplied: render as a link only through safeLink (./url.ts).
   */
  url: string | null;
}

// ---------------------------------------------------------------------------
// Devices: GET api/devices – the catalog, for the mandate editor

/** Categories of SPEC-v0 section 5. */
export type Category =
  | 'light'
  | 'switch'
  | 'climate'
  | 'cover'
  | 'gate'
  | 'lock'
  | 'alarm'
  | 'camera'
  | 'media'
  | 'sensor'
  | 'scene'
  | 'script'
  | 'other';

/**
 * Namespaced extension category such as "paperless:document" (schema pattern). Allowed in
 * stored mandates; the editor shows such rules read-only and treats them as critical.
 */
export type ExtensionCategory = `${string}:${string}`;

export interface Area {
  /** Home Assistant area_id, as used in mandate rules. */
  id: string;
  name: string;
}

export interface Device {
  entity_id: string;
  /** friendly_name from Home Assistant; untrusted text. */
  name: string;
  category: Category;
  area: string | null;
  /** Actions of the category's vocabulary, sorted. */
  actions: string[];
}

export interface DeviceCatalog {
  areas: Area[];
  devices: Device[];
}

// ---------------------------------------------------------------------------
// Mandates (mandate-spec schema/mandate-v0.schema.json)

export type Decision = 'allow' | 'ask' | 'deny';
export type MandateStatus = 'active' | 'revoked';
export type Weekday = 'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun';

/** `any` only on its own; otherwise at least one of entity_id, category, area. */
export type RuleResource =
  | { any: true; entity_id?: never; category?: never; area?: never }
  | ResourceFields;

type ResourceFields =
  | { entity_id: string; category?: Category | ExtensionCategory; area?: string; any?: never }
  | { entity_id?: string; category: Category | ExtensionCategory; area?: string; any?: never }
  | { entity_id?: string; category?: Category | ExtensionCategory; area: string; any?: never };

export interface Conditions {
  /** "HH:MM-HH:MM" in household local time; may cross midnight. */
  time_window?: string;
  weekdays?: Weekday[];
}

export interface Approval {
  /** ISO 8601 duration "PT…M…S". */
  timeout: string;
  /** Home Assistant user IDs. */
  approvers: string[];
}

export interface Rule {
  id: string;
  resource: RuleResource;
  /** Actions of the category's vocabulary or "*". */
  actions: string[];
  decision: Decision;
  conditions?: Conditions;
  /** Only with decision "ask". */
  approval?: Approval;
  /** Only with decision "allow"; needs the separate confirmation (confirm_critical). */
  allow_critical?: true;
}

/**
 * The part of a mandate a human edits. The server adds type, id, principal, agent,
 * default ("deny"), created_by and created_at when it stores a version.
 */
export interface MandateDraft {
  rules: Rule[];
  approval: Approval;
  limits: { max_actions_per_hour: number };
  valid_from: string;
  expires?: string;
}

/** A stored mandate version, as in the schema. */
export interface MandateDocument extends MandateDraft {
  type: 'https://mandate-spec.org/mandate/v0';
  id: string;
  principal: string;
  agent: { client_id: string; display_name: string };
  default: 'deny';
  created_by: string;
  created_at: string;
}

export interface MandateSummary {
  id: string;
  client_id: string;
  agent_display_name: string;
  status: MandateStatus;
  /** Digest of the current version. */
  digest: string;
  max_actions_per_hour: number;
  updated_at: string;
}

export interface MandateVersion {
  digest: string;
  created_at: string;
  created_by: string;
}

/** GET api/mandates/{id}; versions newest first. */
export interface MandateDetail {
  summary: MandateSummary;
  document: MandateDocument;
  versions: MandateVersion[];
}

/**
 * POST api/mandates: a new mandate for an active agent without an active mandate, as a
 * copy of a template (like an admission). Answers "conflict" if the agent has one.
 */
export interface MandateCreate {
  client_id: string;
  template: string;
}

/**
 * PUT api/mandates/{id}. base_digest is the version the edit started from; if another
 * version was stored meanwhile the server answers "conflict". confirm_critical must be
 * true when the draft grants allow_critical that the base version did not (decision U9):
 * an allow_critical rule that is new or changed in any field. Revoked mandates cannot
 * be edited ("conflict").
 */
export interface MandateUpdate {
  draft: MandateDraft;
  base_digest: string;
  confirm_critical?: boolean;
}

// ---------------------------------------------------------------------------
// Preview "may afterwards": POST api/mandates/preview (decision U10)
//
// One entry per device and action of the catalog. The server answers "too_large" above
// 5000 entries. The preview shows effective decisions, so it also reveals critical
// actions that become allowed because a restricting rule was removed.

export interface PreviewRequest {
  draft: MandateDraft;
  /** Compare against this mandate's active version. */
  mandate_id?: string;
  /** Reference time, default now; evaluated in the household time zone. */
  at?: string;
}

export interface PreviewEntry {
  entity_id: string;
  action: string;
  decision: Decision;
  /** Reason code of SPEC-v0 section 4.2. */
  reason: Reason;
  rule_id: string | null;
  critical: boolean;
  /** Decision of the active version; absent without mandate_id. */
  previous?: Decision;
  /** For "ask": who is asked and how long the agent waits (SPEC-v0 section 4.1). */
  approval?: Approval;
}

export interface Preview {
  at: string;
  time_zone: string;
  entries: PreviewEntry[];
}

export type Reason =
  | 'invalid_mandate'
  | 'invalid_request'
  | 'unknown_category'
  | 'unknown_action'
  | 'revoked'
  | 'not_yet_valid'
  | 'expired'
  | 'no_match'
  | 'critical_demotion'
  | 'rule';

// ---------------------------------------------------------------------------
// Templates: GET api/templates, GET|PUT|DELETE api/templates/{name}

export interface TemplateSummary {
  name: string;
  created_at: string;
  created_by: string;
}

export interface Template {
  name: string;
  draft: MandateDraft;
}

export interface TemplateUpdate {
  draft: MandateDraft;
  confirm_critical?: boolean;
}

// ---------------------------------------------------------------------------
// Audit log: GET api/audit, GET api/audit/verify (mandate-spec audit-v0)

export type AuditEvent =
  | 'decision'
  | 'mandate.created'
  | 'mandate.updated'
  | 'mandate.revoked'
  | 'agent.registered'
  | 'agent.revoked'
  | 'emergency_stop.activated'
  | 'emergency_stop.released'
  | 'auth.rejected'
  | 'log.truncated';

export type ResultStatus = 'executed' | 'denied' | 'failed';
export type DeniedBy = 'mandate' | 'approval' | 'rate_limit' | 'emergency_stop' | 'authentication';
export type ApprovalOutcome = 'approved' | 'rejected' | 'timeout' | 'invalid_response';

export interface AuditEntry {
  id: string;
  seq: number;
  recorded_at: string;
  event: AuditEvent;
  actor?: { kind: 'user' | 'agent' | 'system'; id: string };
  agent?: { client_id: string; display_name?: string };
  request?: {
    time: string;
    timezone?: string;
    resource: { entity_id: string; category?: string; area?: string };
    action: string;
  };
  mandate?: { id: string; digest: string; previous_digest?: string };
  evaluation?: { decision: Decision; reason: Reason; rule_id: string | null; approval_timeout?: string };
  approval?: { outcome: ApprovalOutcome; by?: string; at: string };
  result?: { status: ResultStatus; denied_by?: DeniedBy; error?: string; duration_ms?: number };
  truncated?: { up_to_seq: number; last_digest: string };
}

export interface AuditQuery {
  /** Entries with seq below this value; omitted for the newest. */
  before?: number;
  /** 1–100, default 50. */
  limit?: number;
  agent?: string;
  entity_id?: string;
  event?: AuditEvent;
  decision?: Decision;
}

/** Newest first; next_before is null on the last page. */
export interface AuditPage {
  entries: AuditEntry[];
  next_before: number | null;
}

export interface AuditVerification {
  valid: boolean;
  /** seq of the first entry whose chain link fails; null when valid. */
  broken_at_seq: number | null;
  checked: number;
}

// ---------------------------------------------------------------------------
// Approvers: GET api/approvers, PUT|DELETE api/approvers/{user_id},
// POST api/approvers/{user_id}/test (sends a test notification, no actions in it)

export interface Approver {
  user_id: string;
  name: string;
  /** Service name without "notify.", e.g. "mobile_app_pixel_9". */
  notify_service: string;
  /** null means the household language. */
  language: Language | null;
}

export interface ApproverCandidates {
  /** People from person.* with a Home Assistant user. */
  people: { user_id: string; name: string }[];
  notify_services: string[];
}

export interface ApproverList {
  approvers: Approver[];
  candidates: ApproverCandidates;
}

export interface ApproverUpdate {
  notify_service: string;
  language: Language | null;
}

// ---------------------------------------------------------------------------
// Emergency stop: GET|PUT api/emergency-stop

export interface EmergencyStop {
  active: boolean;
}

// ---------------------------------------------------------------------------
// Errors: every non-2xx answer has this body; no internal details.

export type ApiErrorCode =
  | 'unauthenticated'
  | 'forbidden'
  | 'csrf_invalid'
  | 'not_found'
  | 'conflict'
  | 'invalid_input'
  | 'invalid_mandate'
  | 'critical_confirmation_required'
  | 'too_large'
  | 'rate_limited'
  | 'unavailable'
  | 'internal';

export interface ApiErrorBody {
  code: ApiErrorCode;
  /** JSON pointer into the request body for invalid_input/invalid_mandate, e.g. "/draft/rules/2/actions". */
  field?: string;
}
