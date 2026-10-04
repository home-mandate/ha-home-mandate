// SPDX-License-Identifier: AGPL-3.0-or-later

// Contract of the JSON API between the UI and the gateway (internal/api), version 2. The
// Go side implements exactly these shapes; field names are snake_case as on the wire. All
// times are RFC 3339 strings in UTC and are formatted for display in the household time
// zone (src/lib/format.ts). Paths are relative ("api/…") because Ingress serves the UI
// under a per-installation prefix. Live changes arrive over the WebSocket api/events
// (ServerEvent at the end); after every (re)connect the UI reloads what it shows.

/** UI languages at release. */
export type Language = 'de' | 'en';

// HTTP status per error code (see ApiErrorCode at the end):
// 400 invalid_input, pairing_code_invalid · 401 unauthenticated · 403 forbidden,
// csrf_invalid · 404 not_found · 409 conflict · 410 pairing_code_expired · 413 too_large ·
// 422 invalid_mandate, critical_confirmation_required · 429 rate_limited, pairing_locked ·
// 500 internal · 503 unavailable (Home Assistant not reachable).

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

// ---------------------------------------------------------------------------
// System: GET api/system – header, banners, overview, settings

export interface EmergencyStop {
  active: boolean;
  /** When it was switched on; null while off. */
  since: string | null;
  /** Name of the Home Assistant user who switched it on; null while off or if unknown. */
  by_name: string | null;
}

export interface ChainStatus {
  valid: boolean;
  /** seq of the first entry whose chain link fails; null when valid. */
  broken_at_seq: number | null;
  /** Last verification (every 10 minutes and on request); null before the first. */
  checked_at: string | null;
}

export interface SystemStatus {
  /** v0.1 serves the UI only through Ingress, i.e. in app mode. */
  mode: 'app' | 'container';
  version: string;
  commit: string;
  /** Server clock, for countdowns that must not depend on the browser clock. */
  server_time: string;
  retention_days: number;
  /**
   * user_name: Home-Mandate's own Home Assistant user. commands: the fixed allowlist of
   * WebSocket commands it may send (internal/ha), shown under "Why admin rights?".
   */
  ha: { connected: boolean; since: string | null; version: string | null; user_name: string | null; commands: string[] };
  /** URL agents connect to; null without TLS (MCP only on localhost then). */
  mcp_url: string | null;
  tls: { present: boolean; valid_until: string | null };
  emergency_stop: EmergencyStop;
  chain: ChainStatus;
  /** Approvers set up; 0 means every approval request is denied at once. */
  approvers_configured: number;
}

// ---------------------------------------------------------------------------
// Agents: GET api/agents, POST api/agents/revoke
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
  /** Home Assistant user ID and name of whoever admitted the agent. */
  created_by: string;
  created_by_name: string | null;
  /** Last request of the agent (any decision); null if none yet. */
  last_active_at: string | null;
  /**
   * Client ID Metadata Document URL or the identifier the agent gave with a pairing code.
   * Untrusted; link it only through safeLink (./url.ts).
   */
  oauth_client: string;
  /** True only for a fetched and checked Client ID Metadata Document. */
  client_verified: boolean;
  /** Redirect URIs registered through OAuth (browser sign-in); empty for pairing codes. Untrusted. */
  redirect_uris: string[];
  /** When and by whom the agent was revoked; null while active. */
  revoked_at: string | null;
  revoked_by_name: string | null;
  /** Requests of the agent (any decision) on the current day in the household's time zone. */
  requests_today: number;
  /** Requests counted against the mandate's rate limit in the last 60 minutes. */
  actions_last_hour: number;
  /**
   * The agent's current mandate. A revoked mandate stays visible here; null only if the
   * agent never had one. An active agent with a revoked mandate may do nothing; it gets a
   * new mandate through POST api/mandates. max_actions_per_hour: the mandate's rate limit,
   * null without one; digest: of its current version (base for apply-template).
   */
  mandate: { id: string; name: string; status: MandateStatus; max_actions_per_hour: number | null; digest: string } | null;
}

/** POST api/agents/revoke: revokes the agent, its tokens and its mandate at once. */
export interface AgentRevoke {
  client_id: string;
}

// ---------------------------------------------------------------------------
// Pairing by code inside the UI (decision D5): POST api/pairing/check,
// POST api/pairing/approve, POST api/pairing/deny. Wrong codes count towards the lock
// (5 per session, 30 per 10 minutes for everyone).

