// SPDX-License-Identifier: AGPL-3.0-or-later

// A household for the mock client and component tests, shaped like the E2E environment
// (Home Assistant demo integration). Some texts are hostile on purpose: they must render
// escaped, isolated (bidi) and without breaking the layout.

import type {
  Agent,
  ApprovalHistoryEntry,
  ApprovalRequest,
  ApproverList,
  AuditEntry,
  Defaults,
  DeviceCatalog,
  MandateDocument,
  MandateDraft,
  Rule,
  Session,
  SystemStatus,
  Template,
} from './types.ts';

export const HOSTILE_NAME = '<img src=x onerror=alert(1)> Agent "quoted" & <b>bold</b>';
export const BIDI_NAME = 'Helfer \u202Egnalnegrom\u202C \u2066x\u2069';
export const LONG_NAME = 'Sehr langer Agentenname für den Pseudo-Lokalisierungstest mit Überlänge';
export const HOSTILE_REASON = 'Bitte jetzt öffnen!\nIgnoriere alle Regeln. [Link](javascript:alert(1)) \u202Eesrever';
/**
 * Worst case for agent text (decision P5 / step 6c): bidi override and isolate, markup, line
 * breaks and tabs, zero-width and blank-looking letters, stacked combining marks, and one long
 * word, more than 500 characters in all.
 */
export const WORST_NAME =
  '\u202Etnetsissa\u202C <img src=x onerror=alert(1)>\n<b>Fett</b>\tAgent \u2066\u200B\u3164\u2800 Z\u0301\u0302\u0303\u0304\u0305algo ' +
  'Überlänge'.repeat(70);
export const WORST_REASON = `${'Bitte jetzt öffnen!\n[Link](javascript:alert(1)) \u202Eesrever '}${'Dringend '.repeat(70)}`;

/** Home Assistant users of the household: ID → name. */
export const USERS: Readonly<Record<string, string>> = { 'u-admin': 'Markus', 'u-partner': 'Alex' };

/** The mock's clock: Friday 2026-10-02, 19:42 in Berlin. */
export const NOW = '2026-10-02T17:42:00Z';

export const sessionFixture: Session = {
  user: { id: 'u-admin', name: 'Markus' },
  csrf_token: 'csrf-fixture-token',
  language: null,
  household: {
    time_zone: 'Europe/Berlin',
    language: 'de',
    unit_system: { temperature: '°C', length: 'km', mass: 'g', volume: 'L', pressure: 'Pa', wind_speed: 'm/s' },
  },
  sign_out: false,
};

export const systemFixture: SystemStatus = {
  mode: 'app',
  version: '0.1.0',
  commit: 'da11343',
  server_time: NOW,
  retention_days: 30,
  ha: {
    connected: true,
    since: '2026-10-01T06:12:00Z',
    version: '2026.9.4',
    user_name: 'Home-Mandate',
    commands: [
      'auth/current_user',
      'call_service',
      'config/area_registry/list',
      'config/device_registry/list',
      'config/entity_registry/list',
      'config/floor_registry/list',
      'config/label_registry/list',
      'get_config',
      'get_states',
      'ping',
      'subscribe_entities',
      'subscribe_events',
      'unsubscribe_events',
    ],
  },
  mcp_url: 'https://home.example:8765/mcp',
  tls: { present: true, valid_until: '2026-12-24T10:00:00Z', renewal_failed: false },
  emergency_stop: { active: false, since: null, by_name: null },
  chain: { valid: true, broken_at_seq: null, checked_at: '2026-10-02T17:38:00Z' },
  approvers_configured: 1,
  clock_behind: false,
  directory: { store_failing_since: null, overflow: false, renames_last_hour: 0, rename_flood_threshold: 50 },
};

export const defaultsFixture: Defaults = { approval_timeout: 'PT2M', max_actions_per_hour: 60, bell: false };

