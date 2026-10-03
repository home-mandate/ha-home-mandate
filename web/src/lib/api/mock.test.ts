// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './client.ts';
import { approvalsOpenFixture, voiceAssistantDraft } from './fixtures.ts';
import { createMockClient, MOCK_EXPIRED_CODE, MOCK_PAIRING_CODE } from './mock.ts';
import type { ApprovalRequest, Rule, ServerEvent } from './types.ts';

const criticalRule: Rule = {
  id: 'door-open',
  resource: { entity_id: 'lock.front_door' },
  actions: ['unlock'],
  decision: 'allow',
  allow_critical: true,
};

/** listen returns the events the mock emits from now on. */
function listen(api: ReturnType<typeof createMockClient>) {
  const events: ServerEvent[] = [];
  const states: string[] = [];
  const conn = api.events({ onEvent: (e) => events.push(e), onState: (s) => states.push(s) });
  return { events, states, conn };
}

const PAIRING_ID = 'pg-kitchen-tablet';
const NOW_ISO = '2026-10-02T17:42:00.000Z';
const types = (events: ServerEvent[]) => events.map((e) => e.type);

describe('createMockClient: basics', () => {
  it('returns copies, so callers cannot change its state', async () => {
    const api = createMockClient();
    const agents = await api.agents();
    (agents[0] as { display_name: string }).display_name = 'changed';
    expect((await api.agents())[0]?.display_name).not.toBe('changed');
  });

  it('switches the language of the session', async () => {
    const api = createMockClient();
    expect((await api.setLanguage('en')).language).toBe('en');
    expect((await api.session()).language).toBe('en');
  });

  it('reports the system status with the mock clock', async () => {
    const api = createMockClient({ now: () => new Date('2026-10-03T08:00:00Z') });
    const system = await api.system();
    expect(system).toMatchObject({ mode: 'app', server_time: '2026-10-03T08:00:00.000Z', approvers_configured: 1 });
  });

  it('connects the event stream and stops sending after close', async () => {
    const api = createMockClient();
    const { events, states, conn } = listen(api);
    expect(states).toEqual(['connecting', 'open']);
    conn.close();
    await api.setEmergencyStop(true);
    expect(events).toEqual([]);
  });

  it('can simulate a stream that does not connect', () => {
    const { states, conn } = listen(createMockClient({ eventsState: 'closed' }));
    conn.reconnect();
    expect(states).toEqual(['connecting', 'closed', 'closed']);
  });

  it('simulates failures per method', async () => {
    const api = createMockClient({ failures: { agents: 'unavailable', setEmergencyStop: 'csrf_invalid' } });
    const err = await api.agents().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: 'unavailable', status: 0 });
    await expect(api.setEmergencyStop(true)).rejects.toMatchObject({ code: 'csrf_invalid', status: 403 });
    await expect(api.devices()).resolves.toBeDefined();
  });
});

