// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it } from 'vitest';
import { approvalsOpenFixture, BIDI_NAME } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import { openedText } from './live.ts';

beforeEach(() => setLocale('en', { reload: false }));

const plain = (text: string) => text.replace(/[\u2068\u2069]/g, '');

describe('openedText', () => {
  it('says who wants what, with agent and device isolated', () => {
    const text = openedText(approvalsOpenFixture[0]!);
    expect(plain(text)).toBe('New approval request: Claude Code wants to unlock Haustür');
    expect(text).toContain('\u2068Claude Code\u2069');
    expect(text).toContain('\u2068Haustür\u2069');
  });

  it('cleans a hostile agent name', () => {
    const text = openedText({ ...approvalsOpenFixture[0]!, agent: { client_id: 'pair:x', display_name: BIDI_NAME } });
    expect(text).not.toMatch(/[\u202A-\u202E\u2066\u2067]/);
  });
});