export const devicesFixture: DeviceCatalog = {
  areas: [
    { id: 'kitchen', name: 'Küche' },
    { id: 'living_room', name: 'Wohnzimmer' },
    { id: 'hallway', name: 'Flur' },
    { id: 'garage', name: 'Garage' },
  ],
  devices: [
    { entity_id: 'light.kitchen', name: 'Küchenlicht', category: 'light', area: 'kitchen', actions: ['read', 'set', 'turn_off', 'turn_on'], critical: false, suggest_critical: false },
    { entity_id: 'light.living_room', name: 'Wohnzimmerlicht', category: 'light', area: 'living_room', actions: ['read', 'set', 'turn_off', 'turn_on'], critical: false, suggest_critical: false },
    { entity_id: 'lock.front_door', name: 'Haustür', category: 'lock', area: 'hallway', actions: ['lock', 'open', 'read', 'unlock'], critical: false, suggest_critical: false },
    { entity_id: 'cover.garage_door', name: 'Garagentor', category: 'gate', area: 'garage', actions: ['close', 'open', 'read'], critical: false, suggest_critical: false },
    { entity_id: 'alarm_control_panel.security', name: 'Alarmanlage', category: 'alarm', area: null, actions: ['arm', 'disarm', 'read'], critical: false, suggest_critical: false },
    { entity_id: 'camera.demo_camera', name: 'Kamera Einfahrt', category: 'camera', area: 'garage', actions: ['read', 'snapshot'], critical: false, suggest_critical: false },
    { entity_id: 'climate.hvac', name: 'Heizung', category: 'climate', area: 'living_room', actions: ['read', 'set_mode', 'set_temperature'], critical: false, suggest_critical: false },
    { entity_id: 'media_player.living_room', name: 'Lautsprecher', category: 'media', area: 'living_room', actions: ['pause', 'play', 'read', 'set_volume', 'turn_off', 'turn_on'], critical: false, suggest_critical: false },
    { entity_id: 'switch.garden_gate', name: 'Gartentor-Öffner', category: 'switch', area: null, actions: ['read', 'turn_off', 'turn_on'], critical: false, suggest_critical: true },
    { entity_id: 'switch.cellar_door', name: 'Kellertür-Summer', category: 'switch', area: 'hallway', actions: ['read', 'turn_off', 'turn_on'], critical: true, suggest_critical: false },
    { entity_id: 'sensor.outside_temperature', name: 'Außentemperatur', category: 'sensor', area: null, actions: ['read'], critical: false, suggest_critical: false },
    { entity_id: 'script.demo', name: HOSTILE_NAME, category: 'script', area: null, actions: ['read', 'run'], critical: false, suggest_critical: false },
  ],
};