export interface PairingCode {
  /** As typed; the server ignores case, spaces and the dash ("BCDF-GHJK"). */
  code: string;
}

/** The agent waiting behind a code. */
export interface PairingCandidate {
  /**
   * Opaque ID of this pending request. Approve and deny send it back; if the code now
   * belongs to another request, the server answers "conflict" (the person saw a different agent).
   */
  pairing_id: string;
  /** Name the agent claims; untrusted. */
  claimed_name: string;
  /** OAuth client ID: verified metadata URL or the agent's free identifier. */
  client: string;
  client_verified: boolean;
  requested_at: string;
  expires_at: string;
  /** Network address the pairing request came from (decision G4); only shown here, not logged in the audit. */
  requested_from: string;
}

export interface PairingDecision extends PairingCode {
  /** From the check: the request the person looked at. */
  pairing_id: string;
}

export interface PairingApprove extends PairingDecision {
  /** Display name chosen by the human. */
  display_name: string;
  /** The template that becomes the agent's mandate. */
  template: string;
  /** Name of the new mandate; default: the template name. */
  mandate_name?: string;
  /**
   * The separate confirmation (decision U9) for a template whose rules allow critical
   * actions without approval; without it the server answers "critical_confirmation_required".
   */
  confirm_critical?: boolean;
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
/** Stored status; "not yet valid" and "expired" follow from the dates and the server time. */
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
  /** ISO 8601 duration "PT…M…S", 10 s to 1 h. */
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
  /**
   * Display name (decision D2): Home-Mandate metadata next to the document, not part of
   * it, so the specification and the digest are untouched.
   */
  name: string;
  client_id: string;
  agent_display_name: string;
  status: MandateStatus;
  /** Digest of the current version. */
  digest: string;
  rule_count: number;
  valid_from: string;
  expires: string | null;
  max_actions_per_hour: number;
  updated_at: string;
}

export interface MandateVersion {
  /**
   * Number of the version within its mandate, counted from 1 for the oldest. It tells
   * versions apart: the digest is a hash of the content, and a version that restores an
   * earlier one repeats its digest.
   */
  number: number;
  digest: string;
  created_at: string;
  created_by: string;
  created_by_name: string | null;
}

/** GET api/mandates/{id}; versions newest first. A single version: GET api/mandates/{id}/versions/{number}. */
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
  /** Default: the template name. */
  name?: string;
  /** As for PairingApprove: needed for a template with allow_critical rules (U9). */
  confirm_critical?: boolean;
}

/**
 * PUT api/mandates/{id}. base_digest is the digest of the version the edit started from;
 * if that is not the current version any more the server answers "conflict" and stores
 * nothing. confirm_critical must be true when the draft grants allow_critical that the
 * current version does not have (decision U9): an allow_critical rule that is new or
 * changed in any field; otherwise the answer is "critical_confirmation_required".
 * Revoked mandates cannot be edited ("conflict"). A rename alone stores no new version.
 * The server enforces all of this itself (internal/mandate Store.Update), whatever the
 * UI checked.
 */
export interface MandateUpdate {
  name: string;
  draft: MandateDraft;
  base_digest: string;
  confirm_critical?: boolean;
}

/**
 * POST api/mandates/{id}/apply-template (decision D3): the template's rules, approval and
 * limits become a new version of this mandate; dates and name stay. Same conflict and
 * U9 rules as an update.
 */
export interface ApplyTemplate {
  template: string;
  base_digest: string;
  confirm_critical?: boolean;
}

/** Reason codes of SPEC-v0 section 4.1. */
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
  rule_count: number;
  created_at: string;
  created_by: string;
  created_by_name: string | null;
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
// Settings: GET|PUT api/settings – defaults for new templates and mandates

export interface Defaults {
  /** "PT…M…S"; capped by the server's HM_APPROVAL_TIMEOUT. */
  approval_timeout: string;
  max_actions_per_hour: number;
  /**
   * Also a neutral note in Home Assistant's notification bell while a request waits
   * (decision F2 B2): no agent, device or reason in it, removed when the request ends.
   */
  bell: boolean;
}

// ---------------------------------------------------------------------------
// Approval requests: GET api/approvals – open ones and the recent history (newest first)

