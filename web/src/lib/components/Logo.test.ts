// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import Logo from './Logo.svelte';

afterEach(cleanup);

const images = (c: HTMLElement) => [...c.querySelectorAll('img')];

describe('Logo', () => {
  it('renders the light and the dark variant, decorative, 36 px high by default', () => {
    const { container } = render(Logo);
    const [light, dark] = images(container);
    expect(images(container)).toHaveLength(2);
    expect(light?.getAttribute('src')).toBe('./hm-logo.svg');
    expect(dark?.getAttribute('src')).toBe('./hm-logo-dark.svg');
    for (const img of [light, dark]) {
      expect(img?.getAttribute('alt')).toBe('');
      expect(img?.getAttribute('height')).toBe('36');
      expect(img?.getAttribute('width')).toBe('33');
    }
  });

  it('keeps the aspect ratio 1632:1766 instead of a square', () => {
    for (const [size, width] of [
      [48, '44'],
      [72, '67'],
    ] as const) {
      const { container } = render(Logo, { size });
      expect(images(container)[0]?.getAttribute('width')).toBe(width);
      expect(images(container)[0]?.getAttribute('height')).toBe(String(size));
      cleanup();
    }
  });
});
