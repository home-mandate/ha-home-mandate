// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './client.ts';
import { approvalsOpenFixture, voiceAssistantDraft, WORST_NAME, WORST_REASON } from './fixtures.ts';
import { createMockClient, MOCK_EXPIRED_CODE, MOCK_PAIRING_CODE, MOCK_APPROVERS_VERSION } from './mock.ts';
import type { ApprovalRequest, Rule, ServerEvent } from './types.ts';
import { draftOf } from '../mandate/versions.ts';

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
    expect(approvals.history[0]).toMatchObject({ outcome: 'cancelled', cause: 'revoked', by_name: null });
    expect(types(events)).toEqual(['mandates.changed', 'approval.closed', 'audit.appended', 'audit.appended', 'agents.changed']);
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
    expect(agent).toMatchObject({ display_name: 'Tablet Küche', status: 'active', mandate: { name: 'Tablet Küche', status: 'active' } });
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

describe('createMockClient: removing and reconnecting (#21, #22)', () => {
  const VOICE = 'pair:voice-assistant';

  it('removes only revoked agents, with their mandates when asked, and records each removal', async () => {
    const api = createMockClient();
    await expect(api.removeAgent({ client_id: VOICE })).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.removeAgent({ client_id: 'pair:none' })).rejects.toMatchObject({ code: 'not_found' });
    await api.revokeAgent(VOICE);
    const { events } = listen(api);
    const removed = await api.removeAgent({ client_id: VOICE, mandates: true });
    expect(removed.removed_at).toBe(NOW_ISO);
    expect(removed.removed_by_name).toBe('Markus');
    expect(removed.mandate?.removed_at).toBe(NOW_ISO);
    expect(types(events)).toContain('agents.changed');
    const log = (await api.audit({ limit: 2 })).entries.map((e) => e.event);
    expect(log).toEqual(['agent.removed', 'mandate.removed']);
    const mandate = (await api.audit({ event: 'mandate.removed' })).entries[0]?.mandate;
    expect(mandate?.name).toBe('Sprachassistent Küche');
    // Removed stays listed, marked; removing again records nothing.
    expect((await api.agents()).find((a) => a.client_id === VOICE)?.removed_at).toBe(NOW_ISO);
    await api.removeAgent({ client_id: VOICE, mandates: true });
    expect((await api.audit({ limit: 1 })).entries[0]?.event).toBe('agent.removed');
    expect((await api.audit({ event: 'agent.removed' })).total).toBe(1);
  });

  it('revokes and removes in one step, revocations first', async () => {
    const api = createMockClient();
    const removed = await api.removeAgent({ client_id: VOICE, revoke: true });
    expect(removed.status).toBe('revoked');
    expect(removed.connected).toBe(false);
    expect(removed.mandate?.status).toBe('revoked');
    expect(removed.mandate?.removed_at).toBeNull();
    const log = (await api.audit({ limit: 3 })).entries.map((e) => e.event).toReversed();
    expect(log).toEqual(['agent.revoked', 'mandate.revoked', 'agent.removed']);
  });

  it('removes a revoked mandate and every revoked agent and mandate at once', async () => {
    const api = createMockClient();
    await expect(api.removeMandate('mandate-claude')).rejects.toMatchObject({ code: 'conflict' });
    await api.revokeMandate('mandate-claude');
    expect((await api.removeMandate('mandate-claude')).removed_at).toBe(NOW_ISO);
    await api.revokeMandate('mandate-long');
    expect(await api.removeRevoked()).toEqual({ agents: 1, mandates: 1 });
    expect(await api.removeRevoked()).toEqual({ agents: 0, mandates: 0 });
    const agents = await api.agents();
    expect(agents.filter((a) => a.removed_at !== null).map((a) => a.client_id)).toEqual(['pair:old-bot']);
  });

  it('offers the agents of the same client without tokens after an emergency stop, and reconnects one', async () => {
    const api = createMockClient({ afterStop: true });
    const candidate = await api.pairingCheck(MOCK_PAIRING_CODE);
    expect(candidate.reconnect.map((r) => r.display_name)).toEqual(['Küchen-Tablet']);
    expect(candidate.reconnect[0]?.mandate?.name).toBe('Tablet Küche');
    const target = candidate.reconnect[0]?.client_id ?? '';
    await expect(api.pairingReconnect({ code: MOCK_PAIRING_CODE, pairing_id: 'other', client_id: target })).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.pairingReconnect({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, client_id: VOICE })).rejects.toMatchObject({ code: 'conflict' });
    const before = (await api.agents()).length;
    const agent = await api.pairingReconnect({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, client_id: target });
    expect(agent.client_id).toBe(target);
    expect(agent.connected).toBe(true);
    expect((await api.agents()).length).toBe(before);
    expect((await api.audit({ limit: 1 })).entries[0]?.event).toBe('agent.reconnected');
    await expect(api.pairingCheck(MOCK_PAIRING_CODE)).rejects.toMatchObject({ code: 'pairing_code_expired' });
  });

  it('offers nothing to reconnect while the agents have tokens; the emergency stop withdraws them', async () => {
    const api = createMockClient();
    expect((await api.pairingCheck(MOCK_PAIRING_CODE)).reconnect).toEqual([]);
    expect((await api.agents()).find((a) => a.client_id === VOICE)?.connected).toBe(true);
    await api.setEmergencyStop(true);
    await api.setEmergencyStop(false);
    expect((await api.agents()).every((a) => !a.connected)).toBe(true);
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

  it('keeps where the rules came from and stores nothing for the same template twice (#16)', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const template = await api.template('read-only');
    const first = await api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: summary.digest });
    expect(first.result).toBe('updated');
    expect(first.versions[0]).toMatchObject({ origin: 'template', template: 'read-only', template_digest: template.digest });
    expect(first.summary.rules_from).toMatchObject({ template: 'read-only', template_digest: template.digest, edited_since: false });
    const again = await api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: first.summary.digest });
    expect(again.result).toBe('unchanged');
    expect(again.versions).toHaveLength(first.versions.length);
    // An edit is one, and the rules were edited since the template.
    const draft = { ...again.document, limits: { max_actions_per_hour: 3 } };
    const edited = await api.putMandate('mandate-voice', {
      name: again.summary.name,
      draft: { rules: draft.rules, approval: draft.approval, limits: draft.limits, valid_from: draft.valid_from },
      base_digest: again.summary.digest,
    });
    expect(edited.versions[0]).toMatchObject({ origin: 'edit', template: null, template_digest: null });
    expect(edited.summary.rules_from).toMatchObject({ template: 'read-only', edited_since: true });
    expect((await api.agents()).find((a) => a.client_id === 'pair:voice-assistant')?.mandate?.rules_from).toMatchObject({ edited_since: true });
  });

  it('renames a mandate when applying a template, also when nothing else changes', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    await expect(api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: summary.digest, name: ' ' })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/name',
    });
    const applied = await api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: summary.digest, name: ' Küche ' });
    expect(applied.summary.name).toBe('Küche');
    const renamed = await api.applyTemplate('mandate-voice', { template: 'read-only', base_digest: applied.summary.digest, name: 'Wohnzimmer' });
    expect(renamed).toMatchObject({ result: 'unchanged', summary: { name: 'Wohnzimmer' } });
  });

  it('names a new mandate after its agent (#16)', async () => {
    const api = createMockClient();
    await api.revokeMandate('mandate-voice');
    const created = await api.createMandate({ client_id: 'pair:voice-assistant', template: 'read-only' });
    expect(created.summary.name).toBe('Sprachassistent');
    expect(created.versions[0]).toMatchObject({ origin: 'template', template: 'read-only' });
    const paired = await api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: 'Tablet', template: 'read-only' });
    expect(paired.mandate?.name).toBe('Tablet');
  });

  it('lists the mandates that use a template and applies a changed one to the chosen ones (#18)', async () => {
    const api = createMockClient();
    // The Claude mandate takes its rules from voice-assistant too (after another one), then is edited.
    const claude = await api.mandate('mandate-claude');
    const other = await api.applyTemplate('mandate-claude', { template: 'read-only', base_digest: claude.summary.digest });
    const applied = await api.applyTemplate('mandate-claude', { template: 'voice-assistant', base_digest: other.summary.digest });
    const usage = await api.templateUsage('voice-assistant');
    expect(usage.mandates.map((u) => u.mandate_id).sort()).toEqual(['mandate-claude', 'mandate-voice']);
    expect(usage.mandates.every((u) => u.up_to_date && !u.edited_since)).toBe(true);
    await api.putMandate('mandate-claude', { name: 'Claude Code', draft: { ...draftOf(applied.document), limits: { max_actions_per_hour: 2 } }, base_digest: applied.summary.digest });
    const seen = await api.template('voice-assistant');
    const saved = await api.putTemplate('voice-assistant', { draft: { ...seen.draft, limits: { max_actions_per_hour: 9 } }, base_digest: seen.digest });
    const now = await api.templateUsage('voice-assistant');
    expect(now.digest).toBe(saved.digest);
    expect(now.mandates.find((u) => u.mandate_id === 'mandate-claude')).toMatchObject({ edited_since: true, up_to_date: false });
    expect(now.mandates.find((u) => u.mandate_id === 'mandate-voice')).toMatchObject({ edited_since: false, up_to_date: false });
    const targets = now.mandates.map((u) => ({ mandate_id: u.mandate_id, base_digest: u.digest }));
    const rollout = await api.applyTemplateToMandates('voice-assistant', {
      template_digest: saved.digest,
      targets: [...targets, { mandate_id: 'mandate-none', base_digest: targets[0]?.base_digest ?? '' }],
    });
    expect(rollout.results.map((r) => r.result)).toEqual(['updated', 'updated', 'not_found']);
    expect((await api.mandate('mandate-voice')).summary.max_actions_per_hour).toBe(9);
    // The same again: unchanged; on an old version: conflict.
    const again = await api.applyTemplateToMandates('voice-assistant', { template_digest: saved.digest, targets });
    expect(again.results.map((r) => r.result)).toEqual(['conflict', 'conflict']);
    const fresh = (await api.templateUsage('voice-assistant')).mandates.map((u) => ({ mandate_id: u.mandate_id, base_digest: u.digest }));
    expect((await api.applyTemplateToMandates('voice-assistant', { template_digest: saved.digest, targets: fresh })).results.map((r) => r.result)).toEqual([
      'unchanged',
      'unchanged',
    ]);
    await api.revokeMandate('mandate-claude');
    const claudeTarget = fresh.filter((t) => t.mandate_id === 'mandate-claude');
    expect((await api.applyTemplateToMandates('voice-assistant', { template_digest: saved.digest, targets: claudeTarget })).results[0]?.result).toBe('revoked');
  });

  it('edits a mandate as another administrator would (control for the browser tests)', async () => {
    const api = createMockClient();
    api.control.editMandate('mandate-voice', 4);
    const { summary, versions } = await api.mandate('mandate-voice');
    expect(summary.max_actions_per_hour).toBe(4);
    expect(versions[0]?.origin).toBe('edit');
  });

  it('applies a template with critical rules to mandates only after one confirmation (#18)', async () => {
    const api = createMockClient();
    const seen = await api.template('voice-assistant');
    const saved = await api.putTemplate('voice-assistant', { draft: { ...seen.draft, rules: [...seen.draft.rules, criticalRule] }, base_digest: seen.digest, confirm_critical: true });
    const { mandates } = await api.templateUsage('voice-assistant');
    const request = { template_digest: saved.digest, targets: mandates.map((u) => ({ mandate_id: u.mandate_id, base_digest: u.digest })) };
    await expect(api.applyTemplateToMandates('voice-assistant', request)).rejects.toMatchObject({ code: 'critical_confirmation_required' });
    expect((await api.mandate('mandate-voice')).versions).toHaveLength(1);
    const done = await api.applyTemplateToMandates('voice-assistant', { ...request, confirm_critical: true });
    expect(done.results.map((r) => r.result)).toEqual(['updated']);
  });

  it('refuses bad requests to apply a template to mandates (#18)', async () => {
    const api = createMockClient();
    const { digest, mandates } = await api.templateUsage('voice-assistant');
    const one = mandates.map((u) => ({ mandate_id: u.mandate_id, base_digest: u.digest }));
    const many = Array.from({ length: 101 }, (_, i) => ({ mandate_id: `mandate-${i}`, base_digest: digest }));
    for (const [request, code, field] of [
      [{ template_digest: digest, targets: [] }, 'invalid_input', '/targets'],
      [{ template_digest: digest, targets: many }, 'invalid_input', '/targets'],
      [{ template_digest: digest, targets: [...one, ...one] }, 'invalid_input', '/targets/1/mandate_id'],
      [{ template_digest: digest, targets: [{ mandate_id: 'mandate-voice', base_digest: 'x' }] }, 'invalid_input', '/targets/0/base_digest'],
      [{ template_digest: 'x', targets: one }, 'invalid_input', '/template_digest'],
      [{ template_digest: 'sha256:' + '0'.repeat(64), targets: one }, 'conflict', undefined],
    ] as const) {
      await expect(api.applyTemplateToMandates('voice-assistant', { ...request, targets: [...request.targets] })).rejects.toMatchObject({ code, field });
    }
    await expect(api.applyTemplateToMandates('nope', { template_digest: digest, targets: one })).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.templateUsage('nope')).rejects.toMatchObject({ code: 'not_found' });
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
    expect((await api.agents())[0]?.mandate).toEqual({
      id: created.summary.id,
      name: 'Neu',
      status: 'active',
      max_actions_per_hour: 60,
      digest: created.summary.digest,
      rules_from: { template: 'read-only', template_digest: expect.stringMatching(/^sha256:/), at: '2026-10-03T10:00:00.000Z', edited_since: false },
      removed_at: null,
    });
  });

  it('manages templates with U9 and the version the edit started from, and tells listeners', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    const draft = { ...voiceAssistantDraft, rules: [criticalRule] };
    await expect(api.putTemplate('guest', { draft, base_digest: null })).rejects.toMatchObject({ code: 'critical_confirmation_required' });
    const first = await api.putTemplate('guest', { draft, base_digest: null, confirm_critical: true });
    expect(first.digest).toMatch(/^sha256:[0-9a-f]{64}$/);
    // A new template must not exist yet; a change names the current version.
    await expect(api.putTemplate('guest', { draft, base_digest: null })).rejects.toMatchObject({ code: 'conflict', status: 409 });
    const changed = { ...draft, limits: { max_actions_per_hour: 5 } };
    const second = await api.putTemplate('guest', { draft: changed, base_digest: first.digest });
    expect(second.digest).not.toBe(first.digest);
    await expect(api.putTemplate('guest', { draft, base_digest: first.digest })).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.putTemplate('guest', { draft, base_digest: 'sha256:x' })).rejects.toMatchObject({ code: 'invalid_input', field: '/base_digest' });
    expect((await api.templates()).find((t) => t.name === 'guest')).toMatchObject({ rule_count: 1, created_by_name: 'Markus', builtin: false, digest: second.digest });
    await api.deleteTemplate('guest');
    await expect(api.deleteTemplate('guest')).rejects.toMatchObject({ code: 'not_found' });
    // Every change is an audit entry as well (template.changed).
    expect(types(events)).toEqual(['audit.appended', 'templates.changed', 'audit.appended', 'templates.changed', 'audit.appended', 'templates.changed']);
  });

  it('lists base templates first, with titles, and the own ones by name', async () => {
    const api = createMockClient();
    await api.putTemplate('aaa', { draft: voiceAssistantDraft, base_digest: null });
    const list = await api.templates();
    expect(list.map((t) => t.name)).toEqual(['hm-read-only', 'hm-light-climate', 'hm-voice-cautious', 'aaa', 'empty', 'read-only', 'voice-assistant']);
    expect(list[0]).toMatchObject({ builtin: true, hidden: false, created_at: null, created_by: '', title: { de: 'Nur lesen', en: 'Read only' } });
    expect(list[3]).toMatchObject({ builtin: false, title: {}, description: {} });
    expect(list[0]).not.toHaveProperty('draft');
    const full = await api.template('hm-voice-cautious');
    expect(full.draft.approval.approvers).toEqual(['$approvers']);
    expect(full.draft.rules.find((r) => r.decision === 'ask')?.approval?.approvers).toEqual(['$approvers']);
  });

  it('never changes or removes a base template and keeps "hm-" for them', async () => {
    const api = createMockClient();
    const base = await api.template('hm-read-only');
    await expect(api.putTemplate('hm-read-only', { draft: base.draft, base_digest: base.digest })).rejects.toMatchObject({ code: 'builtin_template', status: 409 });
    await expect(api.deleteTemplate('hm-read-only')).rejects.toMatchObject({ code: 'builtin_template' });
    await expect(api.putTemplate('hm-mine', { draft: base.draft, base_digest: null })).rejects.toMatchObject({ code: 'invalid_input', field: '/name' });
    await expect(api.putTemplate('Gäste', { draft: base.draft, base_digest: null })).rejects.toMatchObject({ code: 'invalid_input', field: '/name' });
    // Saved under a new name, with the placeholder kept.
    const copy = await api.putTemplate('my-read-only', { draft: base.draft, base_digest: null });
    expect(copy).toMatchObject({ name: 'my-read-only', builtin: false, title: {} });
    expect(copy.draft.approval.approvers).toEqual(['$approvers']);
  });

  it('hides base templates only, and refuses hidden ones for mandates', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    await expect(api.setTemplateHidden('read-only', true)).rejects.toMatchObject({ code: 'not_found' });
    await api.setTemplateHidden('hm-read-only', true);
    expect((await api.templates()).find((t) => t.name === 'hm-read-only')?.hidden).toBe(true);
    expect((await api.template('hm-read-only')).hidden).toBe(true);
    const refused = { code: 'invalid_input', field: '/template' };
    await expect(
      api.pairingApprove({ code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: 'Tablet', template: 'hm-read-only' }),
    ).rejects.toMatchObject(refused);
    const { summary } = await api.mandate('mandate-voice');
    await expect(api.applyTemplate('mandate-voice', { template: 'hm-read-only', base_digest: summary.digest })).rejects.toMatchObject(refused);
    await api.revokeMandate('mandate-voice');
    await expect(api.createMandate({ client_id: 'pair:voice-assistant', template: 'hm-read-only' })).rejects.toMatchObject(refused);
    await api.setTemplateHidden('hm-read-only', false);
    await expect(api.createMandate({ client_id: 'pair:voice-assistant', template: 'hm-read-only' })).resolves.toBeTruthy();
    expect(types(events).filter((t) => t === 'templates.changed')).toHaveLength(2);
  });

  it('admits only with the template the human saw (template_digest)', async () => {
    const api = createMockClient();
    const seen = await api.template('voice-assistant');
    await api.putTemplate('voice-assistant', { draft: { ...seen.draft, rules: [] }, base_digest: seen.digest });
    const approve = { code: MOCK_PAIRING_CODE, pairing_id: PAIRING_ID, display_name: 'Tablet', template: 'voice-assistant' };
    await expect(api.pairingApprove({ ...approve, template_digest: seen.digest })).rejects.toMatchObject({ code: 'conflict', status: 409 });
    await expect(api.pairingApprove({ ...approve, template_digest: 'sha256:nope' })).rejects.toMatchObject({ code: 'invalid_input', field: '/template_digest' });
    const now = await api.template('voice-assistant');
    await expect(api.pairingApprove({ ...approve, template_digest: now.digest })).resolves.toMatchObject({ display_name: 'Tablet' });
  });

  it('puts the admitting human and every approver in place of the placeholder', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const applied = await api.applyTemplate('mandate-voice', { template: 'hm-voice-cautious', base_digest: summary.digest });
    expect(applied.document.approval.approvers).toEqual(['u-admin']);
    expect(applied.document.rules.find((r) => r.decision === 'ask')?.approval?.approvers).toEqual(['u-admin']);
    expect(JSON.stringify(applied.document)).not.toContain('$approvers');
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

  it('marks devices as critical, no longer proposes them and tells listeners', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    await api.putDeviceCritical('switch.garden_gate', true);
    await api.putDeviceCritical('switch.cellar_door', false);
    const devices = (await api.devices()).devices;
    expect(devices.find((d) => d.entity_id === 'switch.garden_gate')).toMatchObject({ critical: true, suggest_critical: false });
    expect(devices.find((d) => d.entity_id === 'switch.cellar_door')).toMatchObject({ critical: false });
    // As the server: every change of a mark is an audit entry (directory.changed).
    expect(types(events)).toEqual(['audit.appended', 'devices.changed', 'audit.appended', 'devices.changed']);
    const { entries } = await api.audit({ event: 'directory.changed' });
    expect(entries.map((e) => e.directory?.change)).toEqual(['critical_unmarked', 'critical_marked']);
    await api.putDeviceCritical('switch.garden_gate', false);
    expect((await api.devices()).devices.find((d) => d.entity_id === 'switch.garden_gate')).toMatchObject({ critical: false, suggest_critical: true });
    await expect(api.putDeviceCritical('light.nowhere', true)).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.putDeviceCritical('', true)).rejects.toMatchObject({ field: '/entity_id' });
  });

  it('records template and approver changes in the audit log, as the server', async () => {
    const api = createMockClient();
    const base = await api.template('hm-light-climate');
    const created = await api.putTemplate('garden', { draft: base.draft, base_digest: null });
    await api.putTemplate('garden', { draft: base.draft, base_digest: created.digest }); // unchanged
    await api.deleteTemplate('garden');
    await api.setTemplateHidden('hm-read-only', true);
    await api.setTemplateHidden('hm-read-only', true); // already hidden
    await api.setTemplateHidden('hm-read-only', false);
    const templates = (await api.audit({ event: 'template.changed' })).entries.toReversed();
    expect(templates.map((e) => e.template)).toEqual([
      { change: 'stored', name: 'garden', digest: created.digest },
      { change: 'removed', name: 'garden', digest: created.digest },
      { change: 'hidden', name: 'hm-read-only' },
      { change: 'shown', name: 'hm-read-only' },
    ]);
    const device = (critical: boolean) => ({ devices: [{ service: 'mobile_app_iphone', critical }], ui: false, ui_critical: false, language: null });
    await api.putApprover('u-partner', device(true), (await api.approvers()).version);
    await api.putApprover('u-partner', device(false), (await api.approvers()).version); // other channels: no entry
    await api.deleteApprover('u-partner', (await api.approvers()).version);
    const approvers = (await api.audit({ event: 'approver.changed' })).entries.toReversed();
    expect(approvers.map((e) => e.approver)).toEqual([
      { change: 'added', id: 'u-partner', name: 'Alex' },
      { change: 'removed', id: 'u-partner', name: 'Alex' },
    ]);
    expect(approvers.every((e) => e.actor?.kind === 'user')).toBe(true);
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

  it('gives one agent the worst name everywhere it appears, and the worst reason to its requests', async () => {
    const api = createMockClient({ hostile: true });
    const voice = 'pair:voice-assistant';
    expect((await api.agents()).find((a) => a.client_id === voice)?.display_name).toBe(WORST_NAME);
    const mine = (await api.approvals()).open.filter((r) => r.agent.client_id === voice);
    expect(mine).toEqual([expect.objectContaining({ agent: { client_id: voice, display_name: WORST_NAME }, reason: WORST_REASON, can_answer: true })]);
    expect((await api.audit({})).entries.filter((e) => e.agent?.client_id === voice).every((e) => e.agent?.display_name === WORST_NAME)).toBe(true);
    expect((await api.mandate('mandate-voice')).document.agent.display_name).toBe(WORST_NAME);
    expect((await api.pairingCheck(MOCK_PAIRING_CODE)).claimed_name).toBe(WORST_NAME);
    expect([...WORST_NAME].length).toBeGreaterThan(500);
  });

  it('switches the emergency stop from the test controls, like the API', async () => {
    const api = createMockClient();
    const { events } = listen(api);
    await api.control.setEmergencyStop(true);
    expect((await api.system()).emergency_stop).toMatchObject({ active: true, by_name: 'Markus' });
    expect((await api.approvals()).open).toEqual([]);
    expect(types(events)).toContain('system');
    await api.control.setEmergencyStop(false);
    expect((await api.system()).emergency_stop.active).toBe(false);
  });

  it('manages approvers only from the candidates and updates the system count', async () => {
    const api = createMockClient();
    const list = await api.putApprover('u-partner', { devices: [{ service: 'mobile_app_iphone', critical: true }], ui: false, ui_critical: false, language: 'en' }, MOCK_APPROVERS_VERSION);
    expect(list.approvers.map((a) => a.user_id)).toEqual(['u-admin', 'u-partner']);
    expect((await api.system()).approvers_configured).toBe(2);
    await expect(api.putApprover('u-stranger', { devices: [{ service: 'mobile_app_iphone', critical: true }], ui: false, ui_critical: false, language: null }, list.version)).rejects.toMatchObject({
      field: '/user_id',
    });
    await expect(api.testApprover('u-partner')).resolves.toBeUndefined();
    await api.deleteApprover('u-partner', list.version);
    await expect(api.testApprover('u-partner')).rejects.toMatchObject({ code: 'not_found' });
    expect((await api.system()).approvers_configured).toBe(1);
  });

  it('lets the first change of the approvers win', async () => {
    const api = createMockClient();
    const base = (await api.approvers()).version;
    const update = { devices: [{ service: 'mobile_app_iphone', critical: true }], ui: false, ui_critical: false, language: null };
    const first = await api.putApprover('u-partner', update, base);
    expect(first.version).not.toBe(base);
    await expect(api.putApprover('u-partner', { ...update, language: 'de' }, base)).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.deleteApprover('u-partner', base)).rejects.toMatchObject({ code: 'conflict' });
    await expect(api.deleteApprover('u-partner', 'x')).rejects.toMatchObject({ code: 'invalid_input', field: '/base_version' });
    await expect(api.deleteApprover('u-nobody', first.version)).rejects.toMatchObject({ code: 'not_found' });
    expect((await api.approvers()).approvers.find((a) => a.user_id === 'u-partner')?.language).toBe(null);
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
    const put = api.putApprover(user, { ...update, devices: [...update.devices], language: null }, MOCK_APPROVERS_VERSION);
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
    const list = await api.putApprover('u-admin', { devices: [...devices], ui, ui_critical: uiCritical, language: null }, MOCK_APPROVERS_VERSION);
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
    expect((await api.approvals()).history[0]).toMatchObject({ outcome: 'cancelled', cause: 'emergency_stop' });
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