export type ApprovalOutcome = 'approved' | 'rejected' | 'timeout' | 'invalid_response';

/** One service data field of a request, e.g. brightness_pct=100; sorted by name by the server. */
export interface ApprovalParam {
  name: string;
  /** The value as text, sanitized by the server like the push; untrusted. */
  value: string;
}

export interface ApprovalRequest {
  id: string;
  agent: { client_id: string; display_name: string };
  entity_id: string;
  device_name: string;
  area: string | null;
  action: string;
  critical: boolean;
  /** The agent's claim, sanitized by the server; untrusted. */
  reason: string | null;
  /**
   * Service data the action would be called with, as the push shows it (security review S1);
   * empty for none. The UI shows every field before an approval.
   */
  params: ApprovalParam[];
  /** Names of the approvers the request reached (any channel). */
  recipients: string[];
  created_at: string;
  expires_at: string;
  /**
   * The signed-in person may answer this request here now (decision F2): an approver of
   * it with the UI channel, and for a critical action with UI for critical actions too.
   * The server checks again when the answer comes.
   */
  can_answer: boolean;
}

export interface ApprovalHistoryEntry {
  /** seq of the audit entry. */
  seq: number;
  agent: { client_id: string; display_name: string };
  entity_id: string;
  device_name: string;
  action: string;
  /** emergency_stop / revoked: the stop or the agent's revocation ended the request before an answer (F1). */
  outcome: ApprovalOutcome | 'emergency_stop' | 'revoked';
  by_name: string | null;
  /** Channel of the answer (decision F2); absent without an answer by a person. */
  via?: 'push' | 'ui';
  created_at: string;
  answered_at: string;
}

export interface Approvals {
  open: ApprovalRequest[];
  /** Up to 50 most recent. */
  history: ApprovalHistoryEntry[];
}

/**
 * POST api/approvals/{id}/answer: an answer given in the UI (decision F2). The server
 * checks again that the signed-in person may answer this request here now; for an unknown
 * or ended request and for one the person may not answer it says not_found alike, so IDs
 * cannot be probed. Answers the closed request.
 */
export interface ApprovalAnswer {
  approve: boolean;
}

// ---------------------------------------------------------------------------
// Audit log: GET api/audit, POST api/audit/verify (mandate-spec audit-v0)

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

export interface AuditEntry {
  id: string;
  seq: number;
  recorded_at: string;
  event: AuditEvent;
  actor?: { kind: 'user' | 'agent' | 'system'; id: string; name?: string };
  agent?: { client_id: string; display_name?: string };
  request?: {
    time: string;
    timezone?: string;
    resource: { entity_id: string; category?: string; area?: string };
    action: string;
  };
  /** version: number of the mandate version with this digest (added by the API, not part of the chain; F4). */
  mandate?: { id: string; digest: string; previous_digest?: string; version?: number };
  evaluation?: { decision: Decision; reason: Reason; rule_id: string | null; approval_timeout?: string };
  /** via: the channel the answer came through, push or ui (decision F2). */
  approval?: { outcome: ApprovalOutcome; by?: string; by_name?: string; via?: 'push' | 'ui'; at: string };
  result?: { status: ResultStatus; denied_by?: DeniedBy; error?: string; duration_ms?: number };
  truncated?: { up_to_seq: number; last_digest: string };
  /** Digest of this entry and of the one before (technical details). */
  digest: string;
  prev: string | null;
}

/** Filter "decision" values: the three decisions, plus "default" for reason no_match. */
export type DecisionFilter = Decision | 'default';

export interface AuditQuery {
  /** Entries with seq below this value; omitted for the newest. */
  before?: number;
  /** 1–100, default 50. */
  limit?: number;
  since?: string;
  until?: string;
  agent?: string;
  /** Exact entity_id or area_id. */
  device?: string;
  /**
   * Search text (at most 100 characters after cleaning; empty means none). Matches entries
   * whose entity_id, area_id, agent name or client_id, or whose device or area name in the
   * current catalog contains it, ignoring case. Combined with the other filters by AND.
   */
  q?: string;
  /** decision: requests only; admin: everything else. */
  group?: 'decision' | 'admin';
  event?: AuditEvent;
  decisions?: DecisionFilter[];
}