describe('createMockClient: agents and pairing', () => {
  it('revokes an agent with its mandate, rejects its open requests and tells listeners', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    const agent = await api.revokeAgent('https://claude.ai/oauth/claude-code-client-metadata');
    expect(agent).toMatchObject({ status: 'revoked', mandate: { status: 'revoked' }, revoked_at: NOW_ISO, revoked_by_name: 'Markus' });
    const approvals = await api.approvals();
    expect(approvals.open).toEqual([]);
    // F1: the revocation ends the request; it is not a "rejected" by a person.
    expect(approvals.history[0]).toMatchObject({ outcome: 'revoked', by_name: null });
    expect(types(events)).toEqual(['mandates.changed', 'approval.closed', 'audit.appended', 'agents.changed']);
  });

  it('reports activity from the log: requests on the household day and in the last hour, against the limit', async () => {
    const agents = await createMockClient().agents();
    const voice = agents.find((a) => a.client_id === 'pair:voice-assistant');
    const claude = agents.find((a) => a.client_id.startsWith('https://claude.ai/'));
    expect(voice).toMatchObject({ requests_today: 8, actions_last_hour: 1, mandate: { max_actions_per_hour: 60 } });
    expect(claude).toMatchObject({ requests_today: 2, actions_last_hour: 0 });
    expect(claude?.redirect_uris.length).toBeGreaterThan(0);
    expect(voice?.redirect_uris).toEqual([]);
    expect(agents.find((a) => a.status === 'revoked')).toMatchObject({ revoked_by_name: 'Markus' });
  });

  it('counts the household day, not the UTC day', async () => {
    // 00:30 on 3 October in Berlin is still 2 October in UTC.
    const api = createMockClient({ now: () => new Date('2026-10-02T22:30:00Z') });
    expect((await api.agents()).find((a) => a.client_id === 'pair:voice-assistant')?.requests_today).toBe(0);
  });

  it('answers not_found for unknown identifiers', async () => {
    const api = createMockClient();
    await expect(api.revokeAgent('pair:nobody')).rejects.toMatchObject({ code: 'not_found', status: 404 });
    await expect(api.mandate('nope')).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.template('nope')).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.mandateVersion('mandate-voice', 99)).rejects.toMatchObject({ code: 'not_found' });
  });

  it('shows the agent behind a pairing code, ignoring case, spaces and dash', async () => {
    const api = createMockClient();
    await expect(api.pairingCheck(' bcdf ghjk ')).resolves.toMatchObject({
      claimed_name: 'Küchen-Tablet',
      client_verified: false,
      requested_from: '192.168.1.42',
    });
    await expect(api.pairingCheck(MOCK_EXPIRED_CODE)).rejects.toMatchObject({ code: 'pairing_code_expired', status: 410 });
  });

  it('locks pairing after 5 wrong codes and reports the remaining time', async () => {
    let t = new Date('2026-10-02T17:42:00Z').getTime();
    const api = createMockClient({ now: () => new Date(t) });
    for (let i = 0; i < 4; i++) await expect(api.pairingCheck('XXXX-XXXX')).rejects.toMatchObject({ code: 'pairing_code_invalid' });
    await expect(api.pairingCheck('XXXX-XXXX')).rejects.toMatchObject({ code: 'pairing_locked', retryAfter: 600 });
    t += 60_000;
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).rejects.toMatchObject({ code: 'pairing_locked', retryAfter: 540 });
    t += 540_000;
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).resolves.toBeDefined();
  });

  it('admits the agent with a mandate from the template; the code works once', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    const admitted = await api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: ' Tablet Küche ', template: 'read-only' });
    const agent = (await api.agents()).at(-1);
    expect(admitted).toEqual(agent);
    expect(agent).toMatchObject({ display_name: 'Tablet Küche', status: 'active', mandate: { name: 'read-only', status: 'active' } });
    expect(types(events)).toContain('agents.changed');
    await expect(api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: 'x', template: 'read-only' })).rejects.toMatchObject({
      code: 'pairing_code_expired',
    });
  });

  it('checks name and template before using up the code', async () => {
    const api = createMockClient();
    await expect(api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: '  ', template: 'read-only' })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/display_name',
    });
    await expect(api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: 'x', template: 'nope' })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/template',
    });
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).resolves.toBeDefined();
  });

  it('refuses approve and deny for another request than the one checked', async () => {
    const api = createMockClient();
    await expect(api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: 'pg-other', display_name: 'x', template: 'read-only' })).rejects.toMatchObject({
      code: 'conflict',
    });
    await expect(api.pairingDeny({ code: MOCK_PAIRING_CODE, pairing_id: 'pg-other' })).rejects.toMatchObject({ code: 'conflict' });
    expect((await api.pairingCheck(MOCK_PAIRING_CODE)).pairing_id).toBe(PAIRING_ID);
  });

  it('gives the agent the digest of its current mandate version', async () => {
    const api = createMockClient();
    const voice = (await api.agents()).find((a) => a.client_id === 'pair:voice-assistant');
    expect(voice?.mandate?.digest).toBe((await api.mandate('mandate-voice')).summary.digest);
  });

  it('declines a pairing, after which the code is gone', async () => {
    const api = createMockClient();
    await api.pairingDeny({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID });
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).rejects.toMatchObject({ code: 'pairing_code_expired' });
  });
});

