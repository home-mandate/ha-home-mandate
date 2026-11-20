// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { AuditEntry } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { isolate } from '../untrusted.ts';
import { v1Text } from './v1.ts';

const at = '2026-11-20T12:00:00.000Z';
const entry = (e: Partial<AuditEntry>): AuditEntry => ({ id: 'x', seq: 9, recorded_at: at, event: 'decision', digest: 'sha256:x', prev: null, ...e });
const jkt = 'NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs';

describe('v1Text: what an entry of audit/v1 says (SPEC-v0 section 9.1)', () => {
  it('names why a sign-in was rejected', () => {
    setLocale('en', { reload: false });
    const rejected = (reason: string) =>
      v1Text(entry({ event: 'auth.rejected', auth: { reason } as AuditEntry['auth'], result: { status: 'denied', denied_by: 'authentication' } }));
    expect(rejected('expired')).toBe('Token or code expired');
    expect(rejected('replay')).toBe('Proof of the key used twice');
    expect(rejected('made_up‮')).toBe('made_up');
    expect(v1Text(entry({ event: 'auth.rejected' }))).toBe('');
  });

  it('names the key an agent was bound to, the tokens withdrawn and why', () => {
    expect(v1Text(entry({ event: 'agent.bound', binding: { method: 'dpop', jkt } }))).toBe('Key NzbLsXh8uDCc…');
    expect(v1Text(entry({ event: 'credentials.withdrawn', credentials: { scope: 'all', cause: 'emergency_stop' } }))).toBe(
      'Tokens of all agents withdrawn: with the emergency stop',
    );
    expect(v1Text(entry({ event: 'credentials.withdrawn', credentials: { scope: 'agent', cause: 'refresh_reuse' } }))).toBe(
      'Tokens of this agent withdrawn: a refresh token was used twice',
    );
  });

  it('shows certificates with their untrusted names cleaned, and the state of monitoring', () => {
    const cert = entry({
      event: 'cert.unexpected',
      certificate: { digest: jkt, status: 'not_yet_valid', names: ['hm.example.org', 'evil‮.example.org'], issuer: 'CN=X' },
    });
    expect(v1Text(cert)).toBe(`${isolate('hm.example.org')}, ${isolate('evil.example.org')}: not valid yet`);
    expect(v1Text(entry({ event: 'cert.acknowledged', certificate: { digest: jkt } }))).toBe('Certificate NzbLsXh8uDCc…');
    expect(v1Text(entry({ event: 'ct.status', ct: { status: 'limited', previous_status: 'ok' } }))).toBe('Only one source answers');
  });

  it('says how a setting changed, was set first or removed', () => {
    const config = (c: NonNullable<AuditEntry['config']>) => v1Text(entry({ event: 'config.changed', config: c }));
    expect(config({ setting: 'timezone', value: 'Europe/Berlin', previous_value: 'Europe/Vienna' })).toBe(`Time zone: ${isolate('Europe/Vienna')} → ${isolate('Europe/Berlin')}`);
    expect(config({ setting: 'trusted_proxy', value: '192.0.2.10' })).toBe(`Trusted proxy: ${isolate('192.0.2.10')}`);
    expect(config({ setting: 'public_origin', previous_value: 'https://hm.example.org' })).toBe(`Public address removed (was ${isolate('https://hm.example.org')})`);
    expect(config({ setting: 'remote_access', value: 'off', previous_value: 'on' })).toBe(`Access from outside: ${isolate('on')} → ${isolate('off')}`);
  });

  it('names the range of an export', () => {
    expect(v1Text(entry({ event: 'log.exported', exported: { from_seq: 1, to_seq: 4 } }))).toBe('Entries 1 to 4');
    expect(v1Text(entry({ event: 'log.exported' }))).toBe('All earlier entries');
  });

  it('names a certificate also without a status, and one acknowledged by its key', () => {
    expect(v1Text(entry({ event: 'cert.unexpected', certificate: { names: ['hm.example.org'] } }))).toBe(isolate('hm.example.org'));
    expect(v1Text(entry({ event: 'cert.acknowledged', certificate: { public_key: jkt } }))).toBe('Certificate NzbLsXh8uDCc…');
  });

  it('does not fail on members of the wrong type', () => {
    const odd = (e: Record<string, unknown>) => v1Text(entry(e as Partial<AuditEntry>));
    expect(odd({ event: 'auth.rejected', auth: { reason: 5 } })).toBe('5');
    expect(odd({ event: 'cert.unexpected', certificate: { status: 'alarm', names: 'x.example' } })).toBe(': valid, not obtained by Home-Mandate');
    expect(odd({ event: 'agent.bound', binding: { method: 'dpop', jkt: null } })).toBe('');
    expect(odd({ event: 'config.changed', config: { setting: 7, value: 'x' } })).toBe(`7: ${isolate('x')}`);
  });

  it('speaks German', () => {
    setLocale('de', { reload: false });
    expect(v1Text(entry({ event: 'ct.status', ct: { status: 'unavailable' } }))).toBe('Keine Quelle antwortet');
    expect(v1Text(entry({ event: 'credentials.withdrawn', credentials: { scope: 'all', cause: 'emergency_stop' } }))).toBe(
      'Tokens aller Agenten zurückgezogen: mit dem Not-Aus',
    );
    expect(v1Text(entry({ event: 'config.changed', config: { setting: 'trusted_proxy', previous_value: '192.0.2.1' } }))).toBe(
      `Vertrauenswürdiger Proxy entfernt (vorher ${isolate('192.0.2.1')})`,
    );
    setLocale('en', { reload: false });
  });
});