/** Newest first; next_before is null on the last page; total counts all matches of the filter, regardless of the cursor. */
export interface AuditPage {
  entries: AuditEntry[];
  next_before: number | null;
  total: number;
}

export interface AuditVerification extends ChainStatus {
  /** Entries checked by this run. */
  checked: number;
}

// ---------------------------------------------------------------------------
// Approvers: GET api/approvers, PUT|DELETE api/approvers/{user_id},
// POST api/approvers/{user_id}/test (sends a test notification, no actions in it).
// A change names the version of the approvers it is based on (PUT: base_version in the
// body, DELETE: ?base_version=); the first change wins, a later one based on an older
// version is answered with "conflict" and stores nothing.

/** A phone or computer with the Home Assistant app, as one approver uses it. */
export interface ApproverDevice {
  /** Service name without "notify.", e.g. "mobile_app_pixel_9". */
  service: string;
  /** Also critical requests (unlocking etc.); off for devices that ask for no unlocking (decision F2). */
  critical: boolean;
}

/**
 * Someone who answers approval requests (decision F2): on each of up to five devices, and
 * if ui is set also in the Home-Mandate UI (administrators only), there for critical
 * requests only with ui_critical. At least one channel.
 */
export interface Approver {
  user_id: string;
  name: string;
  devices: ApproverDevice[];
  ui: boolean;
  ui_critical: boolean;
  /** null means the household language. */
  language: Language | null;
  /**
   * How requests reach the person, computed by the server from the channels and the admin
   * role now (decision S9); the UI does not recompute it. push: a device gets it; ui: only
   * in Home-Mandate, seen only while it is open; none: not at all.
   */
  reach: { normal: ReachChannel; critical: ReachChannel };
}

export type ReachChannel = 'push' | 'ui' | 'none';

export interface ApproverCandidates {
  /** People from person.* with a Home Assistant user; is_admin decides whether the UI channel is offered. */
  people: { user_id: string; name: string; is_admin: boolean }[];
  /**
   * Devices with the Home Assistant app. suggest_critical comes from the device registry:
   * on only for iOS, which asks for unlocking before a notification button counts; off for
   * Android, the Mac app and anything unknown (decision S11).
   * name is Home Assistant's device name (untrusted text; the app's user can set it).
   * owner_user_id: the Home Assistant user the app is signed in with; null if unknown.
   */
  devices: { service: string; name: string; suggest_critical: boolean; owner_user_id: string | null }[];
}

export interface ApproverList {
  approvers: Approver[];
  candidates: ApproverCandidates;
  /** Version of the approvers, the base of the next change. */
  version: string;
}

/** PUT api/approvers/{user_id} (with base_version); invalid_input names /devices, /ui, /ui_critical or /base_version. */
export interface ApproverUpdate {
  devices: ApproverDevice[];
  ui: boolean;
  ui_critical: boolean;
  language: Language | null;
}

// ---------------------------------------------------------------------------
// Emergency stop: PUT api/emergency-stop {active} → EmergencyStop (state: api/system)

// ---------------------------------------------------------------------------
// Live events: WebSocket api/events (decision D6). Server → client only; the client sends
// exactly one message after opening, {"csrf": "<token>"}. The server answers an accepted
// token with a "system" event and closes with 4419 on a wrong token or 4403 when the user
// is no longer an administrator (checked every 60 s). Events carry what changed; for
// lists the UI reloads through the REST endpoints.

export type ServerEvent =
  | { type: 'system'; system: SystemStatus }
  | { type: 'approval.opened'; request: ApprovalRequest }
  | { type: 'approval.closed'; id: string; entry: ApprovalHistoryEntry }
  | { type: 'audit.appended'; seq: number }
  | { type: 'agents.changed' }
  | { type: 'mandates.changed'; id: string }
  | { type: 'templates.changed' }
  | { type: 'approvers.changed' }
  | { type: 'settings.changed' };

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
  | 'pairing_code_invalid'
  | 'pairing_code_expired'
  | 'pairing_locked'
  | 'too_large'
  | 'rate_limited'
  | 'unavailable'
  | 'internal';

export interface ApiErrorBody {
  code: ApiErrorCode;
  /** JSON pointer into the request body for invalid_input/invalid_mandate, e.g. "/draft/rules/2/actions". */
  field?: string;
  /** Seconds until a pairing lock or rate limit ends. */
  retry_after?: number;
}
