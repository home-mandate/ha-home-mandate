// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { licenseText, packageOf } from './licenses.ts';

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