describe('createMockClient: mandates and templates', () => {
  it('stores a version with author, keeps the old one and renames', async () => {
    const api = createMockClient();
    const before = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, limits: { max_actions_per_hour: 10 } };
    const after = await api.putMandate('mandate-voice', { name: 'Neu', draft, base_digest: before.summary.digest });
    expect(after.summary).toMatchObject({ name: 'Neu', max_actions_per_hour: 10, rule_count: 5, expires: null });
    expect(after.versions).toHaveLength(2);
    expect(after.versions.map((v) => v.number)).toEqual([2, 1]);
    expect(after.versions[0]).toMatchObject({ created_by: 'u-admin', created_by_name: 'Markus' });
    expect((await api.mandateVersion('mandate-voice', 1)).limits.max_actions_per_hour).toBe(60);
    expect((await api.agents())[0]?.mandate?.name).toBe('Neu');
  });

  it('renames without a new version when the draft is unchanged', async () => {
    const api = createMockClient();
    const { summary, document } = await api.mandate('mandate-voice');
    const { rules, approval, limits, valid_from } = document;
    const after = await api.putMandate('mandate-voice', { name: 'Umbenannt', draft: { rules, approval, limits, valid_from }, base_digest: summary.digest });
    expect(after.versions).toHaveLength(1);
    expect(after.summary.name).toBe('Umbenannt');
    // Key order does not make a new version either (the server compares digests).
    const reordered = { valid_from, limits, approval, rules: rules.map((r) => ({ decision: r.decision, actions: r.actions, resource: r.resource, id: r.id, ...(r.conditions ? { conditions: r.conditions } : {}) })) };
    const again = await api.putMandate('mandate-voice', { name: 'Umbenannt', draft: reordered, base_digest: summary.digest });
    expect(again.versions).toHaveLength(1);
  });

  it('rejects an empty or long name', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    for (const name of [' ', 'x'.repeat(81)]) {
      await expect(api.putMandate('mandate-voice', { name, draft: voiceAssistantDraft, base_digest: summary.digest })).rejects.toMatchObject({
        code: 'invalid_input',
        field: '/name',
      });
    }
  });

  it('refuses an edit based on an outdated version, and edits of revoked mandates', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, rules: [] };
    await api.putMandate('mandate-voice', { name: 'a', draft, base_digest: summary.digest });
    await expect(api.putMandate('mandate-voice', { name: 'a', draft, base_digest: summary.digest })).rejects.toMatchObject({
      code: 'conflict',
      status: 409,
    });
    const fresh = (await api.mandate('mandate-voice')).summary.digest;
    await api.revokeMandate('mandate-voice');
    await expect(api.putMandate('mandate-voice', { name: 'a', draft: voiceAssistantDraft, base_digest: fresh })).rejects.toMatchObject({
      code: 'conflict',
    });
  });

  it('requires confirm_critical for a new allow_critical grant (U9)', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, rules: [...voiceAssistantDraft.rules, criticalRule] };
    await expect(api.putMandate('mandate-voice', { name: 'a', draft, base_digest: summary.digest })).rejects.toMatchObject({
      code: 'critical_confirmation_required',
      status: 422,
    });
    await expect(
      api.putMandate('mandate-voice', { name: 'a', draft, base_digest: summary.digest, confirm_critical: true }),
    ).resolves.toBeDefined();
  });

  it('rejects invalid drafts with a pointer under /draft', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, rules: [{ ...criticalRule, actions: [] }] };
    await expect(api.putMandate('mandate-voice', { name: 'a', draft, base_digest: summary.digest, confirm_critical: true })).rejects.toMatchObject({
      code: 'invalid_mandate',
      field: '/draft/rules/0/actions',
    });
  });

  it('applies a template as a new version, keeping dates and name (D3)', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const after = await api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: summary.digest });
    expect(after.document.rules.map((r) => r.id)).toEqual(['read']);
    expect(after.summary.name).toBe(summary.name);
    expect(after.versions).toHaveLength(2);
    await expect(api.applyTemplate('mandate-voice', { template: 'nope', base_digest: after.summary.digest })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/template',
    });
  });

  it('creates a mandate from a template only for an active agent without an active mandate', async () => {
    const api = createMockClient({ now: () => new Date('2026-10-03T10:00:00Z') });
    await expect(api.createMandate({ client_id: 'pair:voice-assistant', template: 'read-only' })).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.createMandate({ client_id: 'pair:old-bot', template: 'read-only' })).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.createMandate({ client_id: 'pair:nobody', template: 'read-only' })).rejects.toMatchObject({ code: 'not_found' });
    await api.revokeMandate('mandate-voice');
    const created = await api.createMandate({ client_id: 'pair:voice-assistant', template: 'read-only', name: 'Neu' });
    expect(created.summary).toMatchObject({ name: 'Neu', client_id: 'pair:voice-assistant', status: 'active' });
    expect(created.document.created_at).toBe('2026-10-03T10:00:00.000Z');
    expect((await api.agents())[0]?.mandate).toEqual({ id: created.summary.id, name: 'Neu', status: 'active', max_actions_per_hour: 60, digest: created.summary.digest });
  });

  it('manages templates with U9 and tells listeners', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    const draft = { ...voiceAssistantDraft, rules: [criticalRule] };
    await expect(api.putTemplate('guest', { draft })).rejects.toMatchObject({ code: 'critical_confirmation_required' });
    await api.putTemplate('guest', { draft, confirm_critical: true });
    await expect(api.putTemplate('guest', { draft })).resolves.toMatchObject({ name: 'guest' });
    expect((await api.templates()).find((t) => t.name === 'guest')).toMatchObject({ rule_count: 1, created_by_name: 'Markus' });
    await api.deleteTemplate('guest');
    await expect(api.deleteTemplate('guest')).rejects.toMatchObject({ code: 'not_found' });
    expect(types(events)).toEqual(['templates.changed', 'templates.changed', 'templates.changed']);
  });
});

