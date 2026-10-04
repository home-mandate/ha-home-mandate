// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { safeLink } from './url.ts';

describe('safeLink', () => {
  it.each([
    ['https://home.example:8765/pair', 'https://home.example:8765/pair'],
    ['http://localhost:8765/pair', 'http://localhost:8765/pair'],
    ['https://claude.ai/oauth/claude-code-client-metadata', 'https://claude.ai/oauth/claude-code-client-metadata'],
  ])('keeps the web URL %s', (input, want) => {
    expect(safeLink(input)).toBe(want);
  });

  it.each([
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    ' javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'vbscript:msgbox(1)',
    'file:///etc/passwd',
    'voice-assistant',
    '//evil.example/pair',
    '/pair',
    'https://user:pass@home.example/pair',
    '',
  ])('refuses %j', (input) => {
    expect(safeLink(input)).toBeNull();
  });

  it('refuses null', () => {
    expect(safeLink(null)).toBeNull();
  });
});
