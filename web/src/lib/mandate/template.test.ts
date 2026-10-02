// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { devicesFixture, templatesFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { templateChips, templateName } from './template.ts';

beforeEach(() => setLocale('en', { reload: false }));

const chips = (name: string) => {
  const draft = templatesFixture.find((t) => t.name === name)?.draft;
  if (!draft) throw new Error(`no template ${name}`);
  return templateChips(draft, devicesFixture, 'en').map((c) => [c.kind, c.text]);
};

describe('templateChips', () => {
  it('shows a single rule with its actions', () => {
    expect(chips('read-only')).toEqual([
      ['allow', 'All devices: read'],
      ['default', 'Default: denied'],
    ]);
  });

  it('lists what several rules of a decision cover, each once', () => {
    expect(chips('voice-assistant')).toEqual([
      ['allow', 'Lights, Climate, Media'],
      ['ask', 'Haustür: read, unlock'],
      ['deny', 'Camera: all actions'],
      ['default', 'Default: denied'],
    ]);
  });

  it('shows only the default for an empty template', () => {
    expect(chips('empty')).toEqual([['default', 'Default: denied']]);
  });

  it('names a single rule with many actions by its subject, with the area', () => {
    const draft = templatesFixture[0]?.draft;
    if (!draft) throw new Error('no template');
    const many = { ...draft, rules: [{ id: 'l', resource: { category: 'light' as const, area: 'kitchen' }, actions: ['read', 'turn_on', 'turn_off'], decision: 'allow' as const }] };
    expect(templateChips(many, devicesFixture, 'en')[0]).toEqual({ kind: 'allow', text: 'Lights · Küche' });
  });
});

describe('templateName', () => {
  it('translates the shipped templates and shows others by their own name', () => {
    expect(templateName('read-only')).toBe('Read only');
    expect(templateName('voice-assistant')).toBe('Voice assistant');
    expect(templateName('empty')).toBe('Empty');
    expect(templateName('Garage\u202E helper')).toBe('Garage helper');
    expect(templateName('constructor')).toBe('constructor');
  });
});
