// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { paramsText } from './params.ts';

describe('paramsText', () => {
  it('shows the service data as name=value pairs in the server’s order', () => {
    expect(paramsText([{ name: 'brightness_pct', value: '100' }, { name: 'color_temp_kelvin', value: '2700' }])).toBe('brightness_pct=100, color_temp_kelvin=2700');
  });

  it('is empty without service data', () => {
    expect(paramsText([])).toBe('');
  });

  it('cleans names and values like other agent text and keeps each at 80 characters', () => {
    const text = paramsText([{ name: 'temp\u202Eerature', value: `35\n${'9'.repeat(200)}` }]);
    expect(text).not.toMatch(/[\u202A-\u202E\n]/);
    expect([...text.split('=')[1]!].length).toBe(80);
  });
});
