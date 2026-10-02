// SPDX-License-Identifier: AGPL-3.0-or-later

// A household for the mock client and component tests, shaped like the E2E environment
// (Home Assistant demo integration). Some texts are hostile on purpose: they must render
// escaped and must not break the layout.

import type {
  Agent,
  ApproverList,
  AuditEntry,
  DeviceCatalog,
  MandateDocument,
  MandateDraft,
  Session,
  Template,
} from './types.ts';

export const HOSTILE_NAME = '<img src=x onerror=alert(1)> Agent "quoted" & <b>bold</b>';
export const LONG_NAME = 'Sehr langer Agentenname für den Pseudo-Lokalisierungstest mit Überlänge';

export const sessionFixture: Session = {
  user: { id: 'u-admin', name: 'Markus' },
  csrf_token: 'csrf-fixture-token',
  language: null,
  household: {
    time_zone: 'Europe/Berlin',
    language: 'de',
    unit_system: {
      temperature: '°C',
      length: 'km',
      mass: 'g',
      volume: 'L',
      pressure: 'Pa',
      wind_speed: 'm/s',
    },
  },
};

export const devicesFixture: DeviceCatalog = {
  areas: [
    { id: 'kitchen', name: 'Küche' },
    { id: 'living_room', name: 'Wohnzimmer' },
    { id: 'hallway', name: 'Flur' },
    { id: 'garage', name: 'Garage' },
  ],
  devices: [
    { entity_id: 'light.kitchen', name: 'Küchenlicht', category: 'light', area: 'kitchen', actions: ['read', 'set', 'turn_off', 'turn_on'] },
    { entity_id: 'light.living_room', name: 'Wohnzimmerlicht', category: 'light', area: 'living_room', actions: ['read', 'set', 'turn_off', 'turn_on'] },
    { entity_id: 'lock.front_door', name: 'Haustür', category: 'lock', area: 'hallway', actions: ['lock', 'open', 'read', 'unlock'] },
    { entity_id: 'cover.garage_door', name: 'Garagentor', category: 'gate', area: 'garage', actions: ['close', 'open', 'read'] },
    { entity_id: 'alarm_control_panel.security', name: 'Alarmanlage', category: 'alarm', area: null, actions: ['arm', 'disarm', 'read'] },
    { entity_id: 'camera.demo_camera', name: 'Kamera Einfahrt', category: 'camera', area: 'garage', actions: ['read', 'snapshot'] },
    { entity_id: 'climate.hvac', name: 'Heizung', category: 'climate', area: 'living_room', actions: ['read', 'set_mode', 'set_temperature'] },
    { entity_id: 'media_player.living_room', name: 'Lautsprecher', category: 'media', area: 'living_room', actions: ['pause', 'play', 'read', 'set_volume', 'turn_off', 'turn_on'] },
    { entity_id: 'sensor.outside_temperature', name: 'Außentemperatur', category: 'sensor', area: null, actions: ['read'] },
    { entity_id: 'script.demo', name: HOSTILE_NAME, category: 'script', area: null, actions: ['read', 'run'] },
  ],
};

