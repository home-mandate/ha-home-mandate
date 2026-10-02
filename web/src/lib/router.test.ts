// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { href, parseHash, sectionOf, type Route } from './router.ts';

describe('parseHash', () => {
  it.each<[string, Route]>([
    ['', { name: 'overview' }],
    ['#', { name: 'overview' }],
    ['#/', { name: 'overview' }],
    ['#/agents', { name: 'agents' }],
    ['#/agents/pair', { name: 'pair' }],
    ['#/agents/browser', { name: 'connect' }],
    ['#/agents/pair%3Avoice-assistant', { name: 'agent', id: 'pair:voice-assistant' }],
    ['#/agents/https%3A%2F%2Fclaude.ai%2Foauth%2Fmeta', { name: 'agent', id: 'https://claude.ai/oauth/meta' }],
    ['#/mandates', { name: 'mandates' }],
    ['#/mandates/mandate-voice', { name: 'mandate', id: 'mandate-voice' }],
    ['#/mandates/mandate-voice/versions', { name: 'mandate_versions', id: 'mandate-voice' }],
    ['#/audit', { name: 'audit', query: {} }],
    ['#/audit?decision=deny&decision=default&agent=pair%3Ax', { name: 'audit', query: { decision: ['deny', 'default'], agent: ['pair:x'] } }],
    ['#/audit/requests', { name: 'requests' }],
    ['#/audit/18342', { name: 'audit_entry', seq: 18342 }],
    ['#/settings', { name: 'settings', section: null }],
    ['#/settings/estop', { name: 'settings', section: 'estop' }],
  ])('%s', (hash, want) => {
    expect(parseHash(hash)).toEqual(want);
  });

  it.each([
    '#/agents/unknown/deeper',
    '#/agents/%E0%A4%A',
    '#/agents/',
    '#/mandates/a b',
    '#/mandates/x',
    '#/mandates/mandate-voice/other',
    '#/mandates/a b/versions',
    '#/mandates/mandate-voice/versions/deeper',
    '#/audit/7/versions',
    '#/audit/0',
    '#/audit/12abc',
    '#/audit/99999999999999999999',
    '#/settings/unknown',
    '#//evil.example',
    '#javascript:alert(1)',
    '#/AGENTS',
  ])('%s is not found', (hash) => {
    expect(parseHash(hash)).toEqual({ name: 'not_found' });
  });
});

describe('href', () => {
  it.each<[Route, string]>([
    [{ name: 'overview' }, '#/'],
    [{ name: 'agent', id: 'https://claude.ai/oauth/meta' }, '#/agents/https%3A%2F%2Fclaude.ai%2Foauth%2Fmeta'],
    [{ name: 'mandate', id: 'mandate-voice' }, '#/mandates/mandate-voice'],
    [{ name: 'mandate_versions', id: 'mandate-voice' }, '#/mandates/mandate-voice/versions'],
    [{ name: 'audit', query: { decision: ['deny', 'default'] } }, '#/audit?decision=deny&decision=default'],
    [{ name: 'audit', query: {} }, '#/audit'],
    [{ name: 'audit_entry', seq: 7 }, '#/audit/7'],
    [{ name: 'requests' }, '#/audit/requests'],
    [{ name: 'settings', section: 'estop' }, '#/settings/estop'],
    [{ name: 'settings', section: null }, '#/settings'],
    [{ name: 'pair' }, '#/agents/pair'],
    [{ name: 'connect' }, '#/agents/browser'],
    [{ name: 'agents' }, '#/agents'],
    [{ name: 'mandates' }, '#/mandates'],
  ])('%j → %s and back', (route, hash) => {
    expect(href(route)).toBe(hash);
    expect(parseHash(hash)).toEqual(route);
  });

  it('has no target for not_found', () => {
    expect(href({ name: 'not_found' })).toBe('#/');
  });
});

describe('sectionOf', () => {
  it.each<[Route, string | null]>([
    [{ name: 'overview' }, 'overview'],
    [{ name: 'agent', id: 'x' }, 'agents'],
    [{ name: 'pair' }, 'agents'],
    [{ name: 'mandate', id: 'm-1234' }, 'mandates'],
    [{ name: 'mandate_versions', id: 'm-1234' }, 'mandates'],
    [{ name: 'requests' }, 'audit'],
    [{ name: 'audit_entry', seq: 1 }, 'audit'],
    [{ name: 'settings', section: 'estop' }, 'settings'],
    [{ name: 'not_found' }, null],
  ])('%j is in %s', (route, section) => {
    expect(sectionOf(route)).toBe(section);
  });
});
