// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { templatesFixture } from '../api/fixtures.ts';
import type { MandateVersion, RulesFrom } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { renameProposal, rulesFromText as rawRulesFromText, versionOriginText as rawVersionOriginText } from './origin.ts';

/** Template names are isolated (U+2068 … U+2069); the tests read the plain text. */
const plain = (text: string) => text.replace(/[\u2068\u2069]/g, '');
const rulesFromText = (...args: Parameters<typeof rawRulesFromText>) => plain(rawRulesFromText(...args));
const versionOriginText = (...args: Parameters<typeof rawVersionOriginText>) => plain(rawVersionOriginText(...args));

beforeEach(() => setLocale('en', { reload: false }));

const ctx = { locale: 'en', timeZone: 'Europe/Berlin' };
const from = (template: string, edited = false): RulesFrom => ({
  template,
  template_digest: 'sha256:' + 'a'.repeat(64),
  at: '2026-10-06T16:09:00.000Z',
  edited_since: edited,
});
const version = (origin: MandateVersion['origin'], template: string | null = null): MandateVersion => ({
  number: 1,
  digest: 'sha256:' + 'b'.repeat(64),
  created_at: '2026-10-06T16:09:00.000Z',
  created_by: 'u-admin',
  created_by_name: 'Markus',
  origin,
  template,
  template_digest: template === null ? null : 'sha256:' + 'a'.repeat(64),
});

describe('rulesFromText', () => {
  it('names the template by its title and the time in the household’s zone', () => {
    expect(rulesFromText(from('hm-voice-cautious'), templatesFixture, ctx)).toBe(
      'Rules last taken from the template Voice assistant (cautious) on Oct 6, 2026, 6:09 PM',
    );
    expect(rulesFromText(from('voice-assistant'), templatesFixture, ctx)).toContain('the template voice-assistant on');
  });

  it('says when the rules were edited since', () => {
    expect(rulesFromText(from('read-only', true), templatesFixture, ctx)).toBe(
      'Rules last taken from the template read-only on Oct 6, 2026, 6:09 PM, edited since',
    );
  });

  it('isolates the template’s name from the text around it', () => {
    expect(rawRulesFromText(from('read-only'), templatesFixture, ctx)).toContain('\u2068read-only\u2069');
    expect(rawVersionOriginText(version('template', 'read-only'), templatesFixture)).toBe('From the template \u2068read-only\u2069');
  });

  it('shows a removed template by its cleaned name and says nothing without an origin', () => {
    expect(rulesFromText(from('gone\u202e-x'), [], ctx)).toContain('the template gone-x on');
    expect(rulesFromText(null, templatesFixture, ctx)).toBe('');
  });

  it('is translated', () => {
    setLocale('de', { reload: false });
    expect(rulesFromText(from('hm-voice-cautious', true), templatesFixture, { locale: 'de', timeZone: 'Europe/Berlin' })).toContain(
      'Sprachassistent (vorsichtig)',
    );
  });
});

describe('versionOriginText', () => {
  it('says where the rules of a version came from; nothing for versions of before', () => {
    expect(versionOriginText(version('template', 'hm-read-only'), templatesFixture)).toBe('From the template Read only');
    expect(versionOriginText(version('edit'), templatesFixture)).toBe('Edited');
    expect(versionOriginText(version('unknown'), templatesFixture)).toBe('');
    expect(versionOriginText(version('template', null), templatesFixture)).toBe('');
  });
});

describe('renameProposal', () => {
  it('proposes the agent’s name while the mandate still carries the previous template’s name', () => {
    expect(renameProposal('voice-assistant', 'Claude Desktop', from('voice-assistant'), templatesFixture)).toBe('Claude Desktop');
    // A base template by its name or its title in any language.
    expect(renameProposal('hm-voice-cautious', ' Claude ', from('hm-voice-cautious'), templatesFixture)).toBe('Claude');
    expect(renameProposal('Sprachassistent (vorsichtig)', 'Claude', from('hm-voice-cautious'), templatesFixture)).toBe('Claude');
    // A template removed since: its name still counts.
    expect(renameProposal('old-one', 'Claude', from('old-one'), templatesFixture)).toBe('Claude');
  });

  it('proposes nothing for a name the human chose, or when the name already fits', () => {
    expect(renameProposal('Küche', 'Claude', from('voice-assistant'), templatesFixture)).toBeNull();
    expect(renameProposal('read-only', 'Claude', from('voice-assistant'), templatesFixture)).toBeNull();
    expect(renameProposal('Claude', 'Claude', from('voice-assistant'), templatesFixture)).toBeNull();
    expect(renameProposal('voice-assistant', '  ', from('voice-assistant'), templatesFixture)).toBeNull();
  });

  it('compares with every template for mandates of before (no known origin)', () => {
    expect(renameProposal('hm-read-only', 'Claude', null, templatesFixture)).toBe('Claude');
    expect(renameProposal('Read only', 'Claude', null, templatesFixture)).toBe('Claude');
    expect(renameProposal('Mein Mandat', 'Claude', null, templatesFixture)).toBeNull();
  });
});
