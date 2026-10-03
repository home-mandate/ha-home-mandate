// SPDX-License-Identifier: AGPL-3.0-or-later

import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { licenseName, licenseText, packageOf, readPackage } from './licenses.ts';

function pkg(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'pkg-'));
  mkdirSync(dir, { recursive: true });
  for (const [name, content] of Object.entries(files)) writeFileSync(join(dir, name), content);
  return dir;
}

describe('packageOf', () => {
  it('finds the package of a bundled module, also scoped and under pnpm', () => {
    expect(packageOf('/w/node_modules/svelte/src/internal/client/index.js')).toEqual({ name: 'svelte', root: '/w/node_modules/svelte' });
    expect(packageOf('/w/node_modules/.pnpm/svelte@5.1.0/node_modules/svelte/src/index.js')).toEqual({
      name: 'svelte',
      root: '/w/node_modules/.pnpm/svelte@5.1.0/node_modules/svelte',
    });
    expect(packageOf('/w/node_modules/@inlang/paraglide-js/dist/runtime.js')?.name).toBe('@inlang/paraglide-js');
  });

  it('leaves own code out', () => {
    expect(packageOf('/w/src/lib/router.ts')).toBeNull();
    expect(packageOf('\0vite/preload-helper')).toBeNull();
  });
});

describe('licenseText', () => {
  it('lists packages by name with version, license and text, then the extras', () => {
    const text = licenseText(
      [
        { name: 'zeta', version: '1.0.0', license: 'MIT', text: 'Z text' },
        { name: 'alpha', version: '2.0.0', license: 'ISC', text: 'A text' },
      ],
      ['Icons\nApache'],
    );
    expect(text.indexOf('alpha 2.0.0 (ISC)')).toBeLessThan(text.indexOf('zeta 1.0.0 (MIT)'));
    expect(text).toContain('A text');
    expect(text.trimEnd().endsWith('Icons\nApache')).toBe(true);
  });
});

describe('readPackage', () => {
  it('reads the license and notice texts under their usual names', () => {
    const dir = pkg({ 'package.json': '{"name":"a","version":"1.0.0","license":"Apache-2.0"}', 'LICENSE-APACHE': 'Apache text', NOTICE: 'Notice text' });
    expect(readPackage(dir)).toEqual({ name: 'a', version: '1.0.0', license: 'Apache-2.0', text: 'Apache text\n\nNotice text' });
  });

  it('gives an empty text when there is none, and names a broken manifest', () => {
    expect(readPackage(pkg({ 'package.json': '{"name":"b","version":"2.0.0","license":"MIT"}' })).text).toBe('');
    expect(() => readPackage(pkg({ 'package.json': '{' }))).toThrow(/unreadable package.json/);
    expect(() => readPackage(pkg({}))).toThrow(/no package.json/);
  });

  it('reads old forms of the license field', () => {
    expect(licenseName('MIT')).toBe('MIT');
    expect(licenseName({ type: 'BSD-3-Clause' })).toBe('BSD-3-Clause');
    expect(licenseName([{ type: 'MIT' }, { type: 'Apache-2.0' }])).toBe('MIT OR Apache-2.0');
    expect(licenseName(undefined)).toBe('see text');
  });
});