describe('createMockClient: approvals, settings, audit, approvers, emergency stop', () => {
  it('opens and closes approval requests live', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    const request = { ...approvalsOpenFixture[0]!, id: 'apr-2' };
    api.control.openApproval(request);
    expect((await api.approvals()).open.map((r) => r.id)).toEqual(['apr-1', 'apr-2']);
    api.control.closeApproval('apr-2', 'approved', 'Markus');
    api.control.closeApproval('apr-unknown', 'approved', null);
    const { open, history } = await api.approvals();
    expect(open.map((r) => r.id)).toEqual(['apr-1']);
    expect(history[0]).toMatchObject({ outcome: 'approved', by_name: 'Markus' });
    expect(types(events)).toEqual(['approval.opened', 'approval.closed']);
  });

  it('validates and stores the defaults', async () => {
    const api = createMockClient();
    await expect(api.putSettings({ approval_timeout: 'PT5S', max_actions_per_hour: 60, bell: false })).rejects.toMatchObject({ field: '/approval_timeout' });
    await expect(api.putSettings({ approval_timeout: 'PT2M', max_actions_per_hour: 0, bell: false })).rejects.toMatchObject({ field: '/max_actions_per_hour' });
    await api.putSettings({ approval_timeout: 'PT5M', max_actions_per_hour: 30, bell: true });
    expect(await api.settings()).toEqual({ approval_timeout: 'PT5M', max_actions_per_hour: 30, bell: true });
  });

  it('pages through the audit log newest first with a total', async () => {
    const api = createMockClient();
    const first = await api.audit({ limit: 3 });
    expect(first.entries.map((e) => e.seq)).toEqual([15, 14, 13]);
    expect(first).toMatchObject({ next_before: 13, total: 15 });
    const last = await api.audit({ before: 3 });
    expect(last.entries.map((e) => e.seq)).toEqual([2, 1]);
    expect(last.next_before).toBeNull();
    expect(last.total).toBe(15); // all matches of the filter, not only those after the cursor
    expect(last.entries[1]).toMatchObject({ prev: null });
  });

  it.each([
    [{ decisions: ['deny' as const] }, [5]],
    [{ decisions: ['default' as const] }, [6]],
    [{ decisions: ['allow' as const, 'ask' as const] }, [14, 11, 10, 9, 8, 7, 4, 3]],
    [{ group: 'admin' as const }, [15, 13, 12, 2, 1]],
    [{ device: 'garage' }, [9, 5]],
    [{ device: 'lock.front_door' }, [11, 10, 8, 4]],
    [{ since: '2026-10-02T16:00:00Z', until: '2026-10-02T16:31:00Z', group: 'decision' as const }, [11, 10, 9]],
    [{ agent: 'https://claude.ai/oauth/claude-code-client-metadata' }, [10, 9]],
    [{ event: 'emergency_stop.activated' as const }, [12]],
    [{ since: '2026-10-02T18:00:00+02:00', until: '2026-10-02T18:31:00+02:00', group: 'decision' as const }, [11, 10, 9]],
  ])('filters the audit log by %j', async (query, seqs) => {
    const page = await createMockClient().audit(query);
    expect(page.entries.map((e) => e.seq)).toEqual(seqs);
    expect(page.total).toBe(seqs.length);
  });

  it.each([
    ['licht', [14, 7, 3]], // two device names
    ['HAUSTÜR', [11, 10, 8, 4]], // case-insensitive, with umlaut
    ['Wohnzimmer', [7]], // area name
    ['garage', [9, 5]], // area id and entity_id
    ['front_door', [11, 10, 8, 4]], // entity_id
    ['claude code', [10, 9]], // agent name
    ['claude-code-client', [10, 9]], // client_id
    ['  Haus\u202Etür ', [11, 10, 8, 4]], // cleaned before matching
    ['nirgendwo', []],
  ])('searches the audit log for %j', async (q, seqs) => {
    const page = await createMockClient().audit({ q });
    expect(page.entries.map((e) => e.seq)).toEqual(seqs);
    expect(page.total).toBe(seqs.length);
  });

  it('combines the search with the other filters, and ignores an empty one', async () => {
    const api = createMockClient();
    expect((await api.audit({ q: 'tür', agent: 'https://claude.ai/oauth/claude-code-client-metadata' })).entries.map((e) => e.seq)).toEqual([10]);
    expect((await api.audit({ q: 'licht', device: 'lock.front_door' })).total).toBe(0);
    expect((await api.audit({ q: ' \u200B ' })).total).toBe(15);
  });

  it('rejects a search text longer than 100 characters', async () => {
    await expect(createMockClient().audit({ q: 'x'.repeat(101) })).rejects.toMatchObject({ code: 'invalid_input', field: '/q' });
  });

  it('rejects an audit limit outside 1–100', async () => {
    const api = createMockClient();
    await expect(api.audit({ limit: 0 })).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.audit({ limit: 101 })).rejects.toMatchObject({ code: 'invalid_input' });
  });

  it('verifies the chain and reports a broken one', async () => {
    const api = createMockClient();
    expect(await api.verifyAudit()).toMatchObject({ valid: true, broken_at_seq: null, checked: 15 });
    api.control.breakChain(7);
    expect((await api.system()).chain).toMatchObject({ valid: false, broken_at_seq: 7 });
  });

  it('starts an empty household: no agents, mandates, log entries or requests', async () => {
    const api = createMockClient({ empty: true });
    expect(await api.agents()).toEqual([]);
    expect(await api.mandates()).toEqual([]);
    expect(await api.approvals()).toEqual({ open: [], history: [] });
    expect((await api.audit({})).entries).toEqual([]);
    expect((await api.templates()).length).toBeGreaterThan(0);
  });

  it('switches the emergency stop from the test controls, like the API', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    api.control.setEmergencyStop(true);
    expect((await api.system()).emergency_stop).toMatchObject({ active: true, by_name: 'Markus' });
    expect((await api.approvals()).open).toEqual([]);
    expect(types(events)).toContain('system');
    api.control.setEmergencyStop(false);
    expect((await api.system()).emergency_stop.active).toBe(false);
  });

  it('manages approvers only from the candidates and updates the system count', async () => {
    const api = createMockClient();
    const list = await api.putApprover('u-partner', { devices: [{ service: 'mobile_app_iphone', critical: true }], ui: false, ui_critical: false, language: 'en' });
    expect(list.approvers.map((a) => a.user_id)).toEqual(['u-admin', 'u-partner']);
    expect((await api.system()).approvers_configured).toBe(2);
    await expect(api.putApprover('u-stranger', { devices: [{ service: 'mobile_app_iphone', critical: true }], ui: false, ui_critical: false, language: null })).rejects.toMatchObject({
      field: '/user_id',
    });
    await expect(api.testApprover('u-partner')).resolves.toBeUndefined();
    await api.deleteApprover('u-partner');
    await expect(api.testApprover('u-partner')).rejects.toMatchObject({ code: 'not_found' });
    expect((await api.system()).approvers_configured).toBe(1);
  });

  const dev = (service: string, critical = true) => ({ service, critical });
  const six = ['mobile_app_pixel_9', 'mobile_app_iphone', 'mobile_app_macbook', 'mobile_app_tablet', 'mobile_app_watch', 'mobile_app_car'];

  // F2 "Speichern": devices 0/1/5/6, duplicate, pattern, unknown device, UI only for admins,
  // critical in the UI only with the UI, at least one channel.
  it.each([
    ['one phone', 'u-partner', { devices: [dev('mobile_app_iphone')], ui: false, ui_critical: false }, null],
    ['five devices', 'u-admin', { devices: six.slice(0, 5).map((s) => dev(s)), ui: false, ui_critical: false }, null],
    ['six devices', 'u-admin', { devices: six.map((s) => dev(s)), ui: false, ui_critical: false }, '/devices'],
    ['a duplicate', 'u-admin', { devices: [dev('mobile_app_iphone'), dev('mobile_app_iphone', false)], ui: false, ui_critical: false }, '/devices'],
    ['a malformed service', 'u-admin', { devices: [dev('persistent_notification.x')], ui: false, ui_critical: false }, '/devices'],
    ['an unknown device', 'u-admin', { devices: [dev('mobile_app_unknown')], ui: false, ui_critical: false }, '/devices'],
    ['no channel', 'u-admin', { devices: [], ui: false, ui_critical: false }, '/devices'],
    ['only the UI, admin', 'u-admin', { devices: [], ui: true, ui_critical: false }, null],
    ['UI and critical, admin', 'u-admin', { devices: [], ui: true, ui_critical: true }, null],
    ['critical in the UI without the UI', 'u-admin', { devices: [dev('mobile_app_iphone')], ui: false, ui_critical: true }, '/ui_critical'],
    ['the UI for someone who is no admin', 'u-partner', { devices: [dev('mobile_app_iphone')], ui: true, ui_critical: false }, '/ui'],
  ] as const)('saves an approver with %s, or names the field', async (_, user, update, field) => {
    const api = createMockClient();
    const put = api.putApprover(user, { ...update, devices: [...update.devices], language: null });
    if (field === null) await expect(put).resolves.toBeDefined();
    else await expect(put).rejects.toMatchObject({ code: 'invalid_input', field });
  });

  // F2 "Erreichbarkeit", as the server reports it: devices always; the UI only for an
  // admin, for critical requests only with ui_critical; critical on a device only with its switch.
  it.each([
    [[dev('mobile_app_pixel_9')], false, false, { normal: 'push', critical: 'push' }],
    [[dev('mobile_app_pixel_9')], true, true, { normal: 'push', critical: 'push' }],
    [[dev('mobile_app_macbook', false)], false, false, { normal: 'push', critical: 'none' }],
    [[dev('mobile_app_macbook', false)], true, false, { normal: 'push', critical: 'none' }],
    [[dev('mobile_app_macbook', false)], true, true, { normal: 'push', critical: 'ui' }],
    [[], true, false, { normal: 'ui', critical: 'none' }],
    [[], true, true, { normal: 'ui', critical: 'ui' }],
  ] as const)('reports how %j (ui %s, critical %s) reaches the admin', async (devices, ui, uiCritical, reach) => {
    const api = createMockClient();
    const list = await api.putApprover('u-admin', { devices: [...devices], ui, ui_critical: uiCritical, language: null });
    expect(list.approvers.find((a) => a.user_id === 'u-admin')?.reach).toEqual(reach);
  });

  it('offers devices with a name and a suggestion for critical requests, and says who is an admin', async () => {
    const { candidates } = await createMockClient().approvers();
    expect(candidates.devices.find((d) => d.service === 'mobile_app_macbook')).toMatchObject({ suggest_critical: false });
    // Decision S11: only iOS gets the suggestion; Android (Pixel) and the Mac do not.
    expect(candidates.devices.find((d) => d.service === 'mobile_app_pixel_9')).toMatchObject({ name: 'Pixel 9', suggest_critical: false });
    expect(candidates.devices.find((d) => d.service === 'mobile_app_iphone')).toMatchObject({ suggest_critical: true, owner_user_id: 'u-partner' });
    expect(candidates.people.map((p) => [p.user_id, p.is_admin])).toEqual([
      ['u-admin', true],
      ['u-partner', false],
    ]);
  });

  it('switches the emergency stop, ends open requests and logs only real changes', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    expect(await api.setEmergencyStop(true)).toEqual({ active: true, since: '2026-10-02T17:42:00.000Z', by_name: 'Markus' });
    expect(await api.setEmergencyStop(true)).toMatchObject({ active: true });
    expect((await api.approvals()).history[0]).toMatchObject({ outcome: 'emergency_stop' });
    expect(types(events)).toEqual(['approval.closed', 'system', 'audit.appended']);
    expect(await api.setEmergencyStop(false)).toEqual({ active: false, since: null, by_name: null });
  });

  it('reports HA going down', async () => {
    const api = createMockClient();
    api.control.setHaConnected(false);
    expect((await api.system()).ha.connected).toBe(false);
  });
});

