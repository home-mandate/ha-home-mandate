// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { templatesFixture } from '../api/fixtures.ts';
import { createMockClient } from '../api/mock.ts';
import type { Template } from '../api/types.ts';
import { setLocale } from '../paraglide/runtime.js';
import { cautiousChoice, nameProblem, offeredTemplates, templateDescription, templateTitle, titleOf } from './template.ts';

beforeEach(() => setLocale('en', { reload: false }));

const named = (name: string): Template => {
  const t = templatesFixture.find((x) => x.name === name);
  if (!t) throw new Error(`no template ${name}`);
  return t;
};

describe('templateTitle and templateDescription', () => {
  it('show a base template in the UI language', () => {
    expect(templateTitle(named('hm-read-only'))).toBe('Read only');
    expect(templateDescription(named('hm-read-only'))).toBe('May read the state of every device, but switches nothing.');
    setLocale('de', { reload: false });
    expect(templateTitle(named('hm-voice-cautious'))).toBe('Sprachassistent (vorsichtig)');
  });

  it('fall back to English, then to the name', () => {
    const t = { ...named('hm-read-only'), title: { en: 'Read only' }, description: {} };
    setLocale('de', { reload: false });
    expect(templateTitle(t)).toBe('Read only');
    expect(templateDescription(t)).toBe('');
    expect(templateTitle({ ...t, title: {} })).toBe('hm-read-only');
  });

  it('show the household’s own templates by their name, cleaned, without a description', () => {
    const own = { ...named('voice-assistant'), name: 'garage‮-helper', title: { en: 'Fake title' }, description: { en: 'Fake' } };
    expect(templateTitle(own)).toBe('garage-helper');
    expect(templateDescription(own)).toBe('');
  });

  it('find a title by name, or show the name of an unknown template', () => {
    expect(titleOf('hm-light-climate', templatesFixture)).toBe('Light and climate');
    expect(titleOf('gone', templatesFixture)).toBe('gone');
  });
});

describe('nameProblem', () => {
  it('accepts what the server accepts', () => {
    expect(nameProblem('guest-room', templatesFixture)).toBeNull();
    expect(nameProblem('a', templatesFixture)).toBeNull();
    expect(nameProblem('0'.repeat(32), templatesFixture)).toBeNull();
  });

  it.each(['', '-guest', 'Guest', 'gäste', 'guest room', 'a'.repeat(33), 'guest_room'])('refuses the form of %j', (name) => {
    expect(nameProblem(name, templatesFixture)).toBe('format');
  });

  it('keeps "hm-" for base templates and refuses names that exist', () => {
    expect(nameProblem('hm-mine', templatesFixture)).toBe('reserved');
    expect(nameProblem('hm-read-only', templatesFixture)).toBe('reserved');
    expect(nameProblem('voice-assistant', templatesFixture)).toBe('taken');
  });
});

describe('offeredTemplates', () => {
  it('loads every template with its rules, but not hidden base templates', async () => {
    const api = createMockClient();
    await api.setTemplateHidden('hm-light-climate', true);
    const offered = await offeredTemplates(api);
    expect(offered.map((t) => t.name)).toEqual(['hm-read-only', 'hm-voice-cautious', 'empty', 'read-only', 'voice-assistant']);
    expect(offered[0]?.draft.rules).toHaveLength(1);
  });

  it('leaves out a template that cannot be read', async () => {
    const api = createMockClient();
    const template = api.template;
    api.template = async (name) => (name === 'empty' ? Promise.reject(new Error('gone')) : template(name));
    expect((await offeredTemplates(api)).map((t) => t.name)).not.toContain('empty');
  });
});

describe('cautiousChoice', () => {
  const t = (name: string, rules: number, builtin = false) => ({ name, builtin, draft: { ...named('empty').draft, rules: named('voice-assistant').draft.rules.slice(0, rules) } });

  it('prefers a template that grants nothing, wherever it is and whatever its name', () => {
    expect(cautiousChoice([t('hm-read-only', 1, true), t('guest', 2), t('nothing', 0)])).toBe('nothing');
  });

  it('else takes the first base template, else the first, else none', () => {
    expect(cautiousChoice([t('guest', 2), t('hm-read-only', 1, true), t('hm-light-climate', 4, true)])).toBe('hm-read-only');
    expect(cautiousChoice([t('guest', 2), t('more', 3)])).toBe('guest');
    expect(cautiousChoice([])).toBe('');
  });
});