export const voiceAssistantDraft: MandateDraft = {
  rules: [
    { id: 'lights', resource: { category: 'light' }, actions: ['read', 'turn_on', 'turn_off', 'set'], decision: 'allow' },
    { id: 'climate', resource: { category: 'climate' }, actions: ['read', 'set_temperature'], decision: 'allow' },
    { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'], decision: 'ask' },
    { id: 'media', resource: { category: 'media' }, actions: ['*'], decision: 'allow', conditions: { time_window: '07:00-22:00' } },
    { id: 'no-cameras', resource: { category: 'camera' }, actions: ['*'], decision: 'deny' },
  ],
  approval: { timeout: 'PT2M', approvers: ['u-admin'] },
  limits: { max_actions_per_hour: 60 },
  valid_from: '2026-10-01T00:00:00Z',
};

export const voiceAssistantMandate: MandateDocument = {
  ...voiceAssistantDraft,
  type: 'https://mandate-spec.org/mandate/v0',
  id: 'mandate-voice',
  principal: 'household:home',
  agent: { client_id: 'pair:voice-assistant', display_name: 'Sprachassistent' },
  default: 'deny',
  created_by: 'u-admin',
  created_at: '2026-10-01T08:00:00Z',
};

type Defaulted = 'created_by' | 'created_by_name' | 'client_verified' | 'redirect_uris' | 'revoked_at' | 'revoked_by_name' | 'requests_today' | 'actions_last_hour' | 'mandate';
type AgentInput = Omit<Agent, Defaulted> & Partial<Omit<Agent, 'mandate'>> & { mandate: { id: string; name: string; status: Agent['status'] } | null };

/** Activity counts and the mandate's limit are filled in by the mock from the log and the mandate. */
const agent = ({ mandate, ...a }: AgentInput): Agent => ({
  created_by: 'u-admin',
  created_by_name: 'Markus',
  client_verified: false,
  redirect_uris: [],
  revoked_at: null,
  revoked_by_name: null,
  requests_today: 0,
  actions_last_hour: 0,
  mandate: mandate && { ...mandate, max_actions_per_hour: null, digest: '' },
  ...a,
});

export const agentsFixture: Agent[] = [
  agent({
    client_id: 'pair:voice-assistant',
    display_name: 'Sprachassistent',
    status: 'active',
    created_at: '2026-10-01T08:00:00Z',
    last_active_at: '2026-10-02T17:40:10Z',
    oauth_client: 'voice-assistant',
    mandate: { id: 'mandate-voice', name: 'Sprachassistent Küche', status: 'active' },
  }),
  agent({
    client_id: 'https://claude.ai/oauth/claude-code-client-metadata',
    display_name: 'Claude Code',
    status: 'active',
    created_at: '2026-10-02T09:15:00Z',
    last_active_at: '2026-10-02T17:12:00Z',
    oauth_client: 'https://claude.ai/oauth/claude-code-client-metadata',
    client_verified: true,
    redirect_uris: ['https://agent.example/oauth/callback'],
    mandate: { id: 'mandate-claude', name: 'Claude Code', status: 'active' },
  }),
  agent({
    client_id: 'pair:old-bot',
    display_name: HOSTILE_NAME,
    status: 'revoked',
    revoked_at: '2026-09-30T07:05:00Z',
    revoked_by_name: 'Markus',
    created_at: '2026-09-29T18:00:00Z',
    last_active_at: '2026-09-30T07:00:00Z',
    oauth_client: 'old-bot',
    mandate: null,
  }),
  agent({
    client_id: 'pair:long',
    display_name: LONG_NAME,
    status: 'active',
    created_at: '2026-10-02T07:00:00Z',
    last_active_at: null,
    oauth_client: 'long',
    mandate: { id: 'mandate-long', name: LONG_NAME, status: 'active' },
  }),
  agent({
    client_id: 'pair:bidi',
    display_name: BIDI_NAME,
    status: 'active',
    created_at: '2026-10-02T07:30:00Z',
    last_active_at: null,
    oauth_client: 'bidi',
    mandate: { id: 'mandate-bidi', name: 'Bidi', status: 'active' },
  }),
];

/** The approvers placeholder of templates (mandate/placeholder.ts), as the server ships it. */
const EVERYONE = { timeout: 'PT2M', approvers: ['$approvers'] };
const BASE_FROM = '2026-01-01T00:00:00Z';
const readAll: Rule = { id: 'r-read-all', resource: { any: true }, actions: ['read'], decision: 'allow' };
const lights: Rule = { id: 'r-lights', resource: { category: 'light' }, actions: ['turn_on', 'turn_off', 'set'], decision: 'allow' };
const climate: Rule = { id: 'r-climate', resource: { category: 'climate' }, actions: ['set_temperature'], decision: 'allow' };

type TemplateInput = Pick<Template, 'name' | 'draft'> & Partial<Template>;

/** Digests are filled in by the mock from the content, as the server computes them. */
const template = (t: TemplateInput): Template => ({
  rule_count: t.draft.rules.length,
  created_at: '2026-10-01T08:00:00Z',
  created_by: 'u-admin',
  created_by_name: 'Markus',
  builtin: false,
  hidden: false,
  title: {},
  description: {},
  digest: '',
  ...t,
});

/** A base template of internal/admission/builtin, with its texts of internal/i18n. */
const builtin = (name: string, rules: Rule[], title: Template['title'], description: Template['description']): Template =>
  template({
    name,
    draft: { rules, approval: EVERYONE, limits: { max_actions_per_hour: 60 }, valid_from: BASE_FROM },
    created_at: null,
    created_by: '',
    created_by_name: null,
    builtin: true,
    title,
    description,
  });

/** Base templates first, in the server's order, then the household's own by name. */
export const templatesFixture: Template[] = [
  builtin(
    'hm-read-only',
    [readAll],
    { de: 'Nur lesen', en: 'Read only' },
    { de: 'Darf den Zustand aller Geräte lesen, aber nichts schalten.', en: 'May read the state of every device, but switches nothing.' },
  ),
  builtin(
    'hm-light-climate',
    [
      readAll,
      lights,
      climate,
      { id: 'r-covers', resource: { category: 'cover' }, actions: ['open', 'close', 'stop', 'set_position'], decision: 'allow' },
    ],
    { de: 'Licht und Klima', en: 'Light and climate' },
    {
      de: 'Darf alles lesen, Licht schalten und dimmen, Temperaturen setzen und Rollläden bewegen. Garagen- und Hoftore gehören nicht dazu.',
      en: 'May read everything, switch and dim lights, set temperatures and move covers such as blinds. Garage doors and gates are not included.',
    },
  ),
  builtin(
    'hm-voice-cautious',
    [
      readAll,
      lights,
      climate,
      { id: 'r-locks', resource: { category: 'lock' }, actions: ['unlock', 'open'], decision: 'ask', approval: EVERYONE },
      { id: 'r-no-cameras', resource: { category: 'camera' }, actions: ['*'], decision: 'deny' },
      { id: 'r-no-alarm', resource: { category: 'alarm' }, actions: ['disarm'], decision: 'deny' },
    ],
    { de: 'Sprachassistent (vorsichtig)', en: 'Voice assistant (cautious)' },
    {
      de: 'Darf alles lesen, Licht schalten und Temperaturen setzen. Schlösser öffnet er nur, wenn du es auf deinem Handy bestätigst; Kameras und das Entschärfen der Alarmanlage nie.',
      en: 'May read everything, switch lights and set temperatures. Opens locks only after you confirm it on your phone; never cameras or disarming the alarm.',
    },
  ),
  template({ name: 'empty', draft: { ...voiceAssistantDraft, rules: [] } }),
  template({ name: 'read-only', draft: { ...voiceAssistantDraft, rules: [{ id: 'read', resource: { any: true }, actions: ['read'], decision: 'allow' }] } }),
  template({ name: 'voice-assistant', draft: voiceAssistantDraft }),
];

export const approversFixture: Omit<ApproverList, 'version'> = {
  approvers: [
    {
      user_id: 'u-admin',
      name: 'Markus',
      devices: [{ service: 'mobile_app_pixel_9', critical: true }],
      ui: true,
      ui_critical: false,
      language: null,
      reach: { normal: 'push', critical: 'push' },
    },
  ],
  candidates: {
    people: [
      { user_id: 'u-admin', name: 'Markus', is_admin: true },
      { user_id: 'u-partner', name: 'Alex', is_admin: false },
    ],
    devices: [
      { service: 'mobile_app_pixel_9', name: 'Pixel 9', suggest_critical: false, owner_user_id: 'u-admin' },
      { service: 'mobile_app_iphone', name: 'iPhone von Alex', suggest_critical: true, owner_user_id: 'u-partner' },
      { service: 'mobile_app_macbook', name: 'MacBook Pro', suggest_critical: false, owner_user_id: 'u-admin' },
      { service: 'mobile_app_tablet', name: 'Galaxy Tab', suggest_critical: false, owner_user_id: null },
      { service: 'mobile_app_watch', name: 'Watch', suggest_critical: false, owner_user_id: 'u-admin' },
      { service: 'mobile_app_car', name: 'Auto', suggest_critical: false, owner_user_id: null },
    ],
  },
};

const voice = { client_id: 'pair:voice-assistant', display_name: 'Sprachassistent' };
const claude = { client_id: 'https://claude.ai/oauth/claude-code-client-metadata', display_name: 'Claude Code' };

export const approvalsOpenFixture: ApprovalRequest[] = [
  {
    id: 'apr-1',
    agent: claude,
    entity_id: 'lock.front_door',
    device_name: 'Haustür',
    area: 'hallway',
    action: 'unlock',
    critical: true,
    reason: HOSTILE_REASON,
    params: [],
    recipients: ['Markus'],
    created_at: '2026-10-02T17:41:30Z',
    expires_at: '2026-10-02T17:43:30Z',
    can_answer: false,
  },
];

export const approvalsHistoryFixture: ApprovalHistoryEntry[] = [
  { seq: 4, agent: voice, entity_id: 'lock.front_door', device_name: 'Haustür', action: 'unlock', outcome: 'timeout', by_name: null, created_at: '2026-10-02T14:43:30Z', answered_at: '2026-10-02T14:45:30Z' },
  { seq: 8, agent: voice, entity_id: 'lock.front_door', device_name: 'Haustür', action: 'unlock', outcome: 'approved', by_name: 'Markus', created_at: '2026-10-02T15:00:00Z', answered_at: '2026-10-02T15:00:42Z' },
  { seq: 9, agent: claude, entity_id: 'cover.garage_door', device_name: 'Garagentor', action: 'open', outcome: 'rejected', by_name: 'Alex', created_at: '2026-10-02T16:00:00Z', answered_at: '2026-10-02T16:00:08Z' },
  { seq: 10, agent: claude, entity_id: 'lock.front_door', device_name: 'Haustür', action: 'unlock', outcome: 'invalid_response', by_name: 'Alex', created_at: '2026-10-02T16:10:00Z', answered_at: '2026-10-02T16:10:05Z' },
  { seq: 11, agent: voice, entity_id: 'lock.front_door', device_name: 'Haustür', action: 'open', outcome: 'emergency_stop', by_name: null, created_at: '2026-10-02T16:29:50Z', answered_at: '2026-10-02T16:30:00Z' },
];

const mandateRef = { id: 'mandate-voice', digest: 'sha256:fixture-0' };
const decision = (
  seq: number,
  recordedAt: string,
  entityId: string,
  category: string,
  area: string | undefined,
  action: string,
  rest: Pick<AuditEntry, 'evaluation' | 'result'> & Partial<AuditEntry>,
  who = voice,
): Omit<AuditEntry, 'digest' | 'prev'> => ({
  id: `0192-${seq}`,
  seq,
  recorded_at: recordedAt,
  event: 'decision',
  actor: { kind: 'agent', id: who.client_id },
  agent: who,
  request: { time: recordedAt, timezone: 'Europe/Berlin', resource: { entity_id: entityId, category, ...(area ? { area } : {}) }, action },
  mandate: mandateRef,
  ...rest,
});

const entries: Omit<AuditEntry, 'digest' | 'prev'>[] = [
  { id: '0192-1', seq: 1, recorded_at: '2026-10-01T08:00:00.000Z', event: 'agent.registered', actor: { kind: 'user', id: 'u-admin', name: 'Markus' }, agent: voice },
  { id: '0192-2', seq: 2, recorded_at: '2026-10-01T08:00:00.000Z', event: 'mandate.created', actor: { kind: 'user', id: 'u-admin', name: 'Markus' }, agent: voice, mandate: mandateRef },
  decision(3, '2026-10-02T14:42:10.120Z', 'light.kitchen', 'light', 'kitchen', 'turn_on', {
    evaluation: { decision: 'allow', reason: 'rule', rule_id: 'lights' },
    result: { status: 'executed', duration_ms: 84 },
  }),
  decision(4, '2026-10-02T14:45:30.000Z', 'lock.front_door', 'lock', 'hallway', 'unlock', {
    evaluation: { decision: 'ask', reason: 'rule', rule_id: 'door', approval_timeout: 'PT2M' },
    approval: { outcome: 'timeout', at: '2026-10-02T14:45:30.000Z' },
    result: { status: 'denied', denied_by: 'approval' },
  }),
  decision(5, '2026-10-02T14:50:02.000Z', 'camera.demo_camera', 'camera', 'garage', 'snapshot', {
    evaluation: { decision: 'deny', reason: 'rule', rule_id: 'no-cameras' },
    result: { status: 'denied', denied_by: 'mandate' },
  }),
  decision(6, '2026-10-02T14:52:00.000Z', 'alarm_control_panel.security', 'alarm', undefined, 'disarm', {
    evaluation: { decision: 'deny', reason: 'no_match', rule_id: null },
    result: { status: 'denied', denied_by: 'mandate' },
  }),
  decision(7, '2026-10-02T15:00:30.000Z', 'light.living_room', 'light', 'living_room', 'set', {
    evaluation: { decision: 'allow', reason: 'rule', rule_id: 'lights' },
    result: { status: 'failed', error: 'Home Assistant: entity unavailable', duration_ms: 1200 },
  }),
  decision(8, '2026-10-02T15:00:42.000Z', 'lock.front_door', 'lock', 'hallway', 'unlock', {
    evaluation: { decision: 'ask', reason: 'rule', rule_id: 'door', approval_timeout: 'PT2M' },
    approval: { outcome: 'approved', by: 'u-admin', by_name: 'Markus', at: '2026-10-02T15:00:42.000Z' },
    result: { status: 'executed', duration_ms: 42_310 },
  }),
  decision(9, '2026-10-02T16:00:08.000Z', 'cover.garage_door', 'gate', 'garage', 'open', {
    evaluation: { decision: 'ask', reason: 'critical_demotion', rule_id: 'gate', approval_timeout: 'PT2M' },
    approval: { outcome: 'rejected', by: 'u-partner', by_name: 'Alex', at: '2026-10-02T16:00:08.000Z' },
    result: { status: 'denied', denied_by: 'approval' },
  }, claude),
  decision(10, '2026-10-02T16:10:05.000Z', 'lock.front_door', 'lock', 'hallway', 'unlock', {
    evaluation: { decision: 'ask', reason: 'rule', rule_id: 'door', approval_timeout: 'PT2M' },
    approval: { outcome: 'invalid_response', by: 'u-partner', by_name: 'Alex', at: '2026-10-02T16:10:05.000Z' },
    result: { status: 'denied', denied_by: 'approval' },
  }, claude),
  decision(11, '2026-10-02T16:30:00.000Z', 'lock.front_door', 'lock', 'hallway', 'open', {
    evaluation: { decision: 'ask', reason: 'rule', rule_id: 'door', approval_timeout: 'PT2M' },
    result: { status: 'denied', denied_by: 'emergency_stop' },
  }),
  { id: '0192-12', seq: 12, recorded_at: '2026-10-02T16:30:00.000Z', event: 'emergency_stop.activated', actor: { kind: 'user', id: 'u-admin', name: 'Markus' } },
  { id: '0192-13', seq: 13, recorded_at: '2026-10-02T16:35:00.000Z', event: 'emergency_stop.released', actor: { kind: 'user', id: 'u-admin', name: 'Markus' } },
  decision(14, '2026-10-02T17:20:00.000Z', 'light.kitchen', 'light', 'kitchen', 'turn_off', {
    evaluation: { decision: 'allow', reason: 'rule', rule_id: 'lights' },
    result: { status: 'denied', denied_by: 'rate_limit' },
  }),
  { id: '0192-15', seq: 15, recorded_at: '2026-10-02T17:30:00.000Z', event: 'auth.rejected', actor: { kind: 'system', id: 'home-mandate' } },
];

/** digestOf is a stand-in digest for fixtures; the server computes real ones (SPEC-v0 section 9). */
export const digestOf = (seq: number) => `sha256:${seq.toString(16).padStart(64, '0')}`;

// The approved request of entry 8 was made 42 s before the answer (approval history).
const approved = entries.find((e) => e.seq === 8);
if (approved?.request) approved.request = { ...approved.request, time: '2026-10-02T15:00:00.000Z' };

export const auditFixture: AuditEntry[] = entries.map((e) => ({
  ...e,
  digest: digestOf(e.seq),
  prev: e.seq === 1 ? null : digestOf(e.seq - 1),
}));
