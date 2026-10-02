// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ApiError } from './client.ts';
import { voiceAssistantDraft } from './fixtures.ts';
import { createMockClient } from './mock.ts';
import type { Rule } from './types.ts';

const criticalRule: Rule = {
  id: 'door',
  resource: { entity_id: 'lock.front_door' },
  actions: ['unlock'],
  decision: 'allow',
  allow_critical: true,
};

describe('createMockClient', () => {
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

  it('revokes an agent together with its mandate and logs it', async () => {
    const api = createMockClient();
    const agent = await api.revokeAgent('pair:voice-assistant');
    expect(agent).toMatchObject({ status: 'revoked', mandate: { status: 'revoked' } });
    expect((await api.mandate('mandate-voice')).summary.status).toBe('revoked');
    expect((await api.audit({ limit: 1 })).entries[0]).toMatchObject({ event: 'agent.revoked' });
  });

  it('answers not_found for unknown identifiers', async () => {
    const api = createMockClient();
    await expect(api.revokeAgent('pair:nobody')).rejects.toMatchObject({ code: 'not_found', status: 404 });
    await expect(api.mandate('nope')).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.template('nope')).rejects.toMatchObject({ code: 'not_found' });
    await expect(api.mandateVersion('mandate-voice', 'sha256:none')).rejects.toMatchObject({ code: 'not_found' });
  });

  it('stores a mandate version and keeps the old one', async () => {
    const api = createMockClient();
    const before = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, limits: { max_actions_per_hour: 10 } };
    const after = await api.putMandate('mandate-voice', { draft, base_digest: before.summary.digest });
    expect(after.summary.max_actions_per_hour).toBe(10);
    expect(after.versions).toHaveLength(before.versions.length + 1);
    expect(after.versions[0]?.created_by).toBe('u-admin');
    expect((await api.mandateVersion('mandate-voice', before.summary.digest)).limits.max_actions_per_hour).toBe(60);
    expect((await api.audit({ limit: 1 })).entries[0]).toMatchObject({ event: 'mandate.updated' });
  });

  it('refuses an edit based on an outdated version', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    await api.putMandate('mandate-voice', { draft: voiceAssistantDraft, base_digest: summary.digest });
    await expect(
      api.putMandate('mandate-voice', { draft: voiceAssistantDraft, base_digest: summary.digest }),
    ).rejects.toMatchObject({ code: 'conflict', status: 409 });
  });

  it('requires confirm_critical for a new allow_critical grant (U9)', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const draft = { ...voiceAssistantDraft, rules: [...voiceAssistantDraft.rules, { ...criticalRule, id: 'door-open' }] };
    await expect(api.putMandate('mandate-voice', { draft, base_digest: summary.digest })).rejects.toMatchObject({
      code: 'critical_confirmation_required',
      status: 422,
    });
    await expect(
      api.putMandate('mandate-voice', { draft, base_digest: summary.digest, confirm_critical: true }),
    ).resolves.toBeDefined();
  });

  it('applies U9 to templates too', async () => {
    const api = createMockClient();
    const draft = { ...voiceAssistantDraft, rules: [criticalRule] };
    await expect(api.putTemplate('new', { draft })).rejects.toMatchObject({ code: 'critical_confirmation_required' });
    await api.putTemplate('new', { draft, confirm_critical: true });
    // Saving the same grant again needs no new confirmation.
    await expect(api.putTemplate('new', { draft })).resolves.toMatchObject({ name: 'new' });
  });

  it('rejects drafts the schema would reject, with a JSON pointer', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    const dup = { ...voiceAssistantDraft, rules: [criticalRule, { ...criticalRule, allow_critical: undefined }] };
    const empty = { ...voiceAssistantDraft, rules: [{ ...criticalRule, actions: [] }] };
    const badLimit = { ...voiceAssistantDraft, limits: { max_actions_per_hour: 0 } };
    const critOnAsk: Rule = { ...criticalRule, decision: 'ask' };
    const asked = { ...voiceAssistantDraft, rules: [critOnAsk] };
    for (const [draft, field] of [
      [dup, '/draft/rules/1/id'],
      [empty, '/draft/rules/0/actions'],
      [badLimit, '/draft/limits/max_actions_per_hour'],
      [asked, '/draft/rules/0/allow_critical'],
    ] as const) {
      await expect(
        api.putMandate('mandate-voice', { draft, base_digest: summary.digest, confirm_critical: true }),
      ).rejects.toMatchObject({ code: 'invalid_mandate', field });
    }
  });

  it('revokes a mandate and shows that at the agent', async () => {
    const api = createMockClient();
    expect((await api.revokeMandate('mandate-voice')).status).toBe('revoked');
    const agent = (await api.agents()).find((a) => a.client_id === 'pair:voice-assistant');
    expect(agent).toMatchObject({ status: 'active', mandate: { id: 'mandate-voice', status: 'revoked' } });
  });

  it('refuses to edit a revoked mandate', async () => {
    const api = createMockClient();
    const { summary } = await api.mandate('mandate-voice');
    await api.revokeMandate('mandate-voice');
    await expect(
      api.putMandate('mandate-voice', { draft: voiceAssistantDraft, base_digest: summary.digest }),
    ).rejects.toMatchObject({ code: 'conflict' });
  });

  it('creates a new mandate from a template for an agent whose mandate was revoked', async () => {
    const api = createMockClient({ now: () => new Date('2026-10-03T10:00:00Z') });
    await expect(api.createMandate({ client_id: 'pair:voice-assistant', template: 'read-only' })).rejects.toMatchObject({
      code: 'conflict',
    });
    await api.revokeMandate('mandate-voice');
    const created = await api.createMandate({ client_id: 'pair:voice-assistant', template: 'read-only' });
    expect(created.summary).toMatchObject({ client_id: 'pair:voice-assistant', status: 'active' });
    expect(created.summary.id).not.toBe('mandate-voice');
    expect(created.document.rules.map((r) => r.id)).toEqual(['read']);
    expect(created.document.created_at).toBe('2026-10-03T10:00:00.000Z');
    const agent = (await api.agents()).find((a) => a.client_id === 'pair:voice-assistant');
    expect(agent?.mandate).toEqual({ id: created.summary.id, status: 'active' });
    expect((await api.audit({ limit: 1 })).entries[0]).toMatchObject({ event: 'mandate.created' });
  });

  it('creates mandates only for active agents and known templates', async () => {
    const api = createMockClient();
    await expect(api.createMandate({ client_id: 'pair:old-bot', template: 'read-only' })).rejects.toMatchObject({
      code: 'conflict',
    });
    await expect(api.createMandate({ client_id: 'pair:nobody', template: 'read-only' })).rejects.toMatchObject({
      code: 'not_found',
    });
    await api.revokeMandate('mandate-voice');
    await expect(api.createMandate({ client_id: 'pair:voice-assistant', template: 'nope' })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/template',
    });
  });

  it('rejects a preview with an invalid reference time', async () => {
    const api = createMockClient();
    await expect(api.preview({ draft: voiceAssistantDraft, at: 'tomorrow' })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/at',
    });
  });

  it('previews against the active version', async () => {
    const api = createMockClient();
    const draft = { ...voiceAssistantDraft, rules: [] };
    const preview = await api.preview({ draft, mandate_id: 'mandate-voice', at: '2026-10-02T17:00:00Z' });
    const kitchen = preview.entries.find((e) => e.entity_id === 'light.kitchen' && e.action === 'turn_on');
    expect(kitchen).toMatchObject({ decision: 'deny', previous: 'allow' });
    expect(preview.time_zone).toBe('Europe/Berlin');
  });

  it('manages templates', async () => {
    const api = createMockClient();
    await api.putTemplate('guest', { draft: voiceAssistantDraft });
    expect((await api.templates()).map((t) => t.name)).toContain('guest');
    await api.deleteTemplate('guest');
    expect((await api.templates()).map((t) => t.name)).not.toContain('guest');
    await expect(api.deleteTemplate('guest')).rejects.toMatchObject({ code: 'not_found' });
  });

  it('pages through the audit log newest first and filters', async () => {
    const api = createMockClient();
    const first = await api.audit({ limit: 3 });
    expect(first.entries.map((e) => e.seq)).toEqual([7, 6, 5]);
    expect(first.next_before).toBe(5);
    const last = await api.audit({ before: 3 });
    expect(last.entries.map((e) => e.seq)).toEqual([2, 1]);
    expect(last.next_before).toBeNull();
    const denied = await api.audit({ decision: 'deny' });
    expect(denied.entries.map((e) => e.seq)).toEqual([5]);
    expect((await api.audit({ event: 'emergency_stop.activated' })).entries).toHaveLength(1);
    expect((await api.audit({ agent: 'pair:voice-assistant' })).entries).toHaveLength(5);
    expect((await api.audit({ entity_id: 'lock.front_door' })).entries.map((e) => e.seq)).toEqual([4]);
  });

  it('rejects an audit limit outside 1–100', async () => {
    const api = createMockClient();
    await expect(api.audit({ limit: 0 })).rejects.toMatchObject({ code: 'invalid_input' });
    await expect(api.audit({ limit: 101 })).rejects.toMatchObject({ code: 'invalid_input' });
  });

  it('verifies the audit chain', async () => {
    expect(await createMockClient().verifyAudit()).toEqual({ valid: true, broken_at_seq: null, checked: 7 });
  });

  it('manages approvers only from the candidates', async () => {
    const api = createMockClient();
    const list = await api.putApprover('u-partner', { notify_service: 'mobile_app_iphone', language: 'en' });
    expect(list.approvers.map((a) => a.user_id)).toEqual(['u-admin', 'u-partner']);
    await expect(api.putApprover('u-stranger', { notify_service: 'mobile_app_iphone', language: null })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/user_id',
    });
    await expect(api.putApprover('u-partner', { notify_service: 'persistent_notification', language: null })).rejects.toMatchObject({
      code: 'invalid_input',
      field: '/notify_service',
    });
    await expect(api.testApprover('u-partner')).resolves.toBeUndefined();
    await api.deleteApprover('u-partner');
    await expect(api.testApprover('u-partner')).rejects.toMatchObject({ code: 'not_found' });
    expect((await api.approvers()).approvers).toHaveLength(1);
  });

  it('switches the emergency stop and logs only real changes', async () => {
    const api = createMockClient();
    expect(await api.setEmergencyStop(true)).toEqual({ active: true });
    expect(await api.setEmergencyStop(true)).toEqual({ active: true });
    expect(await api.emergencyStop()).toEqual({ active: true });
    const { entries } = await api.audit({ event: 'emergency_stop.activated' });
    expect(entries).toHaveLength(2); // one from the fixtures, one now
  });

  it('simulates failures per method', async () => {
    const api = createMockClient({ failures: { agents: 'unavailable', setEmergencyStop: 'csrf_invalid' } });
    const err = await api.agents().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ code: 'unavailable', status: 0 });
    await expect(api.setEmergencyStop(true)).rejects.toMatchObject({ code: 'csrf_invalid', status: 403 });
    await expect(api.devices()).resolves.toBeDefined();
  });

  it('serves the pairing link and the catalog', async () => {
    const api = createMockClient();
    expect((await api.pairing()).url).toMatch(/^https:\/\//);
    expect((await api.devices()).devices.length).toBeGreaterThan(0);
    expect((await api.mandates()).length).toBe(3);
  });
});