describe('createMockClient: answering approvals in the UI (F2)', () => {
  it('closes a request the person may answer, as an answer in the UI with their name', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    api.control.openApproval({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-ui', can_answer: true });
    const entry = await api.answerApproval('apr-ui', true);
    expect(entry).toMatchObject({ outcome: 'approved', by_name: 'Markus', via: 'ui' });
    const approvals = await api.approvals();
    expect(approvals.open.map((r) => r.id)).toEqual(['apr-1']);
    expect(approvals.history[0]).toMatchObject({ outcome: 'approved', via: 'ui' });
    expect(types(events)).toContain('approval.closed');
    expect(await api.answerApproval('apr-1', true).catch((e: unknown) => e)).toMatchObject({ code: 'not_found' });
  });

  it('answers not_found alike for unknown, answered and not answerable requests', async () => {
    const api = createMockClient();
    api.control.openApproval({ ...(approvalsOpenFixture[0] as ApprovalRequest), id: 'apr-ui', can_answer: true });
    await api.answerApproval('apr-ui', false);
    for (const id of ['nope', 'apr-ui', 'apr-1']) {
      await expect(api.answerApproval(id, true)).rejects.toMatchObject({ code: 'not_found', status: 404 });
    }
    expect((await api.approvals()).open.map((r) => r.id)).toEqual(['apr-1']);
  });
});