export const voiceAssistantDraft: MandateDraft = {
  rules: [
    { id: 'lights', resource: { category: 'light' }, actions: ['read', 'turn_on', 'turn_off', 'set'], decision: 'allow' },
    { id: 'climate', resource: { category: 'climate' }, actions: ['read', 'set_temperature'], decision: 'allow' },
    { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'], decision: 'ask' },
    { id: 'night', resource: { category: 'media' }, actions: ['*'], decision: 'allow', conditions: { time_window: '07:00-22:00' } },
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

export const agentsFixture: Agent[] = [
  {
    client_id: 'pair:voice-assistant',
    display_name: 'Sprachassistent',
    status: 'active',
    created_at: '2026-10-01T08:00:00Z',
    created_by: 'u-admin',
    oauth_client: 'voice-assistant',
    client_verified: false,
    mandate: { id: 'mandate-voice', status: 'active' },
  },
  {
    client_id: 'https://claude.ai/oauth/claude-code-client-metadata',
    display_name: 'Claude Code',
    status: 'active',
    created_at: '2026-10-02T09:15:00Z',
    created_by: 'u-admin',
    oauth_client: 'https://claude.ai/oauth/claude-code-client-metadata',
    client_verified: true,
    mandate: { id: 'mandate-claude', status: 'active' },
  },
  {
    client_id: 'pair:old-bot',
    display_name: HOSTILE_NAME,
    status: 'revoked',
    created_at: '2026-09-29T18:00:00Z',
    created_by: 'u-admin',
    oauth_client: 'old-bot',
    client_verified: false,
    mandate: null,
  },
  {
    client_id: 'pair:long',
    display_name: LONG_NAME,
    status: 'active',
    created_at: '2026-10-02T07:00:00Z',
    created_by: 'u-admin',
    oauth_client: 'long',
    client_verified: false,
    mandate: { id: 'mandate-long', status: 'active' },
  },
];

export const templatesFixture: Template[] = [
  { name: 'voice-assistant', draft: voiceAssistantDraft },
  {
    name: 'read-only',
    draft: { ...voiceAssistantDraft, rules: [{ id: 'read', resource: { any: true }, actions: ['read'], decision: 'allow' }] },
  },
];

export const approversFixture: ApproverList = {
  approvers: [{ user_id: 'u-admin', name: 'Markus', notify_service: 'mobile_app_pixel_9', language: null }],
  candidates: {
    people: [
      { user_id: 'u-admin', name: 'Markus' },
      { user_id: 'u-partner', name: 'Alex' },
    ],
    notify_services: ['mobile_app_pixel_9', 'mobile_app_iphone'],
  },
};

const voice = { client_id: 'pair:voice-assistant', display_name: 'Sprachassistent' };
const mandateRef = { id: 'mandate-voice', digest: 'sha256:4f1c' };

export const auditFixture: AuditEntry[] = [
  {
    id: '0192-a',
    seq: 1,
    recorded_at: '2026-10-01T08:00:00.000Z',
    event: 'agent.registered',
    actor: { kind: 'user', id: 'u-admin' },
    agent: voice,
  },
  {
    id: '0192-b',
    seq: 2,
    recorded_at: '2026-10-01T08:00:00.000Z',
    event: 'mandate.created',
    actor: { kind: 'user', id: 'u-admin' },
    agent: voice,
    mandate: mandateRef,
  },
  {
    id: '0192-c',
    seq: 3,
    recorded_at: '2026-10-02T17:42:10.120Z',
    event: 'decision',
    actor: { kind: 'agent', id: voice.client_id },
    agent: voice,
    request: {
      time: '2026-10-02T17:42:10.100Z',
      timezone: 'Europe/Berlin',
      resource: { entity_id: 'light.kitchen', category: 'light', area: 'kitchen' },
      action: 'turn_on',
    },
    mandate: mandateRef,
    evaluation: { decision: 'allow', reason: 'rule', rule_id: 'lights' },
    result: { status: 'executed', duration_ms: 84 },
  },
  {
    id: '0192-d',
    seq: 4,
    recorded_at: '2026-10-02T17:45:31.900Z',
    event: 'decision',
    actor: { kind: 'agent', id: voice.client_id },
    agent: voice,
    request: {
      time: '2026-10-02T17:43:30.000Z',
      timezone: 'Europe/Berlin',
      resource: { entity_id: 'lock.front_door', category: 'lock', area: 'hallway' },
      action: 'unlock',
    },
    mandate: mandateRef,
    evaluation: { decision: 'ask', reason: 'rule', rule_id: 'door', approval_timeout: 'PT2M' },
    approval: { outcome: 'timeout', at: '2026-10-02T17:45:30.000Z' },
    result: { status: 'denied', denied_by: 'approval' },
  },
  {
    id: '0192-e',
    seq: 5,
    recorded_at: '2026-10-02T18:01:02.000Z',
    event: 'decision',
    actor: { kind: 'agent', id: voice.client_id },
    agent: voice,
    request: {
      time: '2026-10-02T18:01:02.000Z',
      timezone: 'Europe/Berlin',
      resource: { entity_id: 'camera.demo_camera', category: 'camera', area: 'garage' },
      action: 'snapshot',
    },
    mandate: mandateRef,
    evaluation: { decision: 'deny', reason: 'rule', rule_id: 'no-cameras' },
    result: { status: 'denied', denied_by: 'mandate' },
  },
  {
    id: '0192-f',
    seq: 6,
    recorded_at: '2026-10-02T18:30:00.000Z',
    event: 'emergency_stop.activated',
    actor: { kind: 'user', id: 'u-admin' },
  },
  {
    id: '0192-g',
    seq: 7,
    recorded_at: '2026-10-02T18:35:00.000Z',
    event: 'emergency_stop.released',
    actor: { kind: 'user', id: 'u-admin' },
  },
];
